#include "artifacts.h"

#include <string.h>

#include "epd_fb.h"
#include "esp_log.h"
#include "esp_partition.h"

static const char *TAG = "artifacts";

#define FRAME_MAGIC_SIZE 4
#define FRAME_HEADER_SIZE 12

esp_err_t artifacts_draw_boot_frame(void)
{
    const esp_partition_t *part = esp_partition_find_first(
        ESP_PARTITION_TYPE_DATA, (esp_partition_subtype_t)0x40, ARTIFACTS_PARTITION);
    if (part == NULL) {
        ESP_LOGE(TAG, "没有 %s 分区", ARTIFACTS_PARTITION);
        return ESP_ERR_NOT_FOUND;
    }

    uint8_t header[FRAME_HEADER_SIZE];
    esp_err_t err = esp_partition_read(part, 0, header, sizeof(header));
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "读帧头失败：%s", esp_err_to_name(err));
        return err;
    }
    if (memcmp(header, "EPD1", FRAME_MAGIC_SIZE) != 0) {
        ESP_LOGW(TAG, "开机画面未写入，刷白屏");
        return ESP_ERR_NOT_FOUND;
    }

    const int x = header[4] | (header[5] << 8);
    const int y = header[6] | (header[7] << 8);
    const int w = header[8] | (header[9] << 8);
    const int h = header[10] | (header[11] << 8);
    const size_t pixels = FRAME_HEADER_SIZE + (size_t)h * (w / 2);
    if (x != 0 || y != 0 || w != EPD_FB_WIDTH || h != EPD_FB_HEIGHT || pixels > part->size) {
        ESP_LOGE(TAG, "开机画面尺寸 %dx%d @(%d,%d) 共 %u B，与 %dx%d 不符",
                 w, h, x, y, (unsigned)pixels, EPD_FB_WIDTH, EPD_FB_HEIGHT);
        return ESP_ERR_INVALID_SIZE;
    }

    uint8_t *back = epd_fb_back();
    if (back == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    // 两块缓冲共用一次分配，back 之后紧挨着另一帧，借那一块暂存行优先的数据。
    uint8_t *scratch = (back == epd_fb_front()) ? back + EPD_FB_FRAME_BYTES
                                                : (uint8_t *)epd_fb_front();
    err = esp_partition_read(part, FRAME_HEADER_SIZE, scratch, pixels - FRAME_HEADER_SIZE);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "读开机画面失败：%s", esp_err_to_name(err));
        return err;
    }
    epd_fb_draw_rows(scratch);
    return ESP_OK;
}
