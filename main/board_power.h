#pragma once

#include <stdint.h>

#include "esp_err.h"

/**
 * @file board_power.h
 * @brief 电池电压与充电状态。
 *
 * 电压从 ADC1_CH0(GPIO1) 读：VBAT 先经 Q7/Q8 开关，再由 220k/110k 分压，
 * 所以采样前必须拉高 ADC_CTL(GPIO2)，采完立刻拉低断开分压，否则这条支路常态漏电。
 * 分压比 3:1，满量程对应 VBAT 约 9 V，单节锂电（3.0–4.2 V）落在量程前段。
 *
 * 充电状态读 TP4056 的开漏输出，外部 10k 上拉、低有效：
 * CHG# 为低是正在充电，STDBY# 为低是已充满。两者同时为高表示没有充电输入。
 */

/** @brief 充电状态。 */
typedef enum {
    BOARD_CHARGE_UNKNOWN = 0,  /**< 尚未采样。 */
    BOARD_CHARGE_CHARGING,     /**< CHG# 为低：正在充电。 */
    BOARD_CHARGE_FULL,         /**< STDBY# 为低：充电完成。 */
    BOARD_CHARGE_IDLE,         /**< 两路都为高：没有充电输入，或在无电池状态下浮动。 */
} board_charge_t;

/** @brief 一次采样结果。 */
typedef struct {
    int millivolts;      /**< VBAT，已按分压比还原。 */
    uint8_t percent;     /**< 按开路电压估算的电量，0–100。 */
    board_charge_t charge;
} board_power_sample_t;

/**
 * @brief 初始化 ADC 单元与校准。
 *
 * @return ESP_OK 成功；重复调用返回 ESP_OK；ADC 单元或通道配置失败返回底层错误码。
 * @note 校准不可用时仍返回 ESP_OK，只是改用满量程折算，电压会偏一点。
 * @note ADC_CTL 的 GPIO 方向由 board_gpio_init() 配好，这里只负责拉高拉低。
 */
esp_err_t board_power_init();

/**
 * @brief 接通分压、采一组样本、断开分压，并读两路充电状态。
 *
 * @param[out] out 结果写入处。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 未初始化或 out 为空；其余为 ADC 错误。
 *
 * @note 阻塞约 15 ms，其中 10 ms 是分压节点的 RC 稳定时间。
 * @note 电量是开路电压的估算值：充电时端电压被抬高，读数会偏高，看趋势而不是绝对值。
 * @note 非线程安全，只在同一个任务里调用。
 */
esp_err_t board_power_read(board_power_sample_t *out);
