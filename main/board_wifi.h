/**
 * @file board_wifi.h
 * @brief 把设备作为 STA 接入外部热点，只为 OTA 服务。
 *
 * 设备不发射热点：上电后连接固定 SSID 的热点，地址由对端 DHCP 分配，
 * 因此每次开机都可能不同，串口日志会打印拿到的 IP。电脑接在同一个热点上，
 * 用这个 IP 访问设备的 8080 端口即可。
 *
 * 热点只支持 2.4 GHz。电脑热点若被系统放在 5 GHz，射频层根本看不见，
 * 日志里会表现为扫不到、连不上。
 */
#pragma once

#include "esp_err.h"

namespace lumenfolio::board_wifi {

/** @brief 目标热点名。 */
inline constexpr char kSsid[] = "smd";
/** @brief 目标热点密码，WPA2 要求 8 位以上。 */
inline constexpr char kPassword[] = "88888888";

}  // namespace lumenfolio::board_wifi

/**
 * @brief 拉起 STA 并开始连接。
 *
 * @return ESP_OK 表示射频已启动、连接流程已开始，不代表已经连上；
 *         否则返回底层错误码。
 *
 * @note 连接是异步的，成功与否看日志（拿到 IP 会打印 IP_EVENT_STA_GOT_IP 一行）。
 * @note 失败时调用方应继续运行：屏幕仍要能显示，不能因为连不上就停机。
 */
esp_err_t board_wifi_start_sta();
