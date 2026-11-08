#include "board_sleep.h"

#include "board_pins.h"
#include "driver/gpio.h"
#include "esp_attr.h"
#include "esp_log.h"
#include "esp_sleep.h"

namespace {

constexpr const char *kTag = "sleep";

// 三个按键都在 RTC IO 范围内（0–21），才拿得到 EXT1 唤醒这一路。
// 充电状态脚 GPIO38/39 不在这个范围，插 USB 唤不醒，这是硬件定死的。
constexpr gpio_num_t kWakeKeys[] = {lumenfolio::board::kKey1, lumenfolio::board::kKey2, lumenfolio::board::kKey3};

// 深睡不清 RTC 内存，这个计数要跨睡眠保留。
RTC_DATA_ATTR uint32_t s_timer_wakes;

}  // namespace

lumenfolio::board_sleep::wake_info lumenfolio::board_sleep::wakeup()
{
    wake_info info = {};

    // 多路唤醒可能同时置位，所以取位图而不是单个原因。
    const uint32_t causes = esp_sleep_get_wakeup_causes();
    if ((causes & (1U << ESP_SLEEP_WAKEUP_TIMER)) != 0) {
        info.timer = true;
        return info;
    }

    // 冷启动或按键唤醒都说明画面刚被彻底换过，重新计 24 h。
    s_timer_wakes = 0;

    if ((causes & (1U << ESP_SLEEP_WAKEUP_EXT1)) == 0) {
        return info;
    }

    const uint64_t mask = esp_sleep_get_ext1_wakeup_status();
    if ((mask & (1ULL << lumenfolio::board::kKey1)) != 0) {
        info.key = true;
        info.which = BOARD_KEY_PREV;
    } else if ((mask & (1ULL << lumenfolio::board::kKey2)) != 0) {
        info.key = true;
        info.which = BOARD_KEY_NEXT;
    } else if ((mask & (1ULL << lumenfolio::board::kKey3)) != 0) {
        info.key = true;
        info.which = BOARD_KEY_AUX;
    }
    return info;
}

bool lumenfolio::board_sleep::take_refresh_slot()
{
    ++s_timer_wakes;
    if (s_timer_wakes < kWakesPerRefresh) {
        return false;
    }
    s_timer_wakes = 0;
    return true;
}

void lumenfolio::board_sleep::enter(uint32_t timer_seconds)
{
    // SY8089 的 EN 上没有下拉电阻。深睡后 GPIO40 变成高阻，EN 会浮空，
    // 3V3_EXT 有可能自己又起来，面板就白吃电——必须把低电平锁住。
    gpio_set_level(lumenfolio::board::kPeriphPowerEn, 0);
    gpio_hold_en(lumenfolio::board::kPeriphPowerEn);

    // 分压开关同理：Q7 栅极浮空可能导通，220k/110k 支路会一直漏电。
    gpio_set_level(lumenfolio::board::kBatteryAdcCtl, 0);
    gpio_hold_en(lumenfolio::board::kBatteryAdcCtl);

    // WS2812 是单线锁存器件，数据脚浮空时容易被干扰点亮；压到低电平再保持。
    gpio_set_direction(lumenfolio::board::kLedWs2812, GPIO_MODE_OUTPUT);
    gpio_set_level(lumenfolio::board::kLedWs2812, 0);
    gpio_hold_en(lumenfolio::board::kLedWs2812);

    gpio_deep_sleep_hold_en();

    uint64_t mask = 0;
    for (gpio_num_t key : kWakeKeys) {
        if (gpio_get_level(key) != 0) {
            mask |= 1ULL << key;
        } else {
            ESP_LOGW(kTag, "GPIO%d 当前是按下状态，本次不作为唤醒源", static_cast<int>(key));
        }
    }
    if (mask != 0) {
        ESP_ERROR_CHECK(esp_sleep_enable_ext1_wakeup_io(mask, ESP_EXT1_WAKEUP_ANY_LOW));
    } else {
        ESP_LOGW(kTag, "没有可用的按键唤醒源，只能等定时器");
    }

    if (timer_seconds > 0) {
        ESP_ERROR_CHECK(esp_sleep_enable_timer_wakeup(
            static_cast<uint64_t>(timer_seconds) * 1000ULL * 1000ULL));
    }

    ESP_LOGI(kTag, "进入深度睡眠：按键 mask=0x%llx，定时 %u s",
             static_cast<unsigned long long>(mask), static_cast<unsigned>(timer_seconds));

    esp_deep_sleep_start();
}

void lumenfolio::board_sleep::release_holds()
{
    // 顺序无所谓：先把深睡的全局保持关掉，再逐个解开。
    gpio_deep_sleep_hold_dis();
    gpio_hold_dis(lumenfolio::board::kPeriphPowerEn);
    gpio_hold_dis(lumenfolio::board::kBatteryAdcCtl);
    gpio_hold_dis(lumenfolio::board::kLedWs2812);
}
