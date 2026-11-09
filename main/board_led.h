#pragma once

#include <stdint.h>

#include "esp_err.h"

/**
 * @file board_led.h
 * @brief 单颗 WS2812B 的颜色出口。灯语由 led_fsm 独占，业务不要直接调用。
 */

/**
 * @brief 占用 GPIO14 并建立 RMT 通道。
 * @return 重复调用也返回 ESP_OK。失败时可能已占住部分资源，不要重试。
 * @note 非线程安全，只在启动阶段调用。成功后通道不释放。
 */
esp_err_t board_led_init(void);

/**
 * @brief 发送一帧颜色，并等到发完。
 * @param red 红，0–255。线上顺序是 G、R、B，由本函数重排。
 * @param green 绿，0–255。
 * @param blue 蓝，0–255。
 * @return 未初始化返回 ESP_ERR_INVALID_STATE。
 * @note 阻塞最多约 100 ms，不可重入。通道队列深度为 1。
 */
esp_err_t board_led_set(uint8_t red, uint8_t green, uint8_t blue);
