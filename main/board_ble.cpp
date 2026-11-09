#include "board_ble.h"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <cstring>
#include <mutex>

#include "esp_attr.h"
#include "esp_log.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "host/ble_hs.h"
#include "host/util/util.h"
#include "led_fsm.h"
#include "lwip/sockets.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"
#include "ota_service.h"
#include "services/gap/ble_svc_gap.h"
#include "services/gatt/ble_svc_gatt.h"

extern "C" void ble_store_config_init(void);

namespace {

constexpr char kTag[] = "ble";
constexpr char kName[] = "LumenFolio";
constexpr uint32_t kWindowMs = 60000;
constexpr uint32_t kResumeMarker = 0x4c46424c;
constexpr uint16_t kPreferredMtu = 517;
constexpr size_t kAttributeMax = 512;
constexpr long kSocketTimeoutUs = 500000;
constexpr char kOtaUpload[] = "POST /ota ";
constexpr char kImageUpload[] = "POST /frame ";
constexpr char kImageTransfer[] = "POST /frame/transfer";

// NimBLE 接收 UUID 的小端字节序。
const ble_uuid128_t kService = BLE_UUID128_INIT(0x01, 0xac, 0x5e, 0xb2, 0x79, 0x5c, 0xc4, 0x89,
                                               0x75, 0x4d, 0x6b, 0x7e, 0x00, 0x67, 0xa1, 0x9f);
const ble_uuid128_t kWrite = BLE_UUID128_INIT(0x01, 0xac, 0x5e, 0xb2, 0x79, 0x5c, 0xc4, 0x89,
                                             0x75, 0x4d, 0x6b, 0x7e, 0x01, 0x67, 0xa1, 0x9f);
const ble_uuid128_t kRead = BLE_UUID128_INIT(0x01, 0xac, 0x5e, 0xb2, 0x79, 0x5c, 0xc4, 0x89,
                                            0x75, 0x4d, 0x6b, 0x7e, 0x02, 0x67, 0xa1, 0x9f);

enum : uint8_t { kOpen = 1, kData = 2, kClose = 3 };
enum : uint8_t { kWait = 0, kBytes = 1, kEnd = 2, kSocketError = 3 };

std::mutex s_init_mutex;
bool s_started;
std::atomic<bool> s_stopping;
std::atomic<uint32_t> s_awake_until_s;
RTC_NOINIT_ATTR uint32_t s_resume_marker;

// 除初始化外，以下状态由 NimBLE 任务独占。每条 GATT 链路同一时刻只拥有一个本地连接。
ble_npl_event s_open_event;
ble_npl_callout s_window_timer;
int64_t s_window_until_us;
bool s_synced;
uint8_t s_addr_type;
uint16_t s_conn = BLE_HS_CONN_HANDLE_NONE;
int s_socket = -1;
uint8_t s_read_state = kEnd;
char s_request_prefix[sizeof(kImageTransfer) - 1];
size_t s_prefix_size;
bool s_image_upload;

void keep_awake()
{
    s_awake_until_s = static_cast<uint32_t>(esp_timer_get_time() / 1000000) + kWindowMs / 1000 + 1;
}

void close_socket(uint8_t result = kEnd)
{
    if (s_socket >= 0) {
        close(s_socket);
        s_socket = -1;
    }
    s_read_state = result;
    s_prefix_size = 0;
    if (s_image_upload) {
        s_image_upload = false;
        led_state_set(LED_STATE_BLE_XFER, false);
    }
}

bool wait_writable()
{
    fd_set write_set;
    FD_ZERO(&write_set);
    FD_SET(s_socket, &write_set);
    timeval timeout = {0, kSocketTimeoutUs};
    return select(s_socket + 1, nullptr, &write_set, nullptr, &timeout) == 1;
}

/**
 * @brief 打开唯一允许访问的本地 HTTP 端口。回环不依赖 Wi-Fi 关联或 DHCP。
 * @note 超时限制 NimBLE 回调占用，避免 HTTP 服务繁忙时卡住蓝牙宿主任务。
 */
bool open_socket()
{
    close_socket();
    s_socket = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s_socket >= 0 && fcntl(s_socket, F_SETFL, O_NONBLOCK) == 0) {
        const int enabled = 1;
        setsockopt(s_socket, IPPROTO_TCP, TCP_NODELAY, &enabled, sizeof(enabled));
        sockaddr_in addr = {};
        addr.sin_family = AF_INET;
        addr.sin_port = htons(OTA_HTTP_PORT);
        addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        if (connect(s_socket, reinterpret_cast<const sockaddr *>(&addr), sizeof(addr)) == 0) {
            return true;
        }
        if (errno == EINPROGRESS && wait_writable()) {
            int error = 0;
            socklen_t size = sizeof(error);
            if (getsockopt(s_socket, SOL_SOCKET, SO_ERROR, &error, &size) == 0 && error == 0) {
                return true;
            }
        }
    }
    close_socket(kSocketError);
    return false;
}

/** @brief 只看首行的请求前缀，兼容默认 MTU 把路径拆在两块中。 */
void observe_request(const uint8_t *data, size_t size)
{
    if (s_prefix_size == sizeof(s_request_prefix)) return;
    const size_t copied = std::min(size, sizeof(s_request_prefix) - s_prefix_size);
    memcpy(s_request_prefix + s_prefix_size, data, copied);
    s_prefix_size += copied;
    const auto starts_with = [](const char *prefix, size_t length) {
        return s_prefix_size >= length && memcmp(s_request_prefix, prefix, length) == 0;
    };
    if (starts_with(kOtaUpload, sizeof(kOtaUpload) - 1)) {
        s_resume_marker = kResumeMarker;
        s_prefix_size = sizeof(s_request_prefix);
    } else if (starts_with(kImageUpload, sizeof(kImageUpload) - 1) ||
               starts_with(kImageTransfer, sizeof(kImageTransfer) - 1)) {
        s_image_upload = true;
        s_prefix_size = sizeof(s_request_prefix);
        led_state_set(LED_STATE_BLE_XFER, true);
    }
}

/** @brief ATT 写成功表示整块已进入 TCP 发送缓冲；失败后关闭，避免重试造成重复字节。 */
bool send_bytes(const uint8_t *data, size_t size)
{
    if (s_socket < 0) return false;
    while (size != 0) {
        const int written = send(s_socket, data, size, 0);
        if (written > 0) {
            data += written;
            size -= written;
        } else if (written == 0 || (errno != EAGAIN && errno != EWOULDBLOCK) || !wait_writable()) {
            close_socket(kSocketError);
            return false;
        }
    }
    return true;
}

/** @brief 客户端用带响应的 ATT 写按顺序打开、传送和关闭一次 HTTP 请求。 */
int write_request(uint16_t conn, uint16_t, ble_gatt_access_ctxt *context, void *)
{
    if (s_stopping || conn != s_conn || context->op != BLE_GATT_ACCESS_OP_WRITE_CHR) {
        return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    }
    uint8_t data[kAttributeMax];
    uint16_t size = 0;
    if (ble_hs_mbuf_to_flat(context->om, data, sizeof(data), &size) != 0 || size == 0) {
        return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    }
    keep_awake();
    switch (data[0]) {
    case kOpen:
        if (size != 1) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
        return open_socket() ? 0 : BLE_ATT_ERR_UNLIKELY;
    case kData:
        if (size == 1) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
        if (!send_bytes(data + 1, size - 1)) return BLE_ATT_ERR_UNLIKELY;
        observe_request(data + 1, size - 1);
        return 0;
    case kClose:
        if (size != 1) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
        close_socket();
        return 0;
    default:
        return BLE_ATT_ERR_UNLIKELY;
    }
}

/** @brief 每次独立 ATT 读消费一个响应块；没有数据就立即回复等待，宿主任务不阻塞。 */
int read_response(uint16_t conn, uint16_t, ble_gatt_access_ctxt *context, void *)
{
    if (s_stopping || conn != s_conn || context->op != BLE_GATT_ACCESS_OP_READ_CHR) {
        return BLE_ATT_ERR_READ_NOT_PERMITTED;
    }
    keep_awake();
    uint8_t data[kAttributeMax];
    size_t size = 1;
    if (s_socket < 0) {
        data[0] = s_read_state;
    } else {
        // 小于单次 READ_RSP 的上限，防止客户端把下一块作为 Read Blob 接着读。
        const size_t limit = std::min(kAttributeMax, static_cast<size_t>(ble_att_mtu(conn) - 2));
        const int received = recv(s_socket, data + 1, limit - 1, MSG_DONTWAIT);
        if (received > 0) {
            data[0] = kBytes;
            size += received;
        } else if (received < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            data[0] = kWait;
        } else {
            close_socket(received == 0 ? kEnd : kSocketError);
            data[0] = s_read_state;
        }
    }
    return os_mbuf_append(context->om, data, size) == 0 ? 0 : BLE_ATT_ERR_INSUFFICIENT_RES;
}

const ble_gatt_chr_def kCharacteristics[] = {
    {.uuid = &kWrite.u, .access_cb = write_request, .arg = nullptr, .descriptors = nullptr,
     .flags = BLE_GATT_CHR_F_WRITE | BLE_GATT_CHR_F_WRITE_ENC, .min_key_size = 0,
     .val_handle = nullptr, .cpfd = nullptr},
    {.uuid = &kRead.u, .access_cb = read_response, .arg = nullptr, .descriptors = nullptr,
     .flags = BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_READ_ENC, .min_key_size = 0,
     .val_handle = nullptr, .cpfd = nullptr},
    {},
};
const ble_gatt_svc_def kServices[] = {
    {.type = BLE_GATT_SVC_TYPE_PRIMARY, .uuid = &kService.u, .includes = nullptr,
     .characteristics = kCharacteristics},
    {},
};

int on_gap_event(ble_gap_event *event, void *);

void update_advertising()
{
    const bool waiting = !s_stopping && s_synced && s_conn == BLE_HS_CONN_HANDLE_NONE &&
                         esp_timer_get_time() < s_window_until_us;
    if (!waiting) {
        if (s_synced && ble_gap_adv_active()) ble_gap_adv_stop();
        led_state_set(LED_STATE_PAIR_WAIT, false);
        return;
    }
    if (ble_gap_adv_active()) return;

    ble_hs_adv_fields adv = {};
    adv.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
    adv.uuids128 = &kService;
    adv.num_uuids128 = 1;
    adv.uuids128_is_complete = 1;
    ble_hs_adv_fields response = {};
    response.name = reinterpret_cast<const uint8_t *>(kName);
    response.name_len = sizeof(kName) - 1;
    response.name_is_complete = 1;

    ble_gap_adv_params params = {};
    params.conn_mode = BLE_GAP_CONN_MODE_UND;
    params.disc_mode = BLE_GAP_DISC_MODE_GEN;
    int result = ble_gap_adv_set_fields(&adv);
    if (result == 0) result = ble_gap_adv_rsp_set_fields(&response);
    if (result == 0) result = ble_gap_adv_start(s_addr_type, nullptr, BLE_HS_FOREVER, &params, on_gap_event, nullptr);
    if (result == 0) {
        led_state_set(LED_STATE_PAIR_WAIT, true);
        ESP_LOGI(kTag, "BLE 可连接");
    } else {
        ESP_LOGW(kTag, "启动 BLE 广播失败：%d", result);
    }
}

/** @brief 连接成功后可继续使用，即使窗口关闭；只有断开后才需要重新按键或仍处于窗口。 */
int on_gap_event(ble_gap_event *event, void *)
{
    switch (event->type) {
    case BLE_GAP_EVENT_CONNECT:
        if (event->connect.status == 0) {
            s_conn = event->connect.conn_handle;
            keep_awake();
            ble_gap_set_data_len(s_conn, 251, 2120);
            ble_gap_upd_params params = {};
            params.itvl_min = 6;  // 7.5 ms
            params.itvl_max = 12; // 15 ms
            params.supervision_timeout = 400;
            ble_gap_update_params(s_conn, &params);
            const int result = ble_gap_security_initiate(s_conn);
            if (result != 0) ESP_LOGW(kTag, "发起 BLE 加密失败：%d", result);
        }
        update_advertising();
        break;
    case BLE_GAP_EVENT_ENC_CHANGE:
        if (event->enc_change.status == 0) {
            led_event_post(LED_EV_BLE_CONNECTED, 0);
        }
        break;
    case BLE_GAP_EVENT_DISCONNECT:
        close_socket();
        s_conn = BLE_HS_CONN_HANDLE_NONE;
        update_advertising();
        break;
    case BLE_GAP_EVENT_ADV_COMPLETE:
        update_advertising();
        break;
    }
    return 0;
}

void on_reset(int reason)
{
    ESP_LOGW(kTag, "BLE 宿主重置：%d", reason);
    s_synced = false;
    s_conn = BLE_HS_CONN_HANDLE_NONE;
    close_socket();
    update_advertising();
}

void on_sync()
{
    s_synced = ble_hs_util_ensure_addr(0) == 0 && ble_hs_id_infer_auto(0, &s_addr_type) == 0;
    update_advertising();
}

void expire_window(ble_npl_event *)
{
    // FreeRTOS 定时器对齐 tick，回调可能略早于微秒截止时间。
    const int64_t remaining_us = s_window_until_us - esp_timer_get_time();
    if (remaining_us > 0) {
        ble_npl_callout_reset(&s_window_timer, ble_npl_time_ms_to_ticks32((remaining_us + 999) / 1000) + 1);
    } else {
        update_advertising();
    }
}

void open_window(ble_npl_event *)
{
    if (s_stopping) return;
    s_window_until_us = esp_timer_get_time() + kWindowMs * 1000LL;
    keep_awake();
    ble_npl_callout_reset(&s_window_timer, ble_npl_time_ms_to_ticks32(kWindowMs));
    update_advertising();
}

void run_host(void *)
{
    nimble_port_run();
    nimble_port_freertos_deinit();
}

/** @brief 按需初始化宿主与控制器，普通 Wi-Fi 和定时唤醒路径不分配 BLE 堆内存。 */
bool start_host()
{
    const esp_err_t error = nimble_port_init();
    if (error != ESP_OK) {
        ESP_LOGE(kTag, "初始化 NimBLE 失败：%s", esp_err_to_name(error));
        return false;
    }
    ble_hs_cfg.reset_cb = on_reset;
    ble_hs_cfg.sync_cb = on_sync;
    ble_hs_cfg.store_status_cb = ble_store_util_status_rr;
    // 相框没有 PIN 输入。系统 Just Works 配对加密，物理按键限制新连接的时机。
    ble_hs_cfg.sm_io_cap = BLE_HS_IO_NO_INPUT_OUTPUT;
    ble_hs_cfg.sm_sc = 1;
    ble_hs_cfg.sm_bonding = 1;
    ble_hs_cfg.sm_our_key_dist = BLE_SM_PAIR_KEY_DIST_ENC | BLE_SM_PAIR_KEY_DIST_ID;
    ble_hs_cfg.sm_their_key_dist = BLE_SM_PAIR_KEY_DIST_ENC | BLE_SM_PAIR_KEY_DIST_ID;

    ble_svc_gap_init();
    ble_svc_gatt_init();
    int result = ble_svc_gap_device_name_set(kName);
    if (result == 0) result = ble_att_set_preferred_mtu(kPreferredMtu);
    if (result == 0) result = ble_gatts_count_cfg(kServices);
    if (result == 0) result = ble_gatts_add_svcs(kServices);
    if (result != 0) {
        ESP_LOGE(kTag, "注册 BLE 服务失败：%d", result);
        nimble_port_deinit();
        return false;
    }
    ble_store_config_init();
    ble_npl_event_init(&s_open_event, open_window, nullptr);
    ble_npl_callout_init(&s_window_timer, nimble_port_get_dflt_eventq(), expire_window, nullptr);
    nimble_port_freertos_init(run_host);
    return true;
}

}  // namespace

void board_ble_open_window()
{
    keep_awake();
    std::lock_guard lock(s_init_mutex);
    if (s_stopping) return;
    if (!s_started) s_started = start_host();
    if (s_started) ble_npl_eventq_put(nimble_port_get_dflt_eventq(), &s_open_event);
}

void board_ble_resume_update(bool pending)
{
    const bool resume = pending && esp_reset_reason() == ESP_RST_SW && s_resume_marker == kResumeMarker;
    s_resume_marker = 0;
    if (resume) board_ble_open_window();
}

bool board_ble_active()
{
    const uint32_t until = s_awake_until_s.load();
    return until != 0 && static_cast<int32_t>(until - static_cast<uint32_t>(esp_timer_get_time() / 1000000)) > 0;
}

void board_ble_suspend()
{
    std::lock_guard lock(s_init_mutex);
    s_stopping = true;
    if (!s_started) return;

    if (nimble_port_stop() == 0) {
        // 停止宿主后回调不再操作套接字或灯语，才可以释放这些资源。
        ble_npl_callout_deinit(&s_window_timer);
        close_socket();
        nimble_port_deinit();
    }
}
