/**
 * @file  ota_service.h
 * @brief 基于 HTTP 的 OTA 入口。
 *
 * 在 core 0 上提供 GET /ota/status、POST /ota、POST /ota/confirm 三个接口，
 * 端口 OTA_HTTP_PORT；设备控制类接口通过 otaServiceRegisterHandler() 挂在同一个
 * 服务上。服务在 Wi-Fi 启动后即开启，不等待屏幕自检，使卡在初始化阶段的旧镜像
 * 仍可被替换。
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>

#include "esp_err.h"
#include "esp_http_server.h"

#ifdef __cplusplus
extern "C" {
#endif

/** @brief OTA HTTP 服务端口。 */
#define OTA_HTTP_PORT 8080

/**
 * @brief 初始化 OTA 状态机并创建后台 poll 任务。
 *
 * @return ESP_OK 成功；否则返回底层错误码。
 *
 * @note 由 app_main 在 nvs_flash_init() 之前调用：待确认镜像遇到 NVS 错误时
 *       不得擦除 NVS，否则回滚回去的旧镜像会丢失仍需要的数据。
 */
esp_err_t otaServiceInit(void);

/**
 * @brief 启动 OTA HTTP server 并注册路由。
 *
 * @return ESP_OK 成功；端口占用、内存不足或 handler 注册失败时返回错误码。
 *
 * @note 必须在 SoftAP 与 netif 已经起来之后调用，否则监听无法绑定。
 */
esp_err_t otaServiceStart(void);

/**
 * @brief 通知状态机启动流程已返回，允许确认待验证镜像。
 *
 * @note 仅设置 ready 标志。确认仍要求分区名、版本与 image id 全部匹配。
 *       未调用则新镜像在 60 s 后被回滚。
 */
void otaServiceReady(void);

/**
 * @brief 查询本次启动是否处于待确认阶段。
 *
 * @return 待确认返回 true，否则返回 false。
 */
bool otaServicePendingVerification(void);

/**
 * @brief 设备运行状态的附加字段提供者。
 *
 * /ota/status 的 JSON 会在 OTA 字段之后追加这个回调写出的内容，用于把电池、
 * 温湿度等状态一并通过局域网读出来。回调在 HTTP 任务里同步执行，只能读缓存，
 * 不得做阻塞采样或开合电源支路。
 *
 * @param[out] buf  输出缓冲：写一段不含外层花括号、不含前导逗号的 JSON 片段。
 * @param[in]  size 缓冲总长度。
 * @return 写入的字符数；0 表示本次没有字段可写；负值表示失败。
 */
typedef int (*otaStatusExtra_t)(char *buf, size_t size);

/**
 * @brief 注册运行状态字段提供者。
 *
 * @param[in] extra 提供者；传 NULL 取消注册。
 *
 * @note 可以反复调用，以最后一次为准。未注册时 /ota/status 的输出与原来一致。
 */
void otaServiceSetStatusExtra(otaStatusExtra_t extra);

/**
 * @brief 在 OTA 的 HTTP 服务上再挂一个路由。
 *
 * 设备只有这一个 HTTP 服务，屏幕控制之类的小接口挂在同一端口上，不必再开一个。
 *
 * @param[in] uri 路由描述；它指向的内存必须比服务活得久，通常用 static 常量。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 服务尚未启动。
 *
 * @note 只能在 otaServiceStart() 之后调用。
 */
esp_err_t otaServiceRegisterHandler(const httpd_uri_t *uri);

#ifdef __cplusplus
}
#endif
