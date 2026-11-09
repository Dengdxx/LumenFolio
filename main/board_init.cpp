#include "board_init.h"

#include "board_pins.h"
#include "driver/gpio.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

namespace {

void configure_output(gpio_num_t pin, uint32_t level)
{
    gpio_config_t cfg = {};
    cfg.pin_bit_mask = 1ULL << pin;
    cfg.mode         = GPIO_MODE_OUTPUT;
    cfg.pull_up_en   = GPIO_PULLUP_DISABLE;
    cfg.pull_down_en = GPIO_PULLDOWN_DISABLE;
    cfg.intr_type    = GPIO_INTR_DISABLE;
    gpio_config(&cfg);
    gpio_set_level(pin, level);
}

void configure_input(gpio_num_t pin, bool internal_pullup)
{
    gpio_config_t cfg = {};
    cfg.pin_bit_mask  = 1ULL << pin;
    cfg.mode          = GPIO_MODE_INPUT;
    cfg.pull_up_en    = internal_pullup ? GPIO_PULLUP_ENABLE : GPIO_PULLUP_DISABLE;
    cfg.pull_down_en  = GPIO_PULLDOWN_DISABLE;
    cfg.intr_type     = GPIO_INTR_DISABLE;
    gpio_config(&cfg);
}

}  // namespace

void board_gpio_init()
{
    // 3V3_EXT 默认关断：只有真的要刷屏时才打开，定时唤醒的空转不碰这条轨。
    configure_output(framefolio::board::kPeriphPowerEn, 0);

    // 三按键：外部 10kΩ 上拉已就位，内部上拉不再开。
    configure_input(framefolio::board::kKey1, false);
    configure_input(framefolio::board::kKey2, false);
    configure_input(framefolio::board::kKey3, false);

    // 充电状态：TP4056 开漏 + 外部 10kΩ 上拉，低有效。
    configure_input(framefolio::board::kPowerChgStat, false);
    configure_input(framefolio::board::kPowerFullStat, false);

    // 卡座检测：插卡接地，内部上拉省掉外部电阻的不确定性。
    configure_input(framefolio::board::kSdCardDetect, true);

    // 电池分压默认断开，测量前拉高、测完拉低，避免漏电。
    configure_output(framefolio::board::kBatteryAdcCtl, 0);

    // GPIO14 交给 board_led_init() 的 RMT 通道，这里不配。
}

void board_power_rail_on()
{
    gpio_set_level(framefolio::board::kPeriphPowerEn, 1);
    // 等 SY8089 软启动完成，再去拉 EPD 的 RST，否则 DDIC 可能没起来。
    vTaskDelay(pdMS_TO_TICKS(50));
}
