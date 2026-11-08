#pragma once

#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"
#include "epd_frame.h"

#ifdef __cplusplus
extern "C" {
#endif

/** @brief 分区表里的系统资产分区名，应用 OTA 不覆盖此分区。 */
#define ARTIFACTS_PARTITION "artifacts"

/**
 * @brief 开机画面与上传帧各占固定 1 MB 槽，接受整幅 EPD1 或 EPD2。
 * @note 帧的编码上限是 960012 字节；压缩和扩大分区都不改变这两个槽的偏移。
 */
#define ARTIFACTS_BOOT_OFFSET   0u
#define ARTIFACTS_INBOX_OFFSET  0x100000u
#define ARTIFACTS_FRAME_BYTES   EPD_FRAME_MAX_BYTES
#define ARTIFACTS_SLOT_BYTES    0x100000u

/**
 * @brief 从资产分区读取开机画面，完整解码验证后画入 back。
 * @return ESP_OK 成功；ESP_ERR_NOT_FOUND 没有合法帧；其余为读取、解压或缓冲错误。
 * @note 仅由显示任务调用，须先 epd_fb_init()；借用已有两块帧缓冲，只画不刷。
 */
esp_err_t artifacts_draw_boot_frame(void);

/** @brief 从上传槽读取并验证 EPD1／EPD2；所有权和调用约束与开机画面相同。 */
esp_err_t artifacts_draw_inbox_frame(void);

/**
 * @brief 从指定槽的帧头读取编码长度，空槽或坏帧头返回 ESP_ERR_NOT_FOUND。
 * @param inbox true 选上传槽，false 选开机画面槽。
 * @param[out] size 帧头声明的整帧字节数，不含槽尾空间。
 */
esp_err_t artifacts_frame_size(bool inbox, size_t *size);

/**
 * @brief 读取指定槽的一段编码数据，不占用显示缓冲。
 * @note 调用方须先读取长度并以此限制偏移与长度。读取和上传都在同一个 HTTP
 *       任务内串行执行，才能保证多段读取之间槽位不被覆盖。
 */
esp_err_t artifacts_frame_read(bool inbox, size_t offset, uint8_t *data, size_t size);

/**
 * @brief 擦除上传槽，准备按偏移流式写入；不触碰开机画面。
 * @return ESP_OK 成功；其余为分区或擦除错误。
 * @note 接收块使用内部 RAM；读取、擦除与写入在组件内互斥，避免读取到覆盖中的帧。
 */
esp_err_t artifacts_inbox_begin(void);

/**
 * @brief 将一个接收块写入上传槽，检查偏移与长度不超过编码上限。
 * @param offset 相对帧起点的偏移。
 * @param data 本块输入，仅在调用期间借用。
 * @param size 本块字节数，不可为零。
 * @return ESP_OK 成功；其余为越界、分区或写入错误。
 * @note 必须先 begin；调用方验证帧头声明与实际总长，先写载荷和元数据，最后单独写魔数。
 */
esp_err_t artifacts_inbox_write(size_t offset, const uint8_t *data, size_t size);

/** @brief 写入中断时擦除上传槽，避免留下半帧；不触碰开机画面。 */
esp_err_t artifacts_inbox_discard(void);

#ifdef __cplusplus
}
#endif
