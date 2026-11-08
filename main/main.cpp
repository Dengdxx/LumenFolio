#include <atomic>
#include <cstdarg>
#include <cstdio>
#include <cstdlib>
#include <cstring>

#include "app.h"
#include "artifacts.h"
#include "console.h"
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
#include "esp_http_server.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "status.h"

namespace {

constexpr const char *kTag = "LumenFolio";

// 温湿度采样周期。上板验证用 5 s，方便对着传感器确认响应。
constexpr uint32_t kEnvPollMs = 5000;

// 电池采样周期。电压是慢变量，30 s 一次足够，也避免频繁开合分压开关。
constexpr uint32_t kPowerPollMs = 30000;

// 空闲多久进深睡。开发阶段放宽到 5 min：30 s 在台面上频繁打断操作。
// 只在电池供电且没有 USB 主机时生效；插着电说明人在旁边，睡了也收不到 OTA。
constexpr uint32_t kIdleSleepMs = 5 * 60 * 1000;

// 最近一次显示到第几张。放进 RTC 内存，深睡唤醒后接着上一张，不会跳回第一张。
RTC_DATA_ATTR int s_frame_index;

// 是否接着充电器。power_task 每 30 s 写一次，display_task 读，单字节不加锁。
bool s_plugged;

/** @brief 显示任务发布的状态，供电源守护与命令入口读取。 */
enum class display_state { starting, idle, busy, dead };
std::atomic<display_state> s_display_state = display_state::starting;
const char *s_last_result = "-";   // 最近一次刷屏的结果，指向字符串常量
lumenfolio::board_sleep::wake_info s_wake;  // 本次启动的唤醒来源，开机时填一次
int s_frame_count;                 // 卡上帧文件数量，显示任务每次扫描后更新
int64_t s_idle_since_us;           // 上一次有命令的时刻
volatile bool s_sleep_requested;   // 控制台要求立刻睡
volatile bool s_sleep_hold;        // 本次开机不自动睡（台面调试用）
volatile bool s_frame_transfer_busy; // 正在处理画面请求，这段时间不自动睡

const char *display_state_name()
{
    switch (s_display_state) {
    case display_state::starting: return "starting";
    case display_state::idle:     return "idle";
    case display_state::busy:     return "busy";
    default:                      return "dead";
    }
}

/** @brief 往缓冲末尾追加一行，装不下就丢掉这一段。 */
void append(char *buf, size_t size, size_t *used, const char *fmt, ...)
{
    if (*used + 1 >= size) {
        return;
    }
    va_list args;
    va_start(args, fmt);
    const int written = vsnprintf(buf + *used, size - *used, fmt, args);
    va_end(args);
    if (written > 0 && static_cast<size_t>(written) < size - *used) {
        *used += static_cast<size_t>(written);
    }
}

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

/** @brief 队列按值保存目标，显示任务不借用 HTTP 请求缓冲。 */
struct display_request {
    display_cmd cmd;
    char sd_name[BOARD_SD_FRAME_NAME_BYTES];
};

/** @brief 只刷指定来源，读取失败时保留屏幕现有画面。 */
esp_err_t show_picked_frame(const display_request &request)
{
    if (request.cmd != display_cmd::pick_sd) {
        const bool inbox = request.cmd == display_cmd::pick_inbox;
        const esp_err_t err = inbox ? artifacts_draw_inbox_frame() : artifacts_draw_boot_frame();
        if (err != ESP_OK) return err;
        return present_back(inbox ? "上传画面" : "开机画面") ? ESP_OK : ESP_FAIL;
    }
    char path[sizeof(BOARD_SD_MOUNT_POINT) + BOARD_SD_FRAME_NAME_BYTES];
    snprintf(path, sizeof(path), "%s/%s", BOARD_SD_MOUNT_POINT, request.sd_name);
    const esp_err_t err = board_sd_load_frame(path);
    if (err != ESP_OK) return err;
    if (!present_back(path)) return ESP_FAIL;
    const int index = board_sd_frame_index(request.sd_name);
    if (index >= 0) {
        s_frame_index = index;
    }
    return ESP_OK;
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
    lumenfolio::board_sleep::enter(lumenfolio::board_sleep::kTimerWakeSeconds);
}

/**
 * @brief 电源守护任务：决定什么时候睡。
 *
 * 单独一个任务，不挂在显示任务上——显示任务初始化失败退出、或者卡在刷屏里，
 * 设备照样要能睡着，否则电池会一直耗到没电。屏幕上有没有画面是另一回事。
 */
void power_guard_task(void *)
{
    for (;;) {
        vTaskDelay(pdMS_TO_TICKS(1000));

        if (s_sleep_requested) {
            ESP_LOGI(kTag, "收到睡眠请求");
            enter_deep_sleep();
        }
        // 带 VBUS 的线由 s_plugged 拦住。再看枚举，只为不供电的烧录线：它只有
        // D+/D-，TP4056 一直报未接充电。这是台面特例，一般不会遇到。
        if (s_sleep_hold || s_plugged || s_frame_transfer_busy || usb_serial_jtag_is_connected() ||
            s_display_state == display_state::busy) {
            continue;
        }
        if (esp_timer_get_time() - s_idle_since_us < static_cast<int64_t>(kIdleSleepMs) * 1000) {
            continue;
        }
        otaStatus_t status = {};
        otaUpdateGetStatus(&status);
        if (status.phase != otaIdle) {
            continue;  // 传固件时睡着会中断，留到下次空闲再睡
        }

        ESP_LOGI(kTag, "空闲 %u ms 且未接充电器，进入深度睡眠", (unsigned)kIdleSleepMs);
        enter_deep_sleep();
    }
}

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

// 投递入口 app_post_display() 在文件末尾实现，控制台和按键都走它。

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
 * @brief 调试串口控制台：按行收，回车执行。
 *
 * 命令语义全在 console.cpp，和 HTTP 的 /console 完全一致；这里只做收字节、
 * 回显、按行切分。串口是 USB-SERIAL-JTAG，和日志同一个口。
 */
void serial_cmd_task(void *)
{
    char line[64] = {};
    size_t length = 0;

    for (;;) {
        uint8_t ch = 0;
        if (usb_serial_jtag_read_bytes(&ch, 1, pdMS_TO_TICKS(500)) != 1) {
            continue;
        }

        if (ch == '\r' || ch == '\n') {
            if (length == 0) {
                continue;
            }
            line[length] = '\0';
            char out[512] = {};
            console_execute(line, out, sizeof(out));
            // 回执自带换行，去掉行尾再交给日志，免得每次多一个空行。
            for (size_t i = strlen(out); i > 0 && out[i - 1] == '\n'; --i) {
                out[i - 1] = '\0';
            }
            ESP_LOGI(kTag, "%s", out);
            length = 0;
            continue;
        }

        if (ch == 0x08 || ch == 0x7f) {  // 退格
            if (length > 0) {
                --length;
            }
            continue;
        }

        if (ch >= 0x20 && ch < 0x7f && length + 1 < sizeof(line)) {
            line[length++] = static_cast<char>(ch);
            usb_serial_jtag_write_bytes(&ch, 1, 0);  // 回显，不然串口上看不到自己敲的字
        }
    }
}

/**
 * @brief HTTP 控制台：GET /console?cmd=<命令>。
 *
 * 和串口共用 console_execute()，回执文本也完全一样。
 */
esp_err_t handle_http_console(httpd_req_t *request)
{
    char query[160] = {};
    char cmd[96] = {};

    httpd_resp_set_type(request, "text/plain");
    if (httpd_req_get_url_query_str(request, query, sizeof(query)) != ESP_OK ||
        httpd_query_key_value(query, "cmd", cmd, sizeof(cmd)) != ESP_OK) {
        return httpd_resp_sendstr(request, "用法：/console?cmd=<命令>，命令表见 cmd=help\n");
    }

    char out[512] = {};
    console_execute(cmd, out, sizeof(out));
    return httpd_resp_sendstr(request, out);
}

// 路由结构必须活到 HTTP 服务结束，所以放静态存储，启动时填一次。
httpd_uri_t s_uri_console;

void register_console_route()
{
    s_uri_console = {};
    s_uri_console.uri = "/console";
    s_uri_console.method = HTTP_GET;
    s_uri_console.handler = handle_http_console;

    const esp_err_t err = otaServiceRegisterHandler(&s_uri_console);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "控制台接口注册失败：%s", esp_err_to_name(err));
        return;
    }
    ESP_LOGI(kTag, "控制台：串口按行输入，HTTP 用 GET /console?cmd=<命令>");
}

/**
 * @brief POST /frame：按声明长度保存 EPD1／EPD2，再由显示任务解码并刷屏。
 *
 * 帧头可跨任意多次 recv，验证尺寸和 Content-Length 后才擦槽。载荷边收边写，
 * 元数据落盘后单独写魔数，避免未完成的上传被误认成有效帧。Flash 写入期间
 * 接收路径不访问 PSRAM；解压长度、完整流结束和 Adler32 由随后的显示任务验证。
 */
esp_err_t handle_frame_upload(httpd_req_t *request)
{
    if (httpd_req_get_hdr_value_len(request, "Transfer-Encoding") || !request->content_len) {
        httpd_resp_set_status(request, "411 Length Required");
        return httpd_resp_sendstr(request, "需要固定的 Content-Length，不接受分块传输\n");
    }
    if (request->content_len < EPD_FRAME_ZLIB_HEADER_BYTES ||
        request->content_len > ARTIFACTS_FRAME_BYTES) {
        httpd_resp_set_status(request, "413 Content Too Large");
        return httpd_resp_sendstr(request, "帧长度必须在 16 到 960012 字节之间\n");
    }

    char type[48] = {};
    if (httpd_req_get_hdr_value_str(request, "Content-Type", type, sizeof(type)) != ESP_OK ||
        strcmp(type, "application/octet-stream") != 0) {
        httpd_resp_set_status(request, "415 Unsupported Media Type");
        return httpd_resp_sendstr(request, "需要 application/octet-stream\n");
    }

    app_frame_transfer_begin();
    esp_err_t result = ESP_OK;
    uint8_t header[EPD_FRAME_ZLIB_HEADER_BYTES];
    size_t got = 0;
    while (result == ESP_OK && got < sizeof(header)) {
        const int received = httpd_req_recv(request, reinterpret_cast<char *>(header + got),
                                            sizeof(header) - got);
        if (received == HTTPD_SOCK_ERR_TIMEOUT) {
            continue;
        }
        if (received <= 0) {
            result = ESP_FAIL;
            break;
        }
        got += static_cast<size_t>(received);
    }
    epd_frame_info_t info = {};
    if (result == ESP_OK) {
        result = epd_frame_parse_header(header, sizeof(header), &info);
        if (result == ESP_OK && info.frame_bytes != request->content_len) {
            result = ESP_ERR_INVALID_SIZE;
        }
    }
    bool writing = false;
    if (result == ESP_OK) {
        writing = true;
        result = artifacts_inbox_begin();
    }
    uint8_t chunk[4096];
    while (result == ESP_OK && got < info.frame_bytes) {
        const size_t ask = info.frame_bytes - got < sizeof(chunk)
                               ? info.frame_bytes - got : sizeof(chunk);
        const int received = httpd_req_recv(request, reinterpret_cast<char *>(chunk), ask);
        if (received == HTTPD_SOCK_ERR_TIMEOUT) {
            continue;
        }
        if (received <= 0) {
            result = ESP_FAIL;
            break;
        }
        result = artifacts_inbox_write(got, chunk, static_cast<size_t>(received));
        got += static_cast<size_t>(received);
    }
    if (result == ESP_OK) {
        // EPD1 的前 16 字节还含 4 字节像素，也必须先于魔数落盘。
        result = artifacts_inbox_write(4, header + 4, sizeof(header) - 4);
    }
    if (result == ESP_OK) {
        result = artifacts_inbox_write(0, header, 4);
    }
    if (result != ESP_OK && writing) {
        artifacts_inbox_discard();
    }
    app_frame_transfer_end();

    if (result != ESP_OK) {
        httpd_resp_set_status(request, result == ESP_ERR_NOT_FOUND || result == ESP_ERR_INVALID_SIZE
                                           ? "422 Unprocessable Content"
                                           : "500 Internal Server Error");
        httpd_resp_set_type(request, "text/plain; charset=utf-8");
        return httpd_resp_sendstr(request, "画面没有写入\n");
    }
    if (app_post_display(display_cmd::inbox) != ESP_OK) {
        httpd_resp_set_status(request, "202 Accepted");
        return httpd_resp_sendstr(request, "已写入，屏幕还没排上刷新\n");
    }
    httpd_resp_set_status(request, "202 Accepted");
    return httpd_resp_sendstr(request, "已写入，正在刷上屏\n");
}

httpd_uri_t s_uri_frame;

void register_frame_route()
{
    s_uri_frame = {};
    s_uri_frame.uri = "/frame";
    s_uri_frame.method = HTTP_POST;
    s_uri_frame.handler = handle_frame_upload;

    const esp_err_t err = otaServiceRegisterHandler(&s_uri_frame);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "传图接口注册失败：%s", esp_err_to_name(err));
        return;
    }
    ESP_LOGI(kTag, "传图：POST /frame，EPD1／EPD2 无损帧");
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
        s_display_state = display_state::dead;
        s_last_result = "epd-init-failed";
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
        s_display_state = display_state::dead;
        s_last_result = "no-fb";
        vTaskDelete(nullptr);  // 没有帧缓冲，翻页也无从谈起
        return;
    }

    s_idle_since_us = esp_timer_get_time();
    s_display_state = display_state::idle;

    // 按键队列可能为 NULL（按键初始化失败），照样要能刷屏，所以先取出来判空。
    QueueHandle_t keys = board_keys_queue();
    display_request current_pick = {};

    for (;;) {
        // 不建 queue set：两个队列各自取，省掉一种能把显示任务弄死的失败路径。
        // 刷新期间积压的事件在这里一次性取完，合并成一个净步数，只刷一次。
        int step = 0;
        bool force = false;
        bool factory = false;
        bool inbox = false;
        bool woke = false;

        board_key_event_t key = {};
        while (keys != nullptr && xQueueReceive(keys, &key, 0) == pdTRUE) {
            woke = true;
            if (key.key == BOARD_KEY_PREV) {
                --step;
            } else if (key.key == BOARD_KEY_NEXT) {
                ++step;
            } else {
                force = true;  // KEY3 = 原地重刷
            }
        }

        display_request request = {};
        display_request picked = {};
        if (s_cmds == nullptr) {
            vTaskDelay(pdMS_TO_TICKS(20));
            continue;
        }
        while (xQueueReceive(s_cmds, &request, pdMS_TO_TICKS(20)) == pdTRUE) {
            woke = true;
            if (request.cmd == display_cmd::next) {
                ++step;
            } else if (request.cmd == display_cmd::prev) {
                --step;
            } else if (request.cmd == display_cmd::factory) {
                factory = true;
                picked = {};
            } else if (request.cmd == display_cmd::inbox) {
                inbox = true;
                picked = {};
            } else if (request.cmd == display_cmd::refresh) {
                force = true;
            } else {
                picked = request;
                inbox = factory = false;
            }
        }

        if (!woke) {
            continue;
        }
        s_idle_since_us = esp_timer_get_time();

        const int count = board_sd_frame_count();
        s_frame_count = count;
        s_display_state = display_state::busy;

        // 同一批选图只刷最后一张，原地重刷仍读取选中的来源。
        if (picked.cmd != display_cmd::refresh ||
            (!inbox && !factory && step == 0 && current_pick.cmd != display_cmd::refresh)) {
            const auto &target = picked.cmd != display_cmd::refresh ? picked : current_pick;
            const esp_err_t err = show_picked_frame(target);
            if (picked.cmd == display_cmd::refresh && err == ESP_ERR_NOT_FOUND) {
                // 原件已被删除，由本次主动刷新重新走默认来源。
                current_pick = {};
            } else {
                if (err == ESP_OK) {
                    current_pick = target;
                    s_last_result = "ok";
                } else {
                    ESP_LOGW(kTag, "选中的画面载入或上屏失败");
                    s_last_result = "load-failed";
                    led_event_post(LED_EV_ANOMALY, 0);
                }
                s_display_state = display_state::idle;
                continue;
            }
        }
        if (inbox) {
            // 上传槽里的图优先于同一批里的翻页：图刚写进去，这一轮就该把它刷上屏。
            if (artifacts_draw_inbox_frame() == ESP_OK) {
                const bool shown = present_back("上传的画面");
                s_last_result = shown ? "inbox" : "inbox-failed";
                if (shown) current_pick = {};
            } else {
                ESP_LOGW(kTag, "上传槽里没有可用画面");
                s_last_result = "inbox-empty";
                led_event_post(LED_EV_ANOMALY, 0);
            }
        } else if (factory) {
            // 出厂画面固定来自 artifacts，不管卡上有没有帧文件。
            if (artifacts_draw_boot_frame() == ESP_OK) {
                const bool shown = present_back("出厂画面");
                s_last_result = shown ? "factory" : "factory-failed";
                if (shown) current_pick = {};
            } else {
                ESP_LOGW(kTag, "artifacts 里没有出厂画面，改刷白屏");
                epd_fb_clear(WHITE);
                const bool shown = present_back("白屏");
                s_last_result = shown ? "factory-white" : "factory-failed";
                if (shown) current_pick = {};
            }
        } else if (count > 0) {
            s_frame_index = ((s_frame_index + step) % count + count) % count;
            const bool shown = show_card_frame(s_frame_index, count);
            s_last_result = shown ? "ok" : "load-failed";
            if (shown) {
                current_pick = {};
            } else {
                ESP_LOGE(kTag, "第 %d/%d 张载入或上屏失败", s_frame_index + 1, count);
                led_event_post(LED_EV_ANOMALY, 0);
            }
        } else if (step != 0 && !force) {
            ESP_LOGW(kTag, "卡上没有可用图片，不翻页");
            s_last_result = "no-image";
            led_event_post(LED_EV_NO_IMAGE, 0);
        } else {
            if (board_sd_mounted()) {
                ESP_LOGI(kTag, "TF 卡上没有可用帧文件，改用 artifacts 里的画面");
            }
            const bool shown = refresh_fallback();
            s_last_result = shown ? "fallback" : "fallback-failed";
            if (shown) current_pick = {};
        }
        s_display_state = display_state::idle;
    }
}

}  // namespace

esp_err_t app_post_display(display_cmd cmd, const char *sd_name)
{
    if (s_cmds == nullptr || s_display_state == display_state::dead) {
        return ESP_ERR_INVALID_STATE;
    }
    display_request request = {cmd, {}};
    if (cmd == display_cmd::pick_sd) {
        if (sd_name == nullptr || strlen(sd_name) >= sizeof(request.sd_name)) {
            return ESP_ERR_INVALID_ARG;
        }
        strcpy(request.sd_name, sd_name);
    }
    return xQueueSend(s_cmds, &request, 0) == pdTRUE ? ESP_OK : ESP_ERR_NO_MEM;
}

void app_request_sleep()
{
    s_sleep_requested = true;
}

void app_hold_awake()
{
    s_sleep_hold = true;
}

void app_frame_transfer_begin()
{
    s_frame_transfer_busy = true;
}

void app_frame_transfer_end()
{
    s_idle_since_us = esp_timer_get_time();
    s_frame_transfer_busy = false;
}

void app_describe(char *buf, size_t size)
{
    if (buf == nullptr || size == 0) {
        return;
    }
    size_t used = 0;
    buf[0] = '\0';

    const char *wake = "cold";
    if (s_wake.timer) {
        wake = "timer";
    } else if (s_wake.key) {
        wake = s_wake.which == BOARD_KEY_PREV ? "key1"
             : s_wake.which == BOARD_KEY_NEXT ? "key2" : "key3";
    }

    const int64_t idle_ms = (esp_timer_get_time() - s_idle_since_us) / 1000;
    const bool usb = usb_serial_jtag_is_connected();
    const char *sleep_state = s_sleep_hold ? "hold"
                            : (s_plugged || usb) ? "off-powered"
                            : (idle_ms < static_cast<int64_t>(kIdleSleepMs) ? "waiting" : "due");

    append(buf, size, &used,
           "wake=%s display=%s last=%s frames=%d idx=%d plugged=%d usb=%d idle=%lldms sleep=%s\n",
           wake, display_state_name(), s_last_result, s_frame_count, s_frame_index,
           s_plugged ? 1 : 0, usb ? 1 : 0, static_cast<long long>(idle_ms), sleep_state);

    board_power_sample_t power = {};
    if (deviceStatusPower(&power)) {
        append(buf, size, &used, "battery=%dmV %u%% %s\n", power.millivolts,
               static_cast<unsigned>(power.percent), charge_name(power.charge));
    }
    shtc3_sample_t env = {};
    if (deviceStatusEnv(&env)) {
        append(buf, size, &used, "env=%.1fC %.1f%%RH\n", env.temperature_c, env.humidity_percent);
    }
    append(buf, size, &used, "sd=%s\n", board_sd_mounted() ? "mounted" : "none");
    append(buf, size, &used, "heap=%u/%u psram=%u\n",
           static_cast<unsigned>(heap_caps_get_free_size(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT)),
           static_cast<unsigned>(heap_caps_get_minimum_free_size(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT)),
           static_cast<unsigned>(heap_caps_get_free_size(MALLOC_CAP_SPIRAM)));
}

extern "C" void app_main(void)
{
    // 深睡会锁住一部分脚的保持位，开机第一件事先解开，否则 LED / ADC_CTL 可能一直是死的。
    lumenfolio::board_sleep::release_holds();

    board_gpio_init();
    s_idle_since_us = esp_timer_get_time();

    s_wake = lumenfolio::board_sleep::wakeup();
    bool refresh_on_start = false;
    if (s_wake.timer) {
        // 定时唤醒只做两件事：看是不是插上了充电器；到点就补刷一张防残影。
        board_power_sample_t sample = {};
        const bool sampled = board_power_init() == ESP_OK && board_power_read(&sample) == ESP_OK;
        const bool plugged = sampled && (sample.charge == BOARD_CHARGE_CHARGING ||
                                         sample.charge == BOARD_CHARGE_FULL);
        if (plugged) {
            ESP_LOGI(kTag, "定时唤醒：检测到充电器，按正常开机处理");
        } else if (!lumenfolio::board_sleep::take_refresh_slot()) {
            ESP_LOGI(kTag, "定时唤醒：没有接充电器，继续睡");
            lumenfolio::board_sleep::enter(lumenfolio::board_sleep::kTimerWakeSeconds);
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
        register_console_route();
        register_frame_route();
        const esp_err_t browse = app_register_frame_browse();
        if (browse != ESP_OK) {
            ESP_LOGE(kTag, "画面浏览接口注册失败：%s", esp_err_to_name(browse));
        }
    } else {
        ESP_LOGE(kTag, "Wi-Fi 未启动：%s，本次开机无法 OTA", esp_err_to_name(wifi));
    }

    ESP_LOGI(kTag, "board ready");
    ESP_LOGI(kTag, "epd   sclk=%d mosi=%d miso=%d cs0=%d cs1=%d dc=%d rst=%d busy=%d",
             lumenfolio::board::kEpdSclk, lumenfolio::board::kEpdMosi, lumenfolio::board::kEpdMiso,
             lumenfolio::board::kEpdCs0, lumenfolio::board::kEpdCs1, lumenfolio::board::kEpdDc,
             lumenfolio::board::kEpdRst, lumenfolio::board::kEpdBusy);
    ESP_LOGI(kTag, "sd    clk=%d cmd=%d d0=%d d1=%d d2=%d d3=%d cd=%d",
             lumenfolio::board::kSdClk, lumenfolio::board::kSdCmd, lumenfolio::board::kSdD0,
             lumenfolio::board::kSdD1, lumenfolio::board::kSdD2, lumenfolio::board::kSdD3,
             lumenfolio::board::kSdCardDetect);
    ESP_LOGI(kTag, "keys  %d %d %d | led=%d | i2c sda=%d scl=%d",
             lumenfolio::board::kKey1, lumenfolio::board::kKey2, lumenfolio::board::kKey3,
             lumenfolio::board::kLedWs2812, lumenfolio::board::kI2cSda, lumenfolio::board::kI2cScl);
    ESP_LOGI(kTag, "pwr   en=%d adc=%d ctl=%d chg=%d full=%d",
             lumenfolio::board::kPeriphPowerEn, lumenfolio::board::kBatteryAdc, lumenfolio::board::kBatteryAdcCtl,
             lumenfolio::board::kPowerChgStat, lumenfolio::board::kPowerFullStat);

    // 刷屏命令共用一条队列：按键、串口控制台、HTTP 控制台。
    s_cmds = xQueueCreate(8, sizeof(display_request));
    if (s_cmds == nullptr) {
        ESP_LOGE(kTag, "命令队列创建失败");
    }

    // 唤醒的那一下按键本身就是一次操作，补投成命令，否则用户得按第二次才看得到画面。
    if (s_wake.key) {
        ESP_LOGI(kTag, "按键唤醒，按 KEY%d 处理", (int)s_wake.which + 1);
        app_post_display(cmd_for_key(s_wake.which));
    } else if (refresh_on_start) {
        app_post_display(display_cmd::refresh);
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
        s_display_state = display_state::dead;
        ESP_LOGE(kTag, "显示任务创建失败");
    }

    // 睡眠由独立任务看管：显示任务挂了也不能让设备一直醒着耗电。
    if (xTaskCreate(power_guard_task, "power_guard", 3072, nullptr, 4, nullptr) != pdPASS) {
        ESP_LOGE(kTag, "电源守护任务创建失败");
    }

    // 放在最后：未调用则新镜像 60 s 后被回滚。
    otaServiceReady();
}
