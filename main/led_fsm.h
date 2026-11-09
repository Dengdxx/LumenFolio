#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "esp_err.h"

/**
 * @file led_fsm.h
 * @brief 灯语状态机。业务只发布事件和持续状态，不直接操作灯。
 *
 * 规格见 docs/灯语设计.md。输出由本模块的一个任务串行驱动，独占 WS2812 的 RMT 通道。
 */

/**
 * @brief 一次性灯语。播放顺序是异常、电量、其余；同一档按入队先后。
 *
 * 只有 LED_EV_BATTERY 使用百分比，其余事件忽略 percent。
 */
typedef enum {
    LED_EV_ANOMALY = 0,     // 红色快闪十次
    LED_EV_BATTERY,         // 电量颜色常亮一次
    LED_EV_BLE_CONNECTED,   // 蓝色慢闪一次
    LED_EV_WIFI_CONNECTED,  // 青色慢闪一次
    LED_EV_DONE,            // 绿色慢闪一次（刷屏完成 / 传输完成 / TF 挂载成功）
    LED_EV_NO_IMAGE,        // 黄色短闪一次
} led_event_t;

/**
 * @brief 持续灯语。可以同时开多个，只显示优先级最高的一个。
 *
 * 刷新高于传图，传图高于 Pair 等待，等待高于充电。
 */
typedef enum {
    LED_STATE_PAIR_WAIT = 0,  // 白色慢闪
    LED_STATE_CHARGING,       // 电量颜色常亮
    LED_STATE_BLE_XFER,       // 蓝色快闪
    LED_STATE_WIFI_XFER,      // 青色快闪
    LED_STATE_EPD_BUSY,       // 黄色慢闪
    LED_STATE_COUNT,
} led_state_t;

/**
 * @brief 启动灯语任务。必须先于 led_event_post() 和 led_state_set()。
 * @return 重复调用返回 ESP_OK。
 * @note 没有并发保护，只在启动阶段调一次。之后不要再调用 board_led_set()。
 */
esp_err_t led_fsm_init(void);

/**
 * @brief 入队一条一次性灯语，立即返回，不打断正在播的那一条。
 * @param event 要播放的事件。
 * @param percent 仅 LED_EV_BATTERY 使用，0–100；超过 100 按 100。
 * @return 未初始化为 ESP_ERR_INVALID_STATE，投递队列满为 ESP_ERR_NO_MEM。
 * @note 不能在中断里调用。内部待播队列满时事件会被丢掉，此时仍可能已经返回 ESP_OK。
 */
esp_err_t led_event_post(led_event_t event, uint8_t percent);

/**
 * @brief 开关一个持续状态。同时开多个时只显示优先级最高的。
 * @param state 必须小于 LED_STATE_COUNT。
 * @param on true 开启，false 关闭。
 * @return 未初始化或 state 越界返回 ESP_ERR_INVALID_STATE。
 * @note 充电颜色用最近一次已经播放的电量百分比；还没播过则按 0。标记是单字节，任务间直接写，不加锁。
 */
esp_err_t led_state_set(led_state_t state, bool on);

/**
 * @brief 熄灯并停住灯语任务，进入深度睡眠前调用。
 *
 * 任务被挂起后不会再有人写 WS2812，灯保持黑色；RMT 通道和 GPIO 都不释放，
 * 因为紧接着整片芯片就要复位。函数返回后不要再调用任何灯语接口。
 *
 * @return ESP_OK 已熄灯；灯语未初始化返回 ESP_ERR_INVALID_STATE，此时灯本来就没亮过。
 */
esp_err_t led_fsm_suspend(void);
