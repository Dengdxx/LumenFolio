#pragma once

/**
 * @brief 按键开启 60 秒 BLE 可连接窗口。首次调用会启动 NimBLE。
 * @note 只能在 NVS 初始化之后从任务调用。
 */
void board_ble_open_window();

/**
 * @brief 软件重启后，仅在待确认镜像且上次开机通过 BLE 请求过 OTA 时重新广播。
 * @note 每次正常开机调用一次，消费 RTC 中的续接标记。
 */
void board_ble_resume_update(bool pending);

/** @brief 广播窗口或最近有传输活动时，推迟自动深睡。 */
bool board_ble_active();

/** @brief 深睡前停止 BLE 宿主与控制器，之后不再开启连接窗口。 */
void board_ble_suspend();
