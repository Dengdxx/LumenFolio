#pragma once

#include <stdbool.h>

#include "driver/i2c_master.h"
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @file shtc3.h
 * @brief 温湿度传感器驱动。板上 U3 是 GXHTC3，命令集与 Sensirion SHTC3 兼容。
 *
 * 传感器接 3V3_MCU 常电，上电即处于休眠态；休眠时不应答地址，只有唤醒命令
 * 会被接受。每次测量前唤醒、测完立即休眠，才能把平均电流压到微安级。
 *
 * 所有函数都不线程安全：同一个总线句柄和内部状态由调用方串行使用。
 */

/** @brief 7 位 I2C 地址。A0–A2 无引出，固定 0x70。 */
#define SHTC3_I2C_ADDR 0x70

/** @brief 一次测量结果。 */
typedef struct {
    float temperature_c;    /**< 摄氏温度，-45 … 130。 */
    float humidity_percent; /**< 相对湿度，0 … 100。 */
} shtc3_sample_t;

/**
 * @brief 在给定总线上挂载传感器并把状态归位到休眠。
 *
 * @param bus 已创建的 I2C 主机总线，所有权限归调用方。
 * @return ESP_OK 成功；ESP_ERR_INVALID_ARG 总线为空；
 *         ESP_ERR_NOT_FOUND 地址无人应答（未贴片或总线不通）；
 *         其余为底层 I2C 错误。
 *
 * @note 重复调用返回 ESP_OK。传感器事先是否处于休眠都能接上：唤醒命令被 NACK
 *       只说明它已经醒着，不视为故障。
 */
esp_err_t shtc3_init(i2c_master_bus_handle_t bus);

/** @brief 是否已经成功初始化。 */
bool shtc3_ready(void);

/**
 * @brief 唤醒、测一次温湿度、再休眠。
 *
 * @param out 结果写入处，不能为 NULL。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 未初始化或 out 为空；
 *         ESP_ERR_INVALID_CRC 数据校验失败；其余为底层 I2C 错误。
 *
 * @note 阻塞约 20 ms，其中绝大部分是传感器自身的转换时间。
 * @note 无论成功与否都尝试让它回到休眠，失败的那次不会把传感器留在唤醒态耗电。
 */
esp_err_t shtc3_read(shtc3_sample_t *out);

#ifdef __cplusplus
}
#endif
