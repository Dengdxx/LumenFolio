#pragma once

#include <stddef.h>

#include "board_power.h"
#include "shtc3.h"

/**
 * @file device_status.h
 * @brief 采样任务与 HTTP 状态接口之间的只读快照。
 *
 * 采样任务把最近一次结果写进来，/ota/status 的附加字段从这里取。写入和读取
 * 都在临界区里完成一次结构体拷贝，HTTP 侧不会读到写了一半的样本。
 * 这里只存缓存，不做任何采样：HTTP 任务里开合分压支路或阻塞等 I2C 都不合适。
 */

/** @brief 记录最近一次电池采样。 */
void deviceStatusSetPower(const board_power_sample_t &sample);

/** @brief 记录最近一次温湿度采样。 */
void deviceStatusSetEnv(const shtc3_sample_t &sample);

/**
 * @brief 写出一段运行状态 JSON 片段。
 *
 * @param[out] buf  输出缓冲。
 * @param[in]  size 缓冲总长度。
 * @return 写入的字符数；还没有任何有效样本或缓冲装不下时返回 0。
 *
 * @note 片段不含外层花括号、不含前导逗号，可直接拼进已有 JSON 对象。
 * @note 没采过的部分整段省略，避免把 0 当成真实读数。
 */
int deviceStatusJson(char *buf, size_t size);
