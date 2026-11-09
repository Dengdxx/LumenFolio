#pragma once

#include <stdint.h>

#include "esp_err.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"

/**
 * @file board_keys.h
 * @brief 三个按键：GPIO 边沿中断加时间窗消抖。
 *
 * 引脚见 board_pins.h：KEY1 GPIO3、KEY2 GPIO8、KEY3 GPIO18，外部 10k 上拉、
 * 按下接地。KEY1、KEY2 的下降沿中断用 60 ms 时间窗消抖，按住不连发。
 * KEY3 由独立任务判定短按或长按，避免显示任务忙时延误蓝牙窗口。
 *
 * 中断只做判定和投递，不做任何耗时动作。翻页与刷屏由显示任务串行处理，
 * 因为一次整帧刷新要几十秒，放在中断或按键上下文里会拖垮系统。
 *
 * @note 调用前必须先执行 board_gpio_init()：它负责把这三个脚配成输入。
 */

/** @brief 按键语义。物理布局未定，先按编号分配。 */
typedef enum {
    BOARD_KEY_PREV = 0, /**< KEY1（GPIO3）：上一张。 */
    BOARD_KEY_NEXT,     /**< KEY2（GPIO8）：下一张。 */
    BOARD_KEY_AUX,      /**< KEY3（GPIO18）：短按重刷，长按开启蓝牙。 */
} board_key_t;

/** @brief 一次按下事件。 */
typedef struct {
    board_key_t key;
} board_key_event_t;

/**
 * @brief 注册中断并创建事件队列。重复调用返回 ESP_OK。
 *
 * @param aux_wake 是否由 KEY3 唤醒。唤醒时的按下沿已发生，由任务补判短按或长按。
 * @return ESP_OK 成功；GPIO 中断服务或队列创建失败时返回底层错误码。
 *
 * @note 会调用 gpio_install_isr_service()；若别的模块已经装过，按成功处理。
 */
esp_err_t board_keys_init(bool aux_wake);

/**
 * @brief 等待一条按下事件。
 *
 * @param[out] event      事件写入处。
 * @param[in]  timeout_ms 最多等多少毫秒；UINT32_MAX 表示一直等；0 表示只取已排队的。
 * @return ESP_OK 取到；ESP_ERR_TIMEOUT 超时；ESP_ERR_INVALID_STATE 未初始化。
 *
 * @note 队列深度 8，满了在中断里直接丢弃，不会阻塞也不丢已有事件。
 */
esp_err_t board_keys_read(board_key_event_t *event, uint32_t timeout_ms);

/**
 * @brief 按键事件队列的句柄，用于和别的命令队列一起放进 queue set 里等。
 *
 * @return 队列句柄；未初始化时为 NULL。
 */
QueueHandle_t board_keys_queue(void);
