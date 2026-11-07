#include "artifacts.h"

#include "epd_fb.h"
#include "esp_log.h"
#include "esp_partition.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

static const char *TAG = "artifacts";
static StaticSemaphore_t s_mutex_storage;
static SemaphoreHandle_t s_mutex;
static portMUX_TYPE s_mutex_init = portMUX_INITIALIZER_UNLOCKED;

/** @brief 并发首次调用时只创建一次静态互斥量，初始化不依赖堆分配。 */
static SemaphoreHandle_t artifacts_mutex(void)
{
    portENTER_CRITICAL(&s_mutex_init);
    if (s_mutex == NULL) {
        s_mutex = xSemaphoreCreateMutexStatic(&s_mutex_storage);
    }
    portEXIT_CRITICAL(&s_mutex_init);
    return s_mutex;
}

static const esp_partition_t *artifacts_partition(void)
{
    const esp_partition_t *part = esp_partition_find_first(
        ESP_PARTITION_TYPE_DATA, (esp_partition_subtype_t)0x40, ARTIFACTS_PARTITION);
    if (part == NULL) {
        ESP_LOGE(TAG, "没有 %s 分区", ARTIFACTS_PARTITION);
    }
    return part;
}

typedef struct {
    const esp_partition_t *part;
    size_t base;
} partition_reader_t;

/** @brief 将帧内偏移映射到固定 Flash 槽，读取上限由共享解码器检查。 */
static esp_err_t read_frame(void *context, size_t offset, uint8_t *data, size_t size)
{
    const partition_reader_t *reader = context;
    return esp_partition_read(reader->part, reader->base + offset, data, size);
}

/**
 * @brief 从固定槽读取 EPD1／EPD2，完整验证后画进 back。
 * @note 只在显示任务里调用。front 暂存行优先像素，back 暂存压缩输入；失败时两者不可上屏。
 *       帧头与全部载荷在同一读锁内读取，避免下一次覆盖擦除与本次读取交错。
 */
static esp_err_t draw_frame(size_t offset, const char *what)
{
    const esp_partition_t *part = artifacts_partition();
    if (part == NULL) {
        return ESP_ERR_NOT_FOUND;
    }
    if (offset > part->size || ARTIFACTS_SLOT_BYTES > part->size - offset) {
        return ESP_ERR_INVALID_SIZE;
    }
    uint8_t *back = epd_fb_back();
    if (back == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    uint8_t *scratch = (uint8_t *)epd_fb_front();
    partition_reader_t reader = {.part = part, .base = offset};
    SemaphoreHandle_t mutex = artifacts_mutex();
    xSemaphoreTake(mutex, portMAX_DELAY);
    const esp_err_t err = epd_frame_decode(read_frame, &reader, ARTIFACTS_SLOT_BYTES,
                                          scratch, back);
    xSemaphoreGive(mutex);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "读取%s失败：%s", what, esp_err_to_name(err));
        return err;
    }
    epd_fb_draw_rows(scratch);
    return ESP_OK;
}

esp_err_t artifacts_draw_boot_frame(void)
{
    return draw_frame(ARTIFACTS_BOOT_OFFSET, "开机画面");
}

esp_err_t artifacts_draw_inbox_frame(void)
{
    return draw_frame(ARTIFACTS_INBOX_OFFSET, "上传的画面");
}

esp_err_t artifacts_inbox_begin(void)
{
    const esp_partition_t *part = artifacts_partition();
    if (part == NULL) {
        return ESP_ERR_NOT_FOUND;
    }
    if (ARTIFACTS_INBOX_OFFSET > part->size ||
        ARTIFACTS_SLOT_BYTES > part->size - ARTIFACTS_INBOX_OFFSET) {
        return ESP_ERR_INVALID_SIZE;
    }
    // 槽位固定为 1 MB，压缩和扩大资产分区都不改变现有图片的槽内位置。
    SemaphoreHandle_t mutex = artifacts_mutex();
    xSemaphoreTake(mutex, portMAX_DELAY);
    const esp_err_t err = esp_partition_erase_range(part, ARTIFACTS_INBOX_OFFSET, ARTIFACTS_SLOT_BYTES);
    xSemaphoreGive(mutex);
    return err;
}

esp_err_t artifacts_inbox_write(size_t offset, const uint8_t *data, size_t size)
{
    if (data == NULL || size == 0 || offset > ARTIFACTS_FRAME_BYTES ||
        size > ARTIFACTS_FRAME_BYTES - offset) {
        return ESP_ERR_INVALID_SIZE;
    }
    const esp_partition_t *part = artifacts_partition();
    if (part == NULL) {
        return ESP_ERR_NOT_FOUND;
    }
    SemaphoreHandle_t mutex = artifacts_mutex();
    xSemaphoreTake(mutex, portMAX_DELAY);
    const esp_err_t err = esp_partition_write(part, ARTIFACTS_INBOX_OFFSET + offset, data, size);
    xSemaphoreGive(mutex);
    return err;
}

esp_err_t artifacts_inbox_discard(void)
{
    return artifacts_inbox_begin();
}
