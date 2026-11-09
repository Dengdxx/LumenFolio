/**
 * @file  ota_service.c
 * @brief OTA HTTP 服务实现：状态查询、固件上传与镜像确认。
 *
 * 上传按固定 Content-Length 流式接收，4 KiB 缓冲在 httpd 同步 handler 中复用；
 * 写 Flash 由 ota_update 状态机串行化。上传成功后 1 s 重启，
 * 确认接口用于取消新镜像的回滚。
 */
#include <stdio.h>
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_http_server.h"
#include "esp_timer.h"
#include "lwip/sockets.h"
#include "ota_service.h"
#include "ota_update.h"

/** httpd 与后台 poll 任务固定在 core 0，不与刷屏任务抢同一个核。 */
#define OTA_TASK_CORE 0

/** httpd 句柄；启动失败或路由注册失败时置 NULL。 */
static httpd_handle_t server;
// httpd 同步 handler 串行使用此缓冲，不将整包放入 RAM 或 HTTP 任务栈。
static unsigned char uploadBuffer[4096];
/** 运行状态附加字段提供者，未注册时为 NULL。启动阶段单线程写入，之后只读。 */
static otaStatusExtra_t statusExtraProvider;

/**
 * @brief 返回 OTA 阶段名，用于 JSON 状态输出。
 *
 * @param[in] phase 状态机阶段。
 * @return 静态阶段名；未知值返回 "unknown"。
 */
static const char *phaseName(otaPhase_t phase)
{
    switch (phase) {
        case otaIdle: return "idle";
        case otaReceiving: return "receiving";
        case otaRebootPending: return "reboot_pending";
        case otaPendingVerify: return "pending_verify";
        case otaRollbackFailed: return "rollback_failed";
        default: return "unknown";
    }
}

// 版本名可能包含引号或控制字符；输入最多 32 字节，输出调用方预留 6 倍空间。
/**
 * @brief 把字符串转义为 JSON 字面量内容（不含首尾引号）。
 *
 * @param[out] out   输出缓冲区，需为输入长度的 6 倍。
 * @param[in]  value 待转义字符串。
 */
static void jsonString(char *out, const char *value)
{
    for (const unsigned char *p = (const unsigned char *)value; *p; p++) {
        if (*p == '"' || *p == '\\') { *out++ = '\\'; *out++ = *p; }
        else if (*p < 32) { snprintf(out, 7, "\\u%04x", *p); out += 6; }
        else *out++ = *p;
    }
    *out = 0;
}

/**
 * @brief GET /ota/status handler：返回当前 OTA 状态 JSON。
 *
 * @param[in,out] request HTTP 请求。
 * @return ESP_OK 表示响应已发送。
 */
static esp_err_t sendStatus(httpd_req_t *request)
{
    otaStatus_t status;
    otaUpdateGetStatus(&status);
    char version[193], project[193], running[103], boot[103], next[103], response[1280];
    jsonString(version, status.version); jsonString(project, status.project);
    jsonString(running, status.runningPartition); jsonString(boot, status.bootPartition);
    jsonString(next, status.updatePartition);
    int used = snprintf(response, sizeof(response),
        "{\"phase\":\"%s\",\"ready\":%s,\"pending_verify\":%s,\"project\":\"%s\",\"version\":\"%s\",\"image_id\":\"%s\","
        "\"running_partition\":\"%s\",\"boot_partition\":\"%s\",\"update_partition\":\"%s\","
        "\"capacity\":%u,\"received\":%u,\"total\":%u,\"last_error\":%d",
        phaseName(status.phase), status.ready ? "true" : "false", status.pendingVerify ? "true" : "false",
        project, version, status.imageId, running, boot, next, (unsigned)status.capacity, (unsigned)status.received,
        (unsigned)status.total, status.lastError);
    if (used < 0) {
        used = 0;
    }
    if ((size_t)used > sizeof(response) - 2) {
        used = (int)sizeof(response) - 2;
    }
    // 附加字段由业务侧提供：留 1 字节给收尾的 '}'、1 字节给 NUL。
    if (statusExtraProvider != NULL && (size_t)used + 2 <= sizeof(response)) {
        response[used] = ',';
        const size_t room = sizeof(response) - (size_t)used - 2;
        const int extra = statusExtraProvider(response + used + 1, room);
        // 写满或截断都不采信，宁可少字段也不能让 JSON 断在半截。
        if (extra > 0 && (size_t)extra <= room) {
            used += 1 + extra;
        }
    }
    response[used++] = '}';
    response[used] = '\0';
    httpd_resp_set_type(request, "application/json");
    httpd_resp_set_hdr(request, "Cache-Control", "no-store");
    return httpd_resp_sendstr(request, response);
}

/**
 * @brief 以纯文本返回失败并主动关闭连接。
 *
 * 请求体可能未读完，关闭连接可避免剩余固件数据被解释为下一个请求。
 *
 * @param[in,out] request HTTP 请求。
 * @param[in]     status  HTTP 状态行。
 * @param[in]     message 响应正文。
 * @return 始终返回 ESP_FAIL，让 httpd 关闭连接。
 */
static esp_err_t failRequest(httpd_req_t *request, const char *status, const char *message)
{
    httpd_resp_set_status(request, status);
    httpd_resp_set_type(request, "text/plain; charset=utf-8");
    httpd_resp_set_hdr(request, "Connection", "close");
    httpd_resp_sendstr(request, message);
    return ESP_FAIL;
}

/**
 * @brief POST /ota handler：接收并写入完整固件镜像。
 *
 * 要求固定 Content-Length 与 application/octet-stream；按 4 KiB 分块流式
 * 写入。局域网总时限 120 s，本机 BLE 隧道时限 15 min，两者均沿用 HTTP 的断流超时。
 * 任何失败都会中止 OTA 并返回可读错误。
 *
 * @param[in,out] request HTTP 请求。
 * @return ESP_OK 表示镜像已写入并已安排重启；失败返回 ESP_FAIL。
 */
static esp_err_t upload(httpd_req_t *request)
{
    char type[48];
    if (httpd_req_get_hdr_value_len(request, "Transfer-Encoding") || !request->content_len)
        return failRequest(request, "411 Length Required", "Send a fixed Content-Length, not chunked encoding.");
    if (httpd_req_get_hdr_value_str(request, "Content-Type", type, sizeof(type)) != ESP_OK ||
        strcmp(type, "application/octet-stream") != 0)
        return failRequest(request, "415 Unsupported Media Type", "Send a raw application .bin.");
    esp_err_t error = otaUpdateBegin(request->content_len);
    if (error == ESP_ERR_INVALID_SIZE)
        return failRequest(request, "413 Content Too Large", "Image does not fit the OTA slot.");
    if (error == ESP_ERR_INVALID_STATE || error == ESP_ERR_NOT_FOUND)
        return failRequest(request, "409 Conflict", "OTA requires idle firmware and OTA partitions.");
    if (error != ESP_OK)
        return failRequest(request, "500 Internal Server Error", "Could not begin the OTA write.");

    size_t remaining = request->content_len;
    // BLE 隧道从 127.0.0.1 接入，ATT 往返延迟可使传输超过局域网时限。
    struct sockaddr_storage peer = {0};
    socklen_t peer_len = sizeof(peer);
    bool loopback = false;
    if (getpeername(httpd_req_to_sockfd(request), (struct sockaddr *)&peer, &peer_len) == 0) {
        if (peer.ss_family == AF_INET) {
            loopback = ((struct sockaddr_in *)&peer)->sin_addr.s_addr == htonl(INADDR_LOOPBACK);
        }
#if LWIP_IPV6
        if (peer.ss_family == AF_INET6) {
            const struct in6_addr *addr = &((struct sockaddr_in6 *)&peer)->sin6_addr;
            static const unsigned char mapped_loopback[16] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 127, 0, 0, 1};
            loopback = memcmp(addr, mapped_loopback, sizeof(mapped_loopback)) == 0;
        }
#endif
    }
    const int64_t deadline = esp_timer_get_time() + (loopback ? 900LL : 120LL) * 1000000;
    while (remaining) {
        if (esp_timer_get_time() >= deadline) { error = ESP_ERR_TIMEOUT; break; }
        const size_t count = remaining < sizeof(uploadBuffer) ? remaining : sizeof(uploadBuffer);
        const int received = httpd_req_recv(request, (char *)uploadBuffer, count);
        if (received <= 0) {
            error = received == HTTPD_SOCK_ERR_TIMEOUT ? ESP_ERR_TIMEOUT : ESP_FAIL;
            break;
        }
        error = otaUpdateWrite(uploadBuffer, (size_t)received);
        if (error != ESP_OK) break;
        remaining -= (size_t)received;
    }
    if (error == ESP_OK) error = otaUpdateFinish();
    if (error != ESP_OK) {
        otaUpdateAbort(error);
        char message[96];
        snprintf(message, sizeof(message), "OTA failed: %s. Check /ota/status before retrying.", esp_err_to_name(error));
        return failRequest(request, error == ESP_ERR_TIMEOUT ? "408 Request Timeout" : "422 Unprocessable Content",
                           message);
    }
    httpd_resp_set_status(request, "202 Accepted");
    httpd_resp_set_hdr(request, "Connection", "close");
    // 一秒后后台重启，即使这份最终答复丢失，地面站也可重连读取实际状态。
    return sendStatus(request);
}

/**
 * @brief POST /ota/confirm handler：用请求头中的运行镜像信息确认新镜像。
 *
 * 必须携带 X-OTA-Partition、X-OTA-Version 与 X-OTA-Image 三个请求头，
 * 且不能有请求体。
 *
 * @param[in,out] request HTTP 请求。
 * @return ESP_OK 表示确认成功；失败返回 ESP_FAIL。
 */
static esp_err_t confirm(httpd_req_t *request)
{
    char partition[17], version[33], imageId[65];
    if (request->content_len || httpd_req_get_hdr_value_len(request, "Transfer-Encoding"))
        return failRequest(request, "400 Bad Request", "Confirmation has no request body.");
    if (httpd_req_get_hdr_value_str(request, "X-OTA-Partition", partition, sizeof(partition)) != ESP_OK ||
        httpd_req_get_hdr_value_str(request, "X-OTA-Version", version, sizeof(version)) != ESP_OK ||
        httpd_req_get_hdr_value_str(request, "X-OTA-Image", imageId, sizeof(imageId)) != ESP_OK)
        return failRequest(request, "400 Bad Request", "Expected running partition and version are required.");
    if (otaUpdateConfirm(partition, version, imageId) != ESP_OK)
        return failRequest(request, "409 Conflict", "This image is not ready for confirmation or does not match.");
    return sendStatus(request);
}

/**
 * @brief OTA 后台任务：每 100 ms 驱动一次状态机。
 *
 * 负责到达重启延迟后重启，以及待确认镜像超时后的回滚。
 *
 * @param[in] arg 未使用。
 */
static void otaWorker(void *arg)
{
    (void)arg;
    for (;;) {
        otaUpdatePoll();
        vTaskDelay(pdMS_TO_TICKS(100));
    }
}

esp_err_t otaServiceInit(void)
{
    esp_err_t error = otaUpdateInit();
    if (error != ESP_OK) return error;
    return xTaskCreatePinnedToCore(otaWorker, "OTA", 4096, NULL, 2, NULL, OTA_TASK_CORE) == pdPASS ?
        ESP_OK : ESP_ERR_NO_MEM;
}

esp_err_t otaServiceStart(void)
{
    httpd_config_t config = HTTPD_DEFAULT_CONFIG();
    config.server_port = OTA_HTTP_PORT;
    config.core_id = OTA_TASK_CORE;
    config.task_priority = 2;
    config.stack_size = 6144;
    config.max_open_sockets = 3;
    // OTA 自己用掉 3 条路由，再留 10 条给设备控制类接口（屏幕刷新、翻页、深睡等）。
    config.max_uri_handlers = 13;
    /* 空闲连接可由新连接回收，避免地面站断线后端口被长期占用。 */
    config.lru_purge_enable = true;
    esp_err_t error = httpd_start(&server, &config);
    if (error != ESP_OK) return error;
    const httpd_uri_t routes[] = {
        {.uri = "/ota/status", .method = HTTP_GET, .handler = sendStatus},
        {.uri = "/ota", .method = HTTP_POST, .handler = upload},
        {.uri = "/ota/confirm", .method = HTTP_POST, .handler = confirm},
    };
    for (unsigned i = 0; i < sizeof(routes) / sizeof(routes[0]); i++) {
        error = httpd_register_uri_handler(server, &routes[i]);
        if (error != ESP_OK) { httpd_stop(server); server = NULL; return error; }
    }
    return ESP_OK;
}

void otaServiceReady(void) { otaUpdateSetReady(); }
bool otaServicePendingVerification(void) { return otaUpdatePendingVerification(); }
void otaServiceSetStatusExtra(otaStatusExtra_t extra) { statusExtraProvider = extra; }
esp_err_t otaServiceRegisterHandler(const httpd_uri_t *uri)
{
    if (server == NULL || uri == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    return httpd_register_uri_handler(server, uri);
}
