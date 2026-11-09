#pragma once

#include "driver/i2c_master.h"
#include "esp_err.h"

/**
 * @file board_i2c.h
 * @brief 板上 I2C 主机总线：SCL = GPIO41、SDA = GPIO42。
 *
 * 总线上挂的是 3V3_MCU 常电的 GXHTC3（0x70）。两线各有 10k 外部上拉，
 * 所以不开内部上拉。原理图上 U2 那颗 NCT75MNR2G 的网络标为 T_SDA/T_SCL，
 * 与这两个网络不同名，并未接到 MCU，扫描时不要期待 0x48。
 *
 * 只在启动阶段初始化一次，句柄在整个运行期有效。
 */
esp_err_t board_i2c_init();

/** @brief 已建好的总线句柄；未初始化时为 nullptr。 */
i2c_master_bus_handle_t board_i2c_bus();
