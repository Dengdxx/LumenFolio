#include "app.h"

#include <cinttypes>
#include <cstdio>
#include <cstring>

#include "artifacts.h"
#include "board_sd.h"
#include "esp_heap_caps.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "ota_service.h"

namespace {

enum class frame_source { invalid, artifacts, sd };

struct frame_slot {
    const char *id;
    const char *name;
    bool inbox;
};
constexpr frame_slot kSlots[] = {{"boot", "开机画面", false}, {"inbox", "上传画面", true}};

// 同步 HTTP handler 独占，复用缓冲而不占显示任务的 PSRAM 或 httpd 的栈。
board_sd_frame_names s_sd_names;
uint8_t s_chunk[4096];

/** @brief 在一次有效的画面浏览请求中暂停自动深睡，所有返回路径都会恢复空闲计时。 */
class FrameTransfer {
public:
    FrameTransfer() { app_frame_transfer_begin(); }
    ~FrameTransfer() { app_frame_transfer_end(); }
};

const frame_slot *find_slot(const char *id)
{
    for (const auto &slot : kSlots) {
        if (strcmp(id, slot.id) == 0) {
            return &slot;
        }
    }
    return nullptr;
}

struct frame_reader {
    frame_source source;
    size_t size;
    bool inbox;
};

/** @brief 查询帧头及编码长度。SD 成功时由 HTTP handler 负责关闭已打开的文件。 */
esp_err_t open_frame(frame_reader &reader, const char *id)
{
    if (reader.source == frame_source::sd) {
        return board_sd_browse_open(id, reader.size);
    }
    const auto *slot = find_slot(id);
    if (slot == nullptr) {
        return ESP_ERR_NOT_FOUND;
    }
    reader.inbox = slot->inbox;
    return artifacts_frame_size(reader.inbox, &reader.size);
}

constexpr size_t kThumbScale = 10;
constexpr size_t kThumbWidth = EPD_FRAME_WIDTH / kThumbScale;
constexpr size_t kThumbHeight = EPD_FRAME_HEIGHT / kThumbScale;
constexpr size_t kThumbBytes = kThumbWidth * kThumbHeight * 2;

/** @brief 将每块 10×10 的墨水近似色平均，保留抖动混色，输出行优先小端 RGB565。 */
bool make_thumbnail(const uint8_t *frame, uint8_t *out)
{
    // 与 app/pc/panel/panel.go 的 Preview 颜色一致，未使用的色码留零。
    constexpr uint8_t inks[16][3] = {
        {20, 20, 20}, {245, 245, 240}, {240, 200, 30}, {190, 30, 40}, {}, {25, 70, 170}, {30, 140, 60},
    };
    constexpr unsigned area = kThumbScale * kThumbScale;
    for (size_t y = 0; y < kThumbHeight; ++y) {
        for (size_t x = 0; x < kThumbWidth; ++x) {
            unsigned red = 0, green = 0, blue = 0;
            for (size_t row = 0; row < kThumbScale; ++row) {
                const uint8_t *pixels = frame + ((y * kThumbScale + row) * EPD_FRAME_WIDTH + x * kThumbScale) / 2;
                for (size_t i = 0; i < kThumbScale / 2; ++i) {
                    const auto &hi = inks[pixels[i] >> 4];
                    const auto &lo = inks[pixels[i] & 0xf];
                    if (hi[0] == 0 || lo[0] == 0) {
                        return false;
                    }
                    red += hi[0] + lo[0];
                    green += hi[1] + lo[1];
                    blue += hi[2] + lo[2];
                }
            }
            const unsigned r = (red + area / 2) / area;
            const unsigned g = (green + area / 2) / area;
            const unsigned b = (blue + area / 2) / area;
            const uint16_t rgb = ((r >> 3) << 11) | ((g >> 2) << 5) | (b >> 3);
            *out++ = static_cast<uint8_t>(rgb);
            *out++ = static_cast<uint8_t>(rgb >> 8);
        }
    }
    return true;
}

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

void format_storage(char *json, size_t size, uint64_t used, uint64_t total)
{
    if (total == 0) {
        snprintf(json, size, "null");
    } else {
        snprintf(json, size, "{\"used_bytes\":%" PRIu64 ",\"total_bytes\":%" PRIu64 "}", used, total);
    }
}

/** @brief GET /frames/storage 查询两个来源，未能读取的来源返回 null。 */
esp_err_t handle_frame_storage(httpd_req_t *request)
{
    const FrameTransfer transfer;
    size_t flash_total = artifacts_capacity();
    size_t flash_used = 0;
    if (flash_total != 0) {
        for (const auto &slot : kSlots) {
            size_t size;
            const esp_err_t err = artifacts_frame_size(slot.inbox, &size);
            if (err == ESP_OK) {
                flash_used += size;
            } else if (err != ESP_ERR_NOT_FOUND) {
                flash_total = 0;
                break;
            }
        }
    }

    uint64_t sd_used = 0, sd_total = 0;
    if (board_sd_storage(sd_used, sd_total) != ESP_OK) {
        sd_total = 0;
    }
    char flash[80], sd[80], json[192];
    format_storage(flash, sizeof(flash), flash_used, flash_total);
    format_storage(sd, sizeof(sd), sd_used, sd_total);
    snprintf(json, sizeof(json), "{\"artifacts\":%s,\"sd\":%s}", flash, sd);
    httpd_resp_set_type(request, "application/json");
    httpd_resp_set_hdr(request, "Cache-Control", "no-store");
    return httpd_resp_sendstr(request, json);
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
    frame_reader reader{source, 0, false};
    const bool from_sd = source == frame_source::sd;
    esp_err_t err = open_frame(reader, id);
    if (err == ESP_ERR_NOT_FOUND || err == ESP_ERR_INVALID_ARG) {
        return httpd_resp_send_err(request, HTTPD_404_NOT_FOUND, "画面不存在");
    }
    if (err != ESP_OK) {
        return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "无法读取画面");
    }

    httpd_resp_set_type(request, "application/octet-stream");
    httpd_resp_set_hdr(request, "Cache-Control", "no-store");
    for (size_t offset = 0; offset < reader.size;) {
        const size_t remaining = reader.size - offset;
        const size_t count = remaining < sizeof(s_chunk) ? remaining : sizeof(s_chunk);
        err = from_sd ? board_sd_browse_read(s_chunk, count)
                      : artifacts_frame_read(reader.inbox, offset, s_chunk, count);
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

/** @brief HTTP 任务复制请求，入队后把连接交给缩略图任务。 */
struct thumbnail_request {
    httpd_req_t *http;
    frame_source source;
    char id[BOARD_SD_FRAME_NAME_BYTES];
};

QueueHandle_t s_thumb_queue;

/** @brief 读取完整帧并发送小图，文件和 PSRAM 工作区不与其他请求共享。 */
void send_thumbnail(const thumbnail_request &job)
{
    const bool from_sd = job.source == frame_source::sd;
    const auto *slot = from_sd ? nullptr : find_slot(job.id);
    if (!from_sd && slot == nullptr) {
        httpd_resp_send_err(job.http, HTTPD_404_NOT_FOUND, "画面不存在");
        return;
    }

    auto *pixels = static_cast<uint8_t *>(heap_caps_malloc(2 * EPD_FRAME_PIXEL_BYTES, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT));
    if (pixels == nullptr) {
        httpd_resp_set_status(job.http, "503 Service Unavailable");
        httpd_resp_sendstr(job.http, "缩略图工作区不足\n");
        return;
    }
    uint8_t *scratch = pixels + EPD_FRAME_PIXEL_BYTES;
    const esp_err_t err = from_sd ? board_sd_decode_frame(job.id, pixels, scratch)
                                  : artifacts_decode_frame(slot->inbox, pixels, scratch);

    // 解压完成后压缩输入已用完，将工作区复用为 RGB565 响应。
    if (err == ESP_ERR_NO_MEM) {
        httpd_resp_set_status(job.http, "503 Service Unavailable");
        httpd_resp_sendstr(job.http, "缩略图工作区不足\n");
    } else if (err == ESP_ERR_NOT_FOUND || err == ESP_ERR_INVALID_ARG) {
        httpd_resp_send_err(job.http, HTTPD_404_NOT_FOUND, "画面不存在");
    } else if (err != ESP_OK || !make_thumbnail(pixels, scratch)) {
        httpd_resp_send_err(job.http, HTTPD_500_INTERNAL_SERVER_ERROR, "无法生成缩略图");
    } else {
        httpd_resp_set_type(job.http, "application/octet-stream");
        httpd_resp_set_hdr(job.http, "Cache-Control", "no-store");
        httpd_resp_send(job.http, reinterpret_cast<const char *>(scratch), kThumbBytes);
    }
    heap_caps_free(pixels);
}

/** @brief 串行处理缩略图；响应成功或失败都必须归还 HTTP 连接。 */
void thumbnail_task(void *arg)
{
    auto queue = static_cast<QueueHandle_t>(arg);
    for (;;) {
        thumbnail_request job;
        xQueueReceive(queue, &job, portMAX_DELAY);
        send_thumbnail(job);
        httpd_req_async_handler_complete(job.http);
    }
}

/** @brief 由 HTTP 任务按需启动。同核低优先级，OTA 写 Flash 可先于 PSRAM 解码执行。 */
bool start_thumbnail_task()
{
    if (s_thumb_queue != nullptr) return true;
    s_thumb_queue = xQueueCreate(1, sizeof(thumbnail_request));
    if (s_thumb_queue == nullptr) return false;
    if (xTaskCreatePinnedToCore(thumbnail_task, "frame_thumb", 4096, s_thumb_queue, 1, nullptr, xPortGetCoreID()) != pdPASS) {
        vQueueDelete(s_thumb_queue);
        s_thumb_queue = nullptr;
        return false;
    }
    return true;
}

/** @brief GET /frame/thumb 移交连接并立即返回 HTTP 任务，最多等待一项。 */
esp_err_t handle_frame_thumbnail(httpd_req_t *request)
{
    encoded_id id;
    const frame_source source = parse_query(request, &id);
    if (source == frame_source::invalid) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "需要有效的 source 和 id");
    }
    thumbnail_request job{nullptr, source, {}};
    if (strlen(id) >= sizeof(job.id)) {
        return httpd_resp_send_err(request, HTTPD_404_NOT_FOUND, "画面不存在");
    }
    strcpy(job.id, id);
    const FrameTransfer transfer;
    if (!start_thumbnail_task()) {
        httpd_resp_set_status(request, "503 Service Unavailable");
        return httpd_resp_sendstr(request, "缩略图任务暂不可用\n");
    }
    if (uxQueueSpacesAvailable(s_thumb_queue) == 0) {
        httpd_resp_set_status(request, "429 Too Many Requests");
        return httpd_resp_sendstr(request, "缩略图队列已满\n");
    }
    if (httpd_req_async_handler_begin(request, &job.http) != ESP_OK) {
        httpd_resp_set_status(request, "503 Service Unavailable");
        return httpd_resp_sendstr(request, "缩略图请求暂不可用\n");
    }
    // HTTP 任务是唯一生产者，检查到的空位在入队前只会增加。
    xQueueSend(s_thumb_queue, &job, 0);
    return ESP_OK;
}

/** @brief POST /frame/display 验证选中来源并排队，由显示任务读取原件和刷屏。 */
esp_err_t handle_frame_display(httpd_req_t *request)
{
    encoded_id id;
    const frame_source source = parse_query(request, &id);
    if (source == frame_source::invalid || request->content_len != 0) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "需要有效的 source 和 id，且请求体须为空");
    }
    const FrameTransfer transfer;
    frame_reader reader{source, 0, false};
    const esp_err_t err = open_frame(reader, id);
    if (source == frame_source::sd && err == ESP_OK) board_sd_browse_close();
    if (err == ESP_ERR_INVALID_ARG) {
        return httpd_resp_send_err(request, HTTPD_400_BAD_REQUEST, "帧文件名非法");
    }
    if (err == ESP_ERR_NOT_FOUND) {
        return httpd_resp_send_err(request, HTTPD_404_NOT_FOUND, "画面不存在");
    }
    if (err != ESP_OK) {
        return httpd_resp_send_err(request, HTTPD_500_INTERNAL_SERVER_ERROR, "无法读取画面");
    }
    const display_cmd cmd = source == frame_source::sd ? display_cmd::pick_sd
                          : reader.inbox ? display_cmd::pick_inbox : display_cmd::pick_boot;
    httpd_resp_set_type(request, "text/plain; charset=utf-8");
    if (app_post_display(cmd, id) != ESP_OK) {
        httpd_resp_set_status(request, "503 Service Unavailable");
        return httpd_resp_sendstr(request, "显示任务暂不可用或队列已满\n");
    }
    httpd_resp_set_status(request, "202 Accepted");
    return httpd_resp_sendstr(request, "画面刷新已排队\n");
}

}  // namespace

esp_err_t app_register_frame_browse()
{
    static const httpd_uri_t routes[] = {
        {.uri = "/frames", .method = HTTP_GET, .handler = handle_frame_list, .user_ctx = nullptr},
        {.uri = "/frames/storage", .method = HTTP_GET, .handler = handle_frame_storage, .user_ctx = nullptr},
        {.uri = "/frame", .method = HTTP_GET, .handler = handle_frame_download, .user_ctx = nullptr},
        {.uri = "/frame/thumb", .method = HTTP_GET, .handler = handle_frame_thumbnail, .user_ctx = nullptr},
        {.uri = "/frame/display", .method = HTTP_POST, .handler = handle_frame_display, .user_ctx = nullptr},
    };
    for (const auto &route : routes) {
        const esp_err_t err = otaServiceRegisterHandler(&route);
        if (err != ESP_OK) return err;
    }
    return ESP_OK;
}
