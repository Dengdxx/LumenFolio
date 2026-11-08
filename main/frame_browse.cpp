#include "app.h"

#include <cstdio>
#include <cstring>

#include "artifacts.h"
#include "board_sd.h"
#include "ota_service.h"

namespace {

enum class frame_source { invalid, artifacts, sd };

constexpr struct {
    const char *id;
    const char *name;
    bool inbox;
} kSlots[] = {{"boot", "开机画面", false}, {"inbox", "上传画面", true}};

// 同步 HTTP handler 独占，复用缓冲而不占显示任务的 PSRAM 或 httpd 的栈。
board_sd_frame_names s_sd_names;
uint8_t s_chunk[4096];

/** @brief 在一次有效的画面浏览请求中暂停自动深睡，所有返回路径都会恢复空闲计时。 */
class FrameTransfer {
public:
    FrameTransfer() { app_frame_transfer_begin(); }
    ~FrameTransfer() { app_frame_transfer_end(); }
};

int hex_digit(char ch)
{
    if (ch >= '0' && ch <= '9') return ch - '0';
    if (ch >= 'a' && ch <= 'f') return ch - 'a' + 10;
    if (ch >= 'A' && ch <= 'F') return ch - 'A' + 10;
    return -1;
}

/** @brief 解开 URL 查询参数，拒绝非法转义和会截断文件名的 NUL。 */
bool decode_id(char *id)
{
    char *out = id;
    for (const char *in = id; *in; ++in) {
        if (*in == '%') {
            if (!in[1] || !in[2]) return false;
            const int high = hex_digit(in[1]), low = hex_digit(in[2]);
            if (high < 0 || low < 0 || (high == 0 && low == 0)) return false;
            *out++ = static_cast<char>(high * 16 + low);
            in += 2;
        } else {
            *out++ = *in == '+' ? ' ' : *in;
        }
    }
    *out = '\0';
    return out != id;
}

using encoded_id = char[BOARD_SD_FRAME_NAME_BYTES * 3];

frame_source parse_query(httpd_req_t *request, encoded_id *id = nullptr)
{
    char query[sizeof(encoded_id) + 24];
    char source[16];
    if (httpd_req_get_url_query_str(request, query, sizeof(query)) != ESP_OK ||
        httpd_query_key_value(query, "source", source, sizeof(source)) != ESP_OK ||
        (id != nullptr &&
         (httpd_query_key_value(query, "id", *id, sizeof(*id)) != ESP_OK || !decode_id(*id)))) {
        return frame_source::invalid;
    }
    if (strcmp(source, "artifacts") == 0) return frame_source::artifacts;
    if (strcmp(source, "sd") == 0) return frame_source::sd;
    return frame_source::invalid;
}

/** @brief 转义 JSON 字符串内容，调用方为每个输入字节预留最多 6 字节。 */
void escape_json(char *out, const char *value)
{
    for (const unsigned char *p = reinterpret_cast<const unsigned char *>(value); *p; ++p) {
        if (*p == '"' || *p == '\\') {
            *out++ = '\\';
            *out++ = *p;
        } else if (*p < 32) {
            snprintf(out, 7, "\\u%04x", static_cast<unsigned>(*p));
            out += 6;
        } else {
            *out++ = *p;
        }
    }
    *out = '\0';
}

esp_err_t send_list_item(httpd_req_t *request, const char *id, const char *name, bool first)
{
    constexpr size_t kEscapedBytes = (BOARD_SD_FRAME_NAME_BYTES - 1) * 6 + 1;
    char escaped_id[kEscapedBytes], escaped_name[kEscapedBytes];
    escape_json(escaped_id, id);
    escape_json(escaped_name, name);
    char json[kEscapedBytes * 2 + 24];
    snprintf(json, sizeof(json), "%s{\"id\":\"%s\",\"name\":\"%s\"}",
             first ? "" : ",", escaped_id, escaped_name);
    return httpd_resp_sendstr_chunk(request, json);
}

/** @brief GET /frames 按来源列出可预览的帧，未挂载的 SD 与空目录分别表示。 */
esp_err_t handle_frame_list(httpd_req_t *request)
{
    const frame_source source = parse_query(request);
    if (source == frame_source::invalid) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "需要 source=artifacts 或 source=sd");
    }
    const FrameTransfer transfer;
    bool available = true;
    int count = 0;
    bool present[sizeof(kSlots) / sizeof(kSlots[0])] = {};
    if (source == frame_source::sd) {
        const esp_err_t err = board_sd_browse_frames(s_sd_names, count);
        available = err != ESP_ERR_INVALID_STATE;
        if (available && err != ESP_OK) {
            return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "无法读取 SD 卡目录");
        }
    } else {
        for (size_t i = 0; i < sizeof(present) / sizeof(present[0]); ++i) {
            size_t size;
            const esp_err_t err = artifacts_frame_size(kSlots[i].inbox, &size);
            if (err != ESP_OK && err != ESP_ERR_NOT_FOUND) {
                return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "无法读取 Flash 帧头");
            }
            present[i] = err == ESP_OK;
        }
    }

    httpd_resp_set_type(request, "application/json");
    httpd_resp_set_hdr(request, "Cache-Control", "no-store");
    if (httpd_resp_sendstr_chunk(request, available ? "{\"available\":true,\"frames\":["
                                                   : "{\"available\":false,\"frames\":[") != ESP_OK) {
        return ESP_FAIL;
    }
    if (source == frame_source::sd) {
        for (int i = 0; i < count; ++i) {
            if (send_list_item(request, s_sd_names[i], s_sd_names[i], i == 0) != ESP_OK) return ESP_FAIL;
        }
    } else {
        bool first = true;
        for (size_t i = 0; i < sizeof(present) / sizeof(present[0]); ++i) {
            if (!present[i]) continue;
            if (send_list_item(request, kSlots[i].id, kSlots[i].name, first) != ESP_OK) return ESP_FAIL;
            first = false;
        }
    }
    if (httpd_resp_sendstr_chunk(request, "]}") != ESP_OK) return ESP_FAIL;
    return httpd_resp_send_chunk(request, nullptr, 0);
}

/** @brief GET /frame 流式返回编码帧，发送中断时关闭连接，避免客户端接收半帧。 */
esp_err_t handle_frame_download(httpd_req_t *request)
{
    encoded_id id;
    const frame_source source = parse_query(request, &id);
    if (source == frame_source::invalid) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "需要有效的 source 和 id");
    }
    const FrameTransfer transfer;
    size_t size = 0;
    bool inbox = false;
    const bool from_sd = source == frame_source::sd;
    esp_err_t err = ESP_ERR_NOT_FOUND;
    if (from_sd) {
        err = board_sd_browse_open(id, size);
    } else {
        for (const auto &slot : kSlots) {
            if (strcmp(id, slot.id) == 0) {
                inbox = slot.inbox;
                err = artifacts_frame_size(inbox, &size);
                break;
            }
        }
    }
    if (err == ESP_ERR_NOT_FOUND) {
        return httpd_resp_send_err(request, HTTPD_404_NOT_FOUND, "画面不存在");
    }
    if (err != ESP_OK) {
        return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "无法读取画面");
    }

    httpd_resp_set_type(request, "application/octet-stream");
    httpd_resp_set_hdr(request, "Cache-Control", "no-store");
    for (size_t offset = 0; offset < size;) {
        const size_t remaining = size - offset;
        const size_t count = remaining < sizeof(s_chunk) ? remaining : sizeof(s_chunk);
        err = from_sd ? board_sd_browse_read(s_chunk, count)
                      : artifacts_frame_read(inbox, offset, s_chunk, count);
        if (err != ESP_OK ||
            httpd_resp_send_chunk(request, reinterpret_cast<const char *>(s_chunk), count) != ESP_OK) {
            err = ESP_FAIL;
            break;
        }
        offset += count;
    }
    if (from_sd) board_sd_browse_close();
    return err == ESP_OK ? httpd_resp_send_chunk(request, nullptr, 0) : ESP_FAIL;
}

}  // namespace

esp_err_t app_register_frame_browse()
{
    static const httpd_uri_t routes[] = {
        {.uri = "/frames", .method = HTTP_GET, .handler = handle_frame_list, .user_ctx = nullptr},
        {.uri = "/frame", .method = HTTP_GET, .handler = handle_frame_download, .user_ctx = nullptr},
    };
    for (const auto &route : routes) {
        const esp_err_t err = otaServiceRegisterHandler(&route);
        if (err != ESP_OK) return err;
    }
    return ESP_OK;
}
