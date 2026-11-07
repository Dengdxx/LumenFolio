#pragma once

#include <stddef.h>

#include "esp_err.h"

/**
 * @file app.h
 * @brief 应用层动作入口。
 *
 * 控制台（串口与 HTTP 共用的那套命令）通过这几个函数驱动应用，不直接碰
 * 任务、队列和硬件。命令语义在 console.cpp 里，动作实现留在 main.cpp。
 */

/** @brief 屏幕动作。按键与控制台都投到这里，由显示任务串行执行。 */
enum class display_cmd { refresh, next, prev, factory };

/**
 * @brief 把一条屏幕动作投给显示任务。
 *
 * @param[in] cmd 动作。
 * @return ESP_OK 已入队；ESP_ERR_INVALID_STATE 命令队列还没建好；ESP_ERR_NO_MEM 队列满。
 *
 * @note 只投递不等待：一次整帧刷新要几十秒，控制台不能跟着卡住。
 */
esp_err_t app_post_display(display_cmd cmd);

/**
 * @brief 请求立即进入深度睡眠。
 *
 * @note 只置标志，真正的睡眠由电源守护任务在 1 s 内执行。显式请求不看充电器、
 *       USB 枚举和 OTA 状态，也不受显示任务是否还活着影响。
 */
void app_request_sleep(void);

/**
 * @brief 本次开机不再自动睡眠，直到复位或收到 sleep。
 *
 * @note 电池供电时空闲超时会自动睡。这条命令把它钉住，方便长时间观察。
 *       接着充电器时本来就不会自动睡。
 */
void app_hold_awake(void);

/**
 * @brief 写一段运行时诊断文本，供控制台的 status 命令使用。
 *
 * @param[out] buf  输出缓冲，按行写 key=value。
 * @param[in]  size 缓冲长度。
 *
 * @note 只读缓存，不采样、不阻塞，可以从 HTTP 任务里调。
 */
void app_describe(char *buf, size_t size);
