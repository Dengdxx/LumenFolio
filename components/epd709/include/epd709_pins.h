#pragma once

#include "driver/gpio.h"
#include "hal/spi_types.h"

/*
 * 7.09" Spectra 6 1200x1600 EPD（EPD0709A02：NT61522 ×2 + EK73601 ×1）。
 *
 * 引脚来源：本项目原理图 Board1「墨水屏相框」P1（2026-10-31），已改为本板走线，
 * 与 OSPTEK 示例板（RST47/BUSY38/DC39/CS40,41/SCK42/SI0 12）不同，不要混用。
 *
 *   原理图网络名        →  宏              GPIO
 *   EPD_CLK            →  PIN_EPD_SCK      12
 *   EPD_MOSI (SI0)     →  PIN_EPD_D0       11
 *   EPD_MISO (SI1)     →  PIN_EPD_D1       13   仅读回用（0xF2/TSC），4 线写不依赖
 *   EPD_CS0 (CSB_M)    →  PIN_EPD_CS0      10   主 IC
 *   EPD_CS1 (CSB_S)    →  PIN_EPD_CS1       9   从 IC
 *   EPD_DC             →  PIN_EPD_DC       47
 *   EPD_RESET          →  PIN_EPD_RST      48
 *   EPD_BUSY           →  PIN_EPD_BUSY     21   高 = 空闲
 *
 * 本板未引出 SI2 / SI3，也没有 LOAD_SW；面板 BS0/BS1 由硬件固定为 4 线 SPI，
 * 需要 D/C，数据只走 SI0。
 */

#define EPD_SPI_HOST            SPI2_HOST
#define EPD_SPI_FREQ_HZ         (5 * 1000 * 1000)

#define PIN_EPD_RST             (GPIO_NUM_48)
#define PIN_EPD_BUSY            (GPIO_NUM_21)
#define PIN_EPD_DC              (GPIO_NUM_47)
#define PIN_EPD_CS0             (GPIO_NUM_10)
#define PIN_EPD_CS1             (GPIO_NUM_9)
#define PIN_EPD_SCK             (GPIO_NUM_12)
#define PIN_EPD_D0              (GPIO_NUM_11)
#define PIN_EPD_D1              (GPIO_NUM_13)
#define PIN_EPD_D2              (GPIO_NUM_NC)
#define PIN_EPD_D3              (GPIO_NUM_NC)

#define PIN_EPD_LOAD_SW         (GPIO_NUM_NC)

#define EPD_CS_MAIN             0
#define EPD_CS_SUB              1

/* 只对真实存在的脚生成位掩码，NC(-1) 不参与 GPIO 配置 */
#define EPD_PIN_MASK(pin)       (((pin) < 0) ? 0ULL : (1ULL << (pin)))
