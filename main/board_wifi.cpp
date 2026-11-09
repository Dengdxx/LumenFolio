#include "board_wifi.h"

#include <cstring>

#include "esp_event.h"
#include "esp_log.h"
#include "esp_netif.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

namespace {

constexpr const char *kTag = "wifi";

// 上板排查用：连接前先扫一次，日志里就能看出目标热点在不在、信号多强。
constexpr bool kScanOnBoot = true;
constexpr uint16_t kMaxScanRecords = 20;
constexpr uint32_t kRetryDelayMs = 2000;

bool started = false;
bool connected = false;
TaskHandle_t retry_task_handle = nullptr;

/**
 * @brief 扫一次 2.4 GHz，把可见热点打进日志，并标出目标在不在。
 *
 * ESP32-S3 只有 2.4 GHz 射频：电脑热点如果被系统放在 5 GHz，这里永远看不见。
 * 必须在 Wi-Fi 启动之后调用。
 */
void scan_once()
{
    wifi_scan_config_t scan = {};
    scan.show_hidden          = true;
    scan.scan_type            = WIFI_SCAN_TYPE_ACTIVE;
    scan.scan_time.active.min = 100;
    scan.scan_time.active.max = 300;

    esp_err_t err = esp_wifi_scan_start(&scan, true);
    if (err != ESP_OK) {
        ESP_LOGW(kTag, "扫描失败：%s", esp_err_to_name(err));
        return;
    }

    uint16_t count = 0;
    esp_wifi_scan_get_ap_num(&count);
    if (count > kMaxScanRecords) {
        count = kMaxScanRecords;
    }

    wifi_ap_record_t records[kMaxScanRecords] = {};
    if (count > 0) {
        err = esp_wifi_scan_get_ap_records(&count, records);
        if (err != ESP_OK) {
            ESP_LOGW(kTag, "取扫描结果失败：%s", esp_err_to_name(err));
            return;
        }
    }

    bool hit = false;
    for (uint16_t i = 0; i < count; ++i) {
        const char *ssid = reinterpret_cast<const char *>(records[i].ssid);
        const bool is_target = strcmp(ssid, framefolio::board_wifi::kSsid) == 0;
        hit = hit || is_target;
        ESP_LOGI(kTag, "  %-24s ch=%2u rssi=%3d auth=%d%s", ssid, records[i].primary,
                 records[i].rssi, static_cast<int>(records[i].authmode), is_target ? "  <==" : "");
    }
    ESP_LOGI(kTag, "扫描到 %u 个热点，%s %s", static_cast<unsigned>(count), framefolio::board_wifi::kSsid,
             hit ? "在列表里" : "没扫到");
}

/**
 * @brief 断线后延迟重连。
 *
 * 重连不能直接在事件回调里 sleep，也不能立刻猛试：AP 不在时 reason 201 会瞬间
 * 反复触发，把事件任务和日志淹掉。用通知把活交给本任务，隔 2 s 试一次。
 */
void retry_task(void *)
{
    for (;;) {
        ulTaskNotifyTake(pdTRUE, portMAX_DELAY);
        vTaskDelay(pdMS_TO_TICKS(kRetryDelayMs));
        if (!connected) {
            const esp_err_t err = esp_wifi_connect();
            if (err != ESP_OK) {
                ESP_LOGW(kTag, "重连失败：%s", esp_err_to_name(err));
            }
        }
    }
}

void on_wifi_event(void *, esp_event_base_t base, int32_t id, void *data)
{
    if (base != WIFI_EVENT || id != WIFI_EVENT_STA_DISCONNECTED) {
        return;
    }
    const auto *event = static_cast<const wifi_event_sta_disconnected_t *>(data);
    connected = false;
    ESP_LOGW(kTag, "连接断开（reason %d），%u ms 后重试", event->reason,
             static_cast<unsigned>(kRetryDelayMs));
    if (retry_task_handle != nullptr) {
        xTaskNotifyGive(retry_task_handle);
    }
}

void on_got_ip(void *, esp_event_base_t base, int32_t id, void *data)
{
    if (base != IP_EVENT || id != IP_EVENT_STA_GOT_IP) {
        return;
    }
    const auto *event = static_cast<const ip_event_got_ip_t *>(data);
    connected = true;
    ESP_LOGI(kTag, "已连上 %s，IP " IPSTR "，网关 " IPSTR "，OTA 端口 8080", framefolio::board_wifi::kSsid,
             IP2STR(&event->ip_info.ip), IP2STR(&event->ip_info.gw));
}

}  // namespace

esp_err_t board_wifi_start_sta()
{
    if (started) {
        return ESP_OK;
    }

    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_sta();

    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, &on_wifi_event, nullptr));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, &on_got_ip, nullptr));
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));

    wifi_config_t sta = {};
    memcpy(sta.sta.ssid, framefolio::board_wifi::kSsid, sizeof(framefolio::board_wifi::kSsid));
    memcpy(sta.sta.password, framefolio::board_wifi::kPassword, sizeof(framefolio::board_wifi::kPassword));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &sta));
    ESP_ERROR_CHECK(esp_wifi_start());
    // OTA 传输不需要省电，关掉 modem sleep，射频保持唤醒。
    ESP_ERROR_CHECK(esp_wifi_set_ps(WIFI_PS_NONE));

    if (xTaskCreate(retry_task, "wifi_retry", 3072, nullptr, 3, &retry_task_handle) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }

    if (kScanOnBoot) {
        scan_once();
    }

    const esp_err_t err = esp_wifi_connect();
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "发起连接失败：%s", esp_err_to_name(err));
        return err;
    }
    started = true;
    return ESP_OK;
}
