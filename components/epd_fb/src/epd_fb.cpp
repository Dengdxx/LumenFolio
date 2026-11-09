#include "epd_fb.h"

#include <string.h>

#include "epd709_pins.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "epd709_comm.h"
#include "status.h"

namespace {

constexpr const char *kTag = "epd_fb";

/* 分块搬运的块大小，必须 <= 驱动里的 32 KB DMA 缓冲。 */
constexpr size_t kChunkBytes = EPD_SPI_CHUNK_SIZE;

constexpr size_t kLineBytes = EPD_FB_IC_WIDTH / 2;                 /* 300 B/行 */
constexpr size_t kPlaneOffset[2] = {0, EPD_FB_PLANE_BYTES};

uint8_t *s_frame[2];   /* 两块整帧；s_frame[0] 起始即 IC0 平面 */
uint8_t *s_back;
const uint8_t *s_front;

// 一次调用必须送完整块 plane：CS 保持期间的 DTM 被当成同一平面，中途插命令会截断。
// PSRAM 不能直接作 SPI DMA 源，所以逐块拷进内部 RAM。
esp_err_t send_plane(uint8_t cs, const uint8_t *plane)
{
    uint8_t *chunk = epdChunkBuffer();
    if (chunk == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }

    if (cs == EPD_CS_MAIN) {
        executeTscCommand();   /* 让主 IC 先做温度读取并选波形；读回值无意义 */
    }

    setPinCs(cs, GPIO_LOW);
    if (spiTransmitCommand(DTM) != DONE) {
        setPinCs(cs, GPIO_HIGH);
        return ESP_FAIL;
    }

    esp_err_t err = ESP_OK;
    for (size_t off = 0; off < EPD_FB_PLANE_BYTES; off += kChunkBytes) {
        const size_t n = (EPD_FB_PLANE_BYTES - off < kChunkBytes) ? (EPD_FB_PLANE_BYTES - off) : kChunkBytes;
        memcpy(chunk, plane + off, n);
        if (spiTransmitData(chunk, (unsigned long)n) != DONE) {
            ESP_LOGE(kTag, "plane%u 写入失败 @%u", cs, (unsigned)off);
            err = ESP_FAIL;
            break;
        }
    }
    setPinCs(cs, GPIO_HIGH);
    return err;
}

}  // namespace

esp_err_t epd_fb_init(void)
{
    if (s_frame[0] != nullptr) {
        return ESP_OK;
    }

    uint8_t *frame = static_cast<uint8_t *>(
        heap_caps_aligned_alloc(64, EPD_FB_FRAME_BYTES * 2, MALLOC_CAP_SPIRAM));
    if (frame == nullptr) {
        ESP_LOGE(kTag, "双缓冲分配失败：需要 %u B PSRAM", (unsigned)(EPD_FB_FRAME_BYTES * 2));
        return ESP_ERR_NO_MEM;
    }
    memset(frame, WHITE, EPD_FB_FRAME_BYTES * 2);

    s_frame[0] = frame;
    s_frame[1] = frame + EPD_FB_FRAME_BYTES;
    s_back = s_frame[0];
    s_front = s_frame[1];

    ESP_LOGI(kTag, "双缓冲就绪：2 x %u B = %u B（PSRAM）",
             (unsigned)EPD_FB_FRAME_BYTES, (unsigned)(EPD_FB_FRAME_BYTES * 2));
    return ESP_OK;
}

bool epd_fb_ready(void)
{
    return s_frame[0] != nullptr;
}

uint8_t *epd_fb_back(void)
{
    return s_back;
}

const uint8_t *epd_fb_front(void)
{
    return s_front;
}

void epd_fb_clear(uint8_t color)
{
    if (!epd_fb_ready()) {
        return;
    }
    /* epd709.h 的颜色码本身就是「高低半字节相同」（0x00/0x11/...），可直接 memset。 */
    memset(s_back, color, EPD_FB_FRAME_BYTES);
}

void epd_fb_draw_rows(const uint8_t *src)
{
    if (!epd_fb_ready()) {
        return;
    }
    constexpr size_t row_bytes = EPD_FB_WIDTH / 2;
    // 每行 600 字节：前 300 给左半幅 IC0，后 300 给右半幅 IC1，两平面相隔 EPD_FB_PLANE_BYTES。
    for (int row = 0; row < EPD_FB_HEIGHT; ++row) {
        memcpy(s_back + static_cast<size_t>(row) * kLineBytes, src, kLineBytes);
        memcpy(s_back + EPD_FB_PLANE_BYTES + static_cast<size_t>(row) * kLineBytes,
               src + kLineBytes, kLineBytes);
        src += row_bytes;
    }
}

void epd_fb_put_pixel(int x, int y, uint8_t color)
{
    if (!epd_fb_ready() || x < 0 || y < 0 || x >= EPD_FB_WIDTH || y >= EPD_FB_HEIGHT) {
        return;
    }

    const int ic = (x < EPD_FB_IC_WIDTH) ? 0 : 1;
    const int lx = x - ic * EPD_FB_IC_WIDTH;
    uint8_t *p = s_back + kPlaneOffset[ic] + static_cast<size_t>(y) * kLineBytes + lx / 2;

    const uint8_t nibble = color & 0x0F;
    if ((lx & 1) == 0) {
        *p = static_cast<uint8_t>((*p & 0x0F) | (nibble << 4));
    } else {
        *p = static_cast<uint8_t>((*p & 0xF0) | nibble);
    }
}

void epd_fb_fill_rect(int x, int y, int w, int h, uint8_t color)
{
    if (!epd_fb_ready() || w <= 0 || h <= 0) {
        return;
    }
    for (int row = y; row < y + h; ++row) {
        if (row < 0 || row >= EPD_FB_HEIGHT) {
            continue;
        }
        for (int col = x; col < x + w; ++col) {
            epd_fb_put_pixel(col, row, color);
        }
    }
}

esp_err_t epd_fb_present(void (*refresh_started)(void *), void *context)
{
    if (!epd_fb_ready()) {
        return ESP_ERR_INVALID_STATE;
    }

    if (send_plane(EPD_CS_MAIN, s_back + kPlaneOffset[0]) != ESP_OK) {
        return ESP_FAIL;
    }
    if (send_plane(EPD_CS_SUB, s_back + kPlaneOffset[1]) != ESP_OK) {
        return ESP_FAIL;
    }
    if (epdDisplayWithCallback(refresh_started, context) != DONE) {
        ESP_LOGE(kTag, "面板刷新失败");
        return ESP_FAIL;
    }

    // 全链路成功才交换。失败时 back 保留，避免丢掉没刷上去的画面。
    uint8_t *prev_back = s_back;
    s_back = const_cast<uint8_t *>(s_front);
    s_front = prev_back;
    return ESP_OK;
}
