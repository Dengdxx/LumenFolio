#pragma once

#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

/** @brief 分区表里的系统资产分区名。升级固件不覆盖它，换图也不用重烧应用。 */
#define ARTIFACTS_PARTITION "artifacts"

/**
 * @brief 从 artifacts 分区读开机画面，画进帧缓冲的 back。
 * @return ESP_OK 已画上；ESP_ERR_NOT_FOUND 分区没有合法的帧，调用方应改刷白屏。
 * @note 帧格式为 "EPD1" + x,y,w,h（uint16 小端）+ 行优先 4bpp，必须是整幅
 *       1200×1600。只在刷屏任务里调用，且须先 epd_fb_init()。
 */
esp_err_t artifacts_draw_boot_frame(void);

#ifdef __cplusplus
}
#endif
