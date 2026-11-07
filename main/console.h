#pragma once

#include <stddef.h>

/**
 * @file console.h
 * @brief 设备控制台：一套命令，串口和 HTTP 两个入口。
 *
 * 命令语义只有 console_execute() 这一份实现。串口按行输入（回车执行），
 * HTTP 用 `GET /console?cmd=<命令>`，两边收到的回执文本也完全相同。
 * 所以除了看日志，日常操作不必再开串口。
 *
 * 命令只负责解析和投递：翻页、刷屏、睡眠都由显示任务异步执行，回执里的
 * "queued" 表示已入队，不代表动作已经做完。
 */

/**
 * @brief 执行一条命令，把回执写进 out。
 *
 * @param[in]  line 命令行，不含换行；首尾空白会被忽略，大小写不敏感。
 * @param[out] buf  回执文本，总是以 '\0' 结尾。
 * @param[in]  size 缓冲长度；不足时回执被截断。
 *
 * @note 可以在任意任务里调用，内部不持有锁，也不阻塞。
 */
void console_execute(const char *line, char *buf, size_t size);

/** @brief 命令表文本，供 help 命令和串口上电提示使用。 */
const char *console_help_text();
