#include <cstdlib>
#include <cstring>
#include <cstdio>

#include "artifacts.h"
#include "board_i2c.h"
#include "board_init.h"
#include "board_keys.h"
#include "board_pins.h"
#include "board_power.h"
#include "board_sd.h"
#include "board_sleep.h"
#include "board_wifi.h"
#include "device_status.h"
#include "driver/usb_serial_jtag.h"
#include "esp_attr.h"
#include "esp_timer.h"
#include "led_fsm.h"
#include "ota_update.h"
#include "shtc3.h"
#include "nvs_flash.h"
#include "ota_service.h"
#include "epd709.h"
#include "epd_fb.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "status.h"

namespace {

constexpr const char *kTag = "FrameFolio";

// 温湿度采样周期。上板验证用 5 s，方便对着传感器确认响应。
constexpr uint32_t kEnvPollMs = 5000;

// 电池采样周期。电压是慢变量，30 s 一次足够，也避免频繁开合分压开关。
constexpr uint32_t kPowerPollMs = 30000;

// 空闲多久进深睡。只在电池供电时生效：插着电说明人在旁边，而且睡了就收不
// 到 OTA，所以插电时不做自动睡眠。
constexpr uint32_t kIdleSleepMs = 30000;

// 最近一次显示到第几张。放进 RTC 内存，深睡唤醒后接着上一张，不会跳回第一张。
RTC_DATA_ATTR int s_frame_index;

// 是否接着充电器。power_task 每 30 s 写一次，display_task 读，单字节不加锁。
bool s_plugged;

const char *charge_name(board_charge_t charge)
{
    switch (charge) {
    case BOARD_CHARGE_CHARGING: return "充电中";
    case BOARD_CHARGE_FULL:     return "已充满";
    case BOARD_CHARGE_IDLE:     return "未接充电";
    default:                    return "未知";
    }
}

void power_task(void *)
{
    board_charge_t last_charge = BOARD_CHARGE_UNKNOWN;
    int last_percent = -1;

    for (;;) {
        board_power_sample_t sample = {};
        if (board_power_read(&sample) == ESP_OK) {
            deviceStatusSetPower(sample);

            // 插着电就一直显示电量色；电量变化超过 5% 才重播一次，避免每 30 s 闪一下。
            const bool plugged = sample.charge == BOARD_CHARGE_CHARGING ||
                                 sample.charge == BOARD_CHARGE_FULL;
            const bool just_plugged = plugged && last_charge != BOARD_CHARGE_CHARGING &&
                                      last_charge != BOARD_CHARGE_FULL;
            const bool moved = plugged && last_percent >= 0 &&
                               std::abs(static_cast<int>(sample.percent) - last_percent) >= 5;
            if (last_percent < 0 || just_plugged || moved) {
                led_event_post(LED_EV_BATTERY, sample.percent);
                last_percent = sample.percent;
            }
            led_state_set(LED_STATE_CHARGING, plugged);
            s_plugged = plugged;
            last_charge = sample.charge;

            ESP_LOGI(kTag, "电池 %d mV（%u%%），%s", sample.millivolts,
                     static_cast<unsigned>(sample.percent), charge_name(sample.charge));
        } else {
            ESP_LOGW(kTag, "电池采样失败");
        }
        vTaskDelay(pdMS_TO_TICKS(kPowerPollMs));
    }
}

void env_task(void *)
{
    for (;;) {
        shtc3_sample_t sample = {};
        if (shtc3_read(&sample) == ESP_OK) {
            deviceStatusSetEnv(sample);
            ESP_LOGI(kTag, "温湿度 %.1f °C / %.1f %%RH", sample.temperature_c, sample.humidity_percent);
        } else {
            ESP_LOGW(kTag, "温湿度读取失败");
        }
        vTaskDelay(pdMS_TO_TICKS(kEnvPollMs));
    }
}

/** @brief 把 back buffer 刷上屏；刷新期间点亮刷屏灯语。 */
bool present_back(const char *what)
{
    led_state_set(LED_STATE_EPD_BUSY, true);
    const esp_err_t err = epd_fb_present();
    led_state_set(LED_STATE_EPD_BUSY, false);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "刷屏失败：%s", esp_err_to_name(err));
        led_event_post(LED_EV_ANOMALY, 0);
        return false;
    }
    ESP_LOGI(kTag, "%s已上屏", what);
    led_event_post(LED_EV_DONE, 0);
    return true;
}

/** @brief 把卡上第 index 张读进帧缓冲并刷上屏。 */
bool show_card_frame(int index, int count)
{
    char path[300] = {};
    if (board_sd_frame_at(index, path, sizeof(path)) != ESP_OK) {
        return false;
    }
    if (board_sd_load_frame(path) != ESP_OK) {
        return false;
    }
    ESP_LOGI(kTag, "第 %d/%d 张：%s", index + 1, count, path);
    return present_back("画面");
}

/**
 * @brief 睡前收尾并进入深度睡眠，不返回。
 *
 * 顺序不能换：先熄灯、卸载文件系统、把面板控制脚放开，最后才断 3V3_EXT 并睡。
 * 面板断电时控制脚若还拉着高电平，会经 ESD 二极管倒灌进已经关断的电源轨。
 */
[[noreturn]] void enter_deep_sleep()
{
    led_fsm_suspend();   // 未初始化时返回错误，灯本来就没亮过，忽略
    board_sd_unmount();  // 让 FAT 落盘、卡退出传输态
    epdParkBus();
    framefolio::board_sleep::enter(framefolio::board_sleep::kTimerWakeSeconds);
}

/**
 * @brief 现在能不能进深睡。
 *
 * @param[in] idle_since_us 上一次有命令入队的时刻。
 * @return 电池供电、空闲够久、且没有 OTA 在写 Flash 时返回 true。
 */
bool sleep_allowed(int64_t idle_since_us)
{
    if (s_plugged) {
        return false;
    }
    if (esp_timer_get_time() - idle_since_us < static_cast<int64_t>(kIdleSleepMs) * 1000) {
        return false;
    }
    otaStatus_t status = {};
    otaUpdateGetStatus(&status);
    return status.phase == otaIdle;  // 传固件时睡着会中断，留到下次空闲再睡
}

/** @brief 刷屏命令。按键、串口、HTTP 三个来源都投到这里，由显示任务串行执行。 */
enum class display_cmd { refresh, next, prev, factory, sleep };

/** @brief 按键语义转成刷屏命令。KEY3 没有翻页含义，当作原地重刷。 */
display_cmd cmd_for_key(board_key_t key)
{
    switch (key) {
    case BOARD_KEY_PREV: return display_cmd::prev;
    case BOARD_KEY_NEXT: return display_cmd::next;
    default:             return display_cmd::refresh;
    }
}

QueueHandle_t s_cmds;

esp_err_t post_display_cmd(display_cmd cmd)
{
    if (s_cmds == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    return xQueueSend(s_cmds, &cmd, 0) == pdTRUE ? ESP_OK : ESP_ERR_NO_MEM;
}

/** @brief 卡上没有帧文件时的退路：artifacts 里的画面，再没有就白屏。 */
bool refresh_fallback()
{
    if (artifacts_draw_boot_frame() == ESP_OK) {
        return present_back("artifacts 画面");
    }
    ESP_LOGW(kTag, "卡上和 artifacts 都没有可用画面，改刷白屏");
    epd_fb_clear(WHITE);
    return present_back("白屏");
}

/**
 * @brief 调试串口输入：r 刷新、n 下一张、p 上一张。
 *
 * 串口是 USB-SERIAL-JTAG，和日志同一个口，这里只读不写。
 */
void serial_cmd_task(void *)
{
    for (;;) {
        uint8_t ch = 0;
        if (usb_serial_jtag_read_bytes(&ch, 1, pdMS_TO_TICKS(500)) != 1) {
            continue;
        }
        switch (ch) {
        case 'r':
        case 'R':
            post_display_cmd(display_cmd::refresh);
            break;
        case 'n':
        case 'N':
            post_display_cmd(display_cmd::next);
            break;
        case 'p':
        case 'P':
            post_display_cmd(display_cmd::prev);
            break;
        case 'f':
        case 'F':
            post_display_cmd(display_cmd::factory);
            break;
        case 's':
        case 'S':
            post_display_cmd(display_cmd::sleep);
            break;
        case '\r':
        case '\n':
            break;
        default:
            ESP_LOGI(kTag, "串口命令：r 刷新 / n 下一张 / p 上一张 / f 出厂画面 / s 立即深睡");
            break;
        }
    }
}

esp_err_t reply_display_cmd(httpd_req_t *request, display_cmd cmd, const char *text)
{
    post_display_cmd(cmd);
    httpd_resp_set_type(request, "text/plain");
    return httpd_resp_sendstr(request, text);
}

esp_err_t handle_http_refresh(httpd_req_t *request)
{
    return reply_display_cmd(request, display_cmd::refresh, "refresh queued\n");
}

esp_err_t handle_http_next(httpd_req_t *request)
{
    return reply_display_cmd(request, display_cmd::next, "next queued\n");
}

esp_err_t handle_http_prev(httpd_req_t *request)
{
    return reply_display_cmd(request, display_cmd::prev, "prev queued\n");
}

esp_err_t handle_http_factory(httpd_req_t *request)
{
    return reply_display_cmd(request, display_cmd::factory, "factory queued\n");
}

esp_err_t handle_http_sleep(httpd_req_t *request)
{
    return reply_display_cmd(request, display_cmd::sleep, "sleep queued\n");
}

// 路由结构必须活到 HTTP 服务结束，所以放静态存储，启动时填一次。
httpd_uri_t s_uri_refresh;
httpd_uri_t s_uri_next;
httpd_uri_t s_uri_prev;
httpd_uri_t s_uri_factory;
httpd_uri_t s_uri_sleep;

void register_display_routes()
{
    s_uri_refresh = {};
    s_uri_refresh.uri = "/display/refresh";
    s_uri_refresh.method = HTTP_GET;
    s_uri_refresh.handler = handle_http_refresh;

    s_uri_next = {};
    s_uri_next.uri = "/display/next";
    s_uri_next.method = HTTP_GET;
    s_uri_next.handler = handle_http_next;

    s_uri_prev = {};
    s_uri_prev.uri = "/display/prev";
    s_uri_prev.method = HTTP_GET;
    s_uri_prev.handler = handle_http_prev;

    s_uri_factory = {};
    s_uri_factory.uri = "/display/factory";
    s_uri_factory.method = HTTP_GET;
    s_uri_factory.handler = handle_http_factory;

    s_uri_sleep = {};
    s_uri_sleep.uri = "/power/sleep";
    s_uri_sleep.method = HTTP_GET;
    s_uri_sleep.handler = handle_http_sleep;

    const esp_err_t first = otaServiceRegisterHandler(&s_uri_refresh);
    const esp_err_t second = otaServiceRegisterHandler(&s_uri_next);
    const esp_err_t third = otaServiceRegisterHandler(&s_uri_prev);
    const esp_err_t fourth = otaServiceRegisterHandler(&s_uri_factory);
    const esp_err_t fifth = otaServiceRegisterHandler(&s_uri_sleep);

    const esp_err_t results[] = {first, second, third, fourth, fifth};
    for (const esp_err_t err : results) {
        if (err != ESP_OK) {
            ESP_LOGE(kTag, "控制接口注册失败：%s", esp_err_to_name(err));
            return;
        }
    }
    ESP_LOGI(kTag, "控制接口：GET /display/refresh|next|prev|factory、/power/sleep");
}

/**
 * @brief 显示任务：初始化面板后等命令，不主动刷屏。
 *
 * 面板是双稳态的，上电后保持上一次的画面，所以开机不刷能省下几十秒，
 * 重烧固件也不会再把屏刷一遍。按键、串口、HTTP 三条路都进 queue set，
 * 谁先到先处理；刷新本身在任务里串行，一次几十秒，期间积压的命令合并成
 * 一个净步数，只刷一次。
 */
void display_task(void *)
{
    // 3V3_EXT 在这里才打开：定时唤醒的空转走不到本任务，面板也就不会被上电。
    board_power_rail_on();

    if (initEPD() != DONE) {
        ESP_LOGE(kTag, "EPD init 失败：BUSY 未拉高，先查 3V3_EXT(POWER_CTL) / RST / BUSY 走线");
        led_event_post(LED_EV_ANOMALY, 0);
        vTaskDelete(nullptr);
        return;
    }
    ESP_LOGI(kTag, "PSRAM 空闲 %u B", (unsigned)heap_caps_get_free_size(MALLOC_CAP_SPIRAM));

    if (epd_fb_init() != ESP_OK) {
        if (epdDisplayColorBar() == DONE) {
            ESP_LOGW(kTag, "PSRAM 不可用，降级为六色条纹");
            led_event_post(LED_EV_DONE, 0);
        } else {
            ESP_LOGE(kTag, "六色条纹刷新失败");
            led_event_post(LED_EV_ANOMALY, 0);
        }
        vTaskDelete(nullptr);  // 没有帧缓冲，翻页也无从谈起
        return;
    }

    QueueSetHandle_t set = xQueueCreateSet(16);
    if (set == nullptr || xQueueAddToSet(board_keys_queue(), set) != pdPASS ||
        xQueueAddToSet(s_cmds, set) != pdPASS) {
        ESP_LOGE(kTag, "命令队列集合创建失败");
        vTaskDelete(nullptr);
        return;
    }

    // 最后一次有命令的时刻，深睡判断拿它算空闲时长。
    int64_t idle_since = esp_timer_get_time();
    for (;;) {
        // 等命令的同时每秒醒一次，看看是不是该睡了。
        const QueueSetMemberHandle_t active = xQueueSelectFromSet(set, pdMS_TO_TICKS(1000));
        if (active == nullptr) {
            if (sleep_allowed(idle_since)) {
                ESP_LOGI(kTag, "空闲 %u ms 且未接充电器，进入深度睡眠", (unsigned)kIdleSleepMs);
                enter_deep_sleep();
            }
            continue;
        }
        idle_since = esp_timer_get_time();

        int step = 0;
        bool force = false;
        bool factory = false;
        bool sleep_now = false;
        if (active == board_keys_queue()) {
            board_key_event_t event = {};
            if (xQueueReceive(board_keys_queue(), &event, 0) != pdTRUE) {
                continue;
            }
            if (event.key == BOARD_KEY_PREV) {
                step = -1;
            } else if (event.key == BOARD_KEY_NEXT) {
                step = 1;
            } else {
                force = true;  // KEY3 = 原地重刷
            }
        } else {
            display_cmd cmd = display_cmd::refresh;
            if (xQueueReceive(s_cmds, &cmd, 0) != pdTRUE) {
                continue;
            }
            if (cmd == display_cmd::next) {
                step = 1;
            } else if (cmd == display_cmd::prev) {
                step = -1;
            } else if (cmd == display_cmd::factory) {
                factory = true;
            } else if (cmd == display_cmd::sleep) {
                sleep_now = true;
            } else {
                force = true;
            }
        }

        // 刷新期间积压的命令合并成一次动作。
        board_key_event_t key = {};
        while (xQueueReceive(board_keys_queue(), &key, 0) == pdTRUE) {
            if (key.key == BOARD_KEY_PREV) {
                --step;
            } else if (key.key == BOARD_KEY_NEXT) {
                ++step;
            } else {
                force = true;
            }
        }
        display_cmd pending = display_cmd::refresh;
        while (xQueueReceive(s_cmds, &pending, 0) == pdTRUE) {
            if (pending == display_cmd::next) {
                ++step;
            } else if (pending == display_cmd::prev) {
                --step;
            } else if (pending == display_cmd::factory) {
                factory = true;
            } else if (pending == display_cmd::sleep) {
                sleep_now = true;
            } else {
                force = true;
            }
        }

        // 显式的睡眠命令优先：不看充电器和 OTA 状态，是用户主动要求的。
        if (sleep_now) {
            ESP_LOGI(kTag, "收到睡眠命令");
            enter_deep_sleep();
        }

        const int count = board_sd_frame_count();
        if (factory) {
            // 出厂画面固定来自 artifacts，不管卡上有没有帧文件。
            if (artifacts_draw_boot_frame() == ESP_OK) {
                present_back("出厂画面");
            } else {
                ESP_LOGW(kTag, "artifacts 里没有出厂画面，改刷白屏");
                epd_fb_clear(WHITE);
                present_back("白屏");
            }
        } else if (count > 0) {
            s_frame_index = ((s_frame_index + step) % count + count) % count;
            if (!show_card_frame(s_frame_index, count)) {
                ESP_LOGE(kTag, "第 %d/%d 张载入或上屏失败", s_frame_index + 1, count);
                led_event_post(LED_EV_ANOMALY, 0);
            }
        } else if (step != 0 && !force) {
            ESP_LOGW(kTag, "卡上没有可用图片，不翻页");
            led_event_post(LED_EV_NO_IMAGE, 0);
        } else {
            if (board_sd_mounted()) {
                ESP_LOGI(kTag, "TF 卡上没有可用帧文件，改用 artifacts 里的画面");
            }
            refresh_fallback();
        }
    }
}

}  // namespace

extern "C" void app_main(void)
{
    // 深睡会锁住一部分脚的保持位，开机第一件事先解开，否则 LED / ADC_CTL 可能一直是死的。
    framefolio::board_sleep::release_holds();

    board_gpio_init();

    const framefolio::board_sleep::wake_info wake = framefolio::board_sleep::wakeup();
    bool refresh_on_start = false;
    if (wake.timer) {
        // 定时唤醒只做两件事：看是不是插上了充电器；到点就补刷一张防残影。
        board_power_sample_t sample = {};
        const bool sampled = board_power_init() == ESP_OK && board_power_read(&sample) == ESP_OK;
        const bool plugged = sampled && (sample.charge == BOARD_CHARGE_CHARGING ||
                                         sample.charge == BOARD_CHARGE_FULL);
        if (plugged) {
            ESP_LOGI(kTag, "定时唤醒：检测到充电器，按正常开机处理");
        } else if (!framefolio::board_sleep::take_refresh_slot()) {
            ESP_LOGI(kTag, "定时唤醒：没有接充电器，继续睡");
            framefolio::board_sleep::enter(framefolio::board_sleep::kTimerWakeSeconds);
        } else {
            ESP_LOGI(kTag, "定时唤醒：到了补刷时间，本次开机刷一张防残影");
            refresh_on_start = true;
        }
    }

    ESP_ERROR_CHECK(led_fsm_init());

    // 必须先于 nvs_flash_init()：待确认镜像遇到 NVS 错误时不能擦除，
    // 否则回滚回去的旧镜像会丢数据。
    ESP_ERROR_CHECK(otaServiceInit());
    // 让 /ota/status 顺带带回电池与温湿度，没有 USB 时也能从局域网看运行状态。
    otaServiceSetStatusExtra(deviceStatusJson);
    esp_err_t ret = nvs_flash_init();
    if ((ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) &&
        !otaServicePendingVerification()) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    // 温湿度挂在 3V3_MCU 常电上，不依赖 3V3_EXT，可以先于屏幕起来。
    if (board_i2c_init() == ESP_OK && shtc3_init(board_i2c_bus()) == ESP_OK) {
        if (xTaskCreate(env_task, "env", 4096, nullptr, 3, nullptr) != pdPASS) {
            ESP_LOGE(kTag, "温湿度任务创建失败");
        }
    }

    if (board_power_init() == ESP_OK) {
        if (xTaskCreate(power_task, "power", 3072, nullptr, 3, nullptr) != pdPASS) {
            ESP_LOGE(kTag, "电池采样任务创建失败");
        }
    }

    // TF 卡：插着就挂上并把根目录打出来，没卡不影响开机自检。
    if (board_sd_mount() == ESP_OK) {
        const int entries = board_sd_list(BOARD_SD_MOUNT_POINT, 20);
        ESP_LOGI(kTag, "根目录共 %d 个条目", entries);
    }

    // 连不上热点不停机：屏幕仍要能显示，60 s 回滚窗口照常走。
    // 连接是异步的，这里只保证射频和 HTTP 服务都起来了，拿到 IP 由事件回调打印。
    const esp_err_t wifi = board_wifi_start_sta();
    if (wifi == ESP_OK) {
        ESP_ERROR_CHECK(otaServiceStart());
        register_display_routes();
    } else {
        ESP_LOGE(kTag, "Wi-Fi 未启动：%s，本次开机无法 OTA", esp_err_to_name(wifi));
    }

    ESP_LOGI(kTag, "board ready");
    ESP_LOGI(kTag, "epd   sclk=%d mosi=%d miso=%d cs0=%d cs1=%d dc=%d rst=%d busy=%d",
             framefolio::board::kEpdSclk, framefolio::board::kEpdMosi, framefolio::board::kEpdMiso,
             framefolio::board::kEpdCs0, framefolio::board::kEpdCs1, framefolio::board::kEpdDc,
             framefolio::board::kEpdRst, framefolio::board::kEpdBusy);
    ESP_LOGI(kTag, "sd    clk=%d cmd=%d d0=%d d1=%d d2=%d d3=%d cd=%d",
             framefolio::board::kSdClk, framefolio::board::kSdCmd, framefolio::board::kSdD0,
             framefolio::board::kSdD1, framefolio::board::kSdD2, framefolio::board::kSdD3,
             framefolio::board::kSdCardDetect);
    ESP_LOGI(kTag, "keys  %d %d %d | led=%d | i2c sda=%d scl=%d",
             framefolio::board::kKey1, framefolio::board::kKey2, framefolio::board::kKey3,
             framefolio::board::kLedWs2812, framefolio::board::kI2cSda, framefolio::board::kI2cScl);
    ESP_LOGI(kTag, "pwr   en=%d adc=%d ctl=%d chg=%d full=%d",
             framefolio::board::kPeriphPowerEn, framefolio::board::kBatteryAdc, framefolio::board::kBatteryAdcCtl,
             framefolio::board::kPowerChgStat, framefolio::board::kPowerFullStat);

    // 三条刷屏命令来源共用一条队列：按键、调试串口、HTTP。
    s_cmds = xQueueCreate(8, sizeof(display_cmd));
    if (s_cmds == nullptr) {
        ESP_LOGE(kTag, "命令队列创建失败");
    }

    // 唤醒的那一下按键本身就是一次操作，补投成命令，否则用户得按第二次才看得到画面。
    if (wake.key) {
        ESP_LOGI(kTag, "按键唤醒，按 KEY%d 处理", (int)wake.which + 1);
        post_display_cmd(cmd_for_key(wake.which));
    } else if (refresh_on_start) {
        post_display_cmd(display_cmd::refresh);
    }

    // 按键用下降沿中断，必须先于显示任务注册，否则第一次翻页会丢。
    if (board_keys_init() != ESP_OK) {
        ESP_LOGE(kTag, "按键不可用，翻页功能失效");
    }

    // 调试串口：USB-SERIAL-JTAG 的输入方向，和日志同一个口。
    usb_serial_jtag_driver_config_t serial_cfg = {};
    serial_cfg.tx_buffer_size = 256;
    serial_cfg.rx_buffer_size = 256;
    if (usb_serial_jtag_driver_install(&serial_cfg) == ESP_OK) {
        if (xTaskCreate(serial_cmd_task, "serial_cmd", 3072, nullptr, 3, nullptr) != pdPASS) {
            ESP_LOGE(kTag, "串口命令任务创建失败");
        }
    } else {
        ESP_LOGW(kTag, "USB 串口驱动安装失败，串口命令不可用");
    }

    if (xTaskCreate(display_task, "display", 8192, nullptr, 5, nullptr) != pdPASS) {
        ESP_LOGE(kTag, "显示任务创建失败");
    }

    // 放在最后：未调用则新镜像 60 s 后被回滚。
    otaServiceReady();
}
