#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

/** @brief 两种帧共享整幅 1200×1600、行优先 4bpp；尺寸和解压上限不可由输入扩张。 */
#define EPD_FRAME_WIDTH             1200u
#define EPD_FRAME_HEIGHT            1600u
#define EPD_FRAME_PIXEL_BYTES       (EPD_FRAME_WIDTH * EPD_FRAME_HEIGHT / 2u)
#define EPD_FRAME_RAW_HEADER_BYTES  12u
#define EPD_FRAME_ZLIB_HEADER_BYTES 16u
#define EPD_FRAME_MAX_BYTES         (EPD_FRAME_RAW_HEADER_BYTES + EPD_FRAME_PIXEL_BYTES)

/** @brief 已验证的帧头信息；frame_bytes 包含帧头，不含槽位末尾的空闲空间。 */
typedef struct {
    size_t header_bytes;
    size_t frame_bytes;
    bool compressed;
} epd_frame_info_t;

/**
 * @brief 验证 EPD1／EPD2 的位置、尺寸和长度上限，不读取或校验像素流。
 * @param header 至少含 EPD1 的 12 字节或 EPD2 的 16 字节帧头。
 * @param size header 中可读的字节数，不要求包含完整载荷。
 * @param info 成功时返回总长度和载荷类型，失败时不可使用。
 * @return ESP_OK 结构合法；ESP_ERR_NOT_FOUND 魔数不符；其余为参数或尺寸错误。
 */
esp_err_t epd_frame_parse_header(const uint8_t *header, size_t size, epd_frame_info_t *info);

/**
 * @brief 从帧起点的 offset 精确读取 size 字节；短读必须返回错误。
 * @note 数据源和 context 归调用者所有，只在 epd_frame_decode() 返回前使用。
 */
typedef esp_err_t (*epd_frame_read_fn)(void *context, size_t offset, uint8_t *data, size_t size);

/**
 * @brief 读取 EPD1，或将 EPD2 的标准 zlib 流还原为行优先 4bpp。
 * @param read 精确读取回调，不保存其指针或 context。
 * @param context 传给 read 的数据源。
 * @param source_bytes 数据源可读上限，可为文件长度或固定槽容量。
 * @param pixels 接收恰好 EPD_FRAME_PIXEL_BYTES 字节的行优先像素。
 * @param compressed 与 pixels 不重叠的同尺寸工作区，EPD2 用它暂存压缩载荷。
 * @return ESP_OK 已完整还原；其余为读取、长度、流或 Adler32 校验错误。
 * @note 两块缓冲由调用者借出，失败时内容不可使用；全部验证通过才可提交到显示缓冲。
 *       调用方须独占两块工作区，不与上传写 Flash 并发。
 *       复用已有双缓冲，不再分配整帧。解压状态按需在堆上分配，避免撑爆显示任务栈。
 */
esp_err_t epd_frame_decode(epd_frame_read_fn read, void *context, size_t source_bytes,
                           uint8_t *pixels, uint8_t *compressed);

#ifdef __cplusplus
}
#endif
