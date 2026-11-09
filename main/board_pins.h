#pragma once

#include "driver/gpio.h"
#include "epd709_pins.h"
#include "hal/adc_types.h"

/**
 * @file board_pins.h
 * @brief 板级引脚唯一入口：原理图 Board1「墨水屏相框」P1（创建 2026-10-27 / 更新 2026-10-31），U1 = ESP32-S3。
 *
 * 只描述电气连接与有效电平，不含驱动逻辑。驱动不得另行写死 GPIO 编号。
 * @note 改引脚必须先改原理图，再改这里。
 */
namespace framefolio::board {

// ───────────── 墨水屏 EPD：SPI（CS0/CS1 主从片选） ─────────────
// 引脚真值在驱动组件的 epd709_pins.h 里（驱动内部也直接用它），这里只做别名，避免两处各自维护。
inline constexpr gpio_num_t kEpdSclk = PIN_EPD_SCK;  // EPD_CLK   FSPICLK
inline constexpr gpio_num_t kEpdMosi = PIN_EPD_D0;   // EPD_MOSI  SI0
inline constexpr gpio_num_t kEpdMiso = PIN_EPD_D1;   // EPD_MISO  SI1（仅读回）
inline constexpr gpio_num_t kEpdCs0  = PIN_EPD_CS0;  // EPD_CS0   CSB_M（主）
inline constexpr gpio_num_t kEpdCs1  = PIN_EPD_CS1;  // EPD_CS1   CSB_S（从）
inline constexpr gpio_num_t kEpdDc   = PIN_EPD_DC;   // EPD_DC
inline constexpr gpio_num_t kEpdRst  = PIN_EPD_RST;  // EPD_RESET
/** @brief EPD BUSY。驱动按高电平空闲等待；原理图未标极性，上板若相反须同时改驱动。 */
inline constexpr gpio_num_t kEpdBusy = PIN_EPD_BUSY; // EPD_BUSY

// ───────────── TF 卡：SDMMC 4-bit + 卡座检测 ─────────────
inline constexpr gpio_num_t kSdClk = GPIO_NUM_7;   // SD_CLK
inline constexpr gpio_num_t kSdCmd = GPIO_NUM_15;  // SD_CMD
inline constexpr gpio_num_t kSdD0  = GPIO_NUM_6;   // SD_D0
inline constexpr gpio_num_t kSdD1  = GPIO_NUM_5;   // SD_D1
inline constexpr gpio_num_t kSdD2  = GPIO_NUM_17;  // SD_D2
inline constexpr gpio_num_t kSdD3  = GPIO_NUM_16;  // SD_D3
/** @brief 卡座检测，插卡接地。内部上拉由 board_gpio_init() 打开；触点抖动由调用方消抖。 */
inline constexpr gpio_num_t kSdCardDetect = GPIO_NUM_4;  // SD_CD

// ───────────── 三按键：10kΩ 上拉到 3V3_MCU，按下接地 ─────────────
inline constexpr gpio_num_t kKey1 = GPIO_NUM_3;   // KEY_1
inline constexpr gpio_num_t kKey2 = GPIO_NUM_8;   // KEY_2
inline constexpr gpio_num_t kKey3 = GPIO_NUM_18;  // KEY_3
inline constexpr uint32_t   kKeyActiveLevel = 0;  // 低有效（硬件上拉）

// ───────────── RGB LED：单颗 WS2812B-V6，VDD=3V3_MCU ─────────────
/** @brief WS2812B 数据线。时序须由 RMT 保证，GPIO 翻转或 PWM 会错色。 */
inline constexpr gpio_num_t kLedWs2812 = GPIO_NUM_14;  // RGB → LED1 DIN
inline constexpr int        kLedPixelCount = 1;

// ───────────── 电池采样：VBAT 经 Q8 开关后 220k/110k 分压 ─────────────
inline constexpr gpio_num_t    kBatteryAdc     = GPIO_NUM_1;           // ADC_BAT = ADC1_CH0
inline constexpr adc_channel_t kBatteryChannel = ADC_CHANNEL_0;
inline constexpr gpio_num_t    kBatteryAdcCtl  = GPIO_NUM_2;           // ADC_CTL：高 = 接通分压
/** @brief 分压比分子。Vbat = Vadc × Num / Den；不采样时 kBatteryAdcCtl 必须为低，否则支路漏电。 */
inline constexpr uint32_t      kBatteryDividerNum = 3;                 // (220k + 110k) / 110k
inline constexpr uint32_t      kBatteryDividerDen = 1;

// ───────────── 充电状态（TP4056，开漏 + 10kΩ 上拉到 3V3_MCU） ─────────────
inline constexpr gpio_num_t kPowerChgStat  = GPIO_NUM_39;  // POWER_CHG  ← CHRG#   低有效
inline constexpr gpio_num_t kPowerFullStat = GPIO_NUM_38;  // POWER_FULL ← STDBY#  低有效
/** @brief 两路充电状态共用开漏低有效。 */
inline constexpr uint32_t   kChargeActiveLevel = 0;
/** @brief 3V3_EXT 使能，高有效。EPD 的 VDD/VDDIO 在这条轨上，刷屏期间拉低会停在中途。 */
inline constexpr gpio_num_t kPeriphPowerEn = GPIO_NUM_40;  // POWER_CTL → SY8089 EN

// ───────────── 温湿度 SHTC3：I2C，地址 0x70 ─────────────
inline constexpr gpio_num_t kI2cSda = GPIO_NUM_42;  // TEMP_SDA
inline constexpr gpio_num_t kI2cScl = GPIO_NUM_41;  // TEMP_SCL

// ───────────── 固定脚（硅片决定，不可改） ─────────────
inline constexpr gpio_num_t kUsbDm  = GPIO_NUM_19;  // USB_D-
inline constexpr gpio_num_t kUsbDp  = GPIO_NUM_20;  // USB_D+
inline constexpr gpio_num_t kBoot   = GPIO_NUM_0;   // BOOT 键
// 调试串口 UART0：TXD0 = GPIO43，RXD0 = GPIO44（原理图第 36/37 脚）
// EN 脚接 RESET 键 + 100nF 复位 RC

// ───────────── 原理图上未连接的脚 ─────────────
// GPIO35 / GPIO36 / GPIO37 / GPIO45 / GPIO46 悬空（原理图打叉）

}  // namespace framefolio::board
