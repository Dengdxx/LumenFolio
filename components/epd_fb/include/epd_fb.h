#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "esp_err.h"
#include "epd709.h"

#ifdef __cplusplus
extern "C" {
#endif

/*
 * 双缓冲：两块整帧 PSRAM 缓冲 + 交换刷屏。
 *
 * 缓冲布局（与 NT61522 双 IC 的对齐方式一致，便于整块直发）：
 *
 *   plane0 [IC0 / CSB_M]  600 x 1600 px @4bpp = 480,000 B
 *   plane1 [IC1 / CSB_S]  600 x 1600 px @4bpp = 480,000 B
 *   合计一帧 960,000 B，两块缓冲共 1,920,000 B（需 PSRAM）
 *
 * 像素：4bpp，1 字节 2 像素。**偶数 x 占高半字节，奇数 x 占低半字节**——
 * 该顺序目前按常见约定假设，上板用棋盘格确认；若反了，只需改 epd_fb_put_pixel()。
 *
 * 缓冲在 PSRAM 上，而 SPI DMA 不能直接读 PSRAM：刷屏时经 32 KB 内部 RAM 分块搬运。
 * 所有函数都不线程安全，只在同一个任务里用。
 */

/** @brief 整帧 1200×1600，按两颗 IC 各 600 列切开。 */
#define EPD_FB_WIDTH        1200
#define EPD_FB_HEIGHT       1600
#define EPD_FB_IC_WIDTH     600
/** @brief 单颗 IC 的 DTM 长度，必须等于 EPD_IMAGE_BYTES_PER_IC。 */
#define EPD_FB_PLANE_BYTES  (EPD_FB_IC_WIDTH / 2 * EPD_FB_HEIGHT) /* 480000 */
#define EPD_FB_FRAME_BYTES  (EPD_FB_PLANE_BYTES * 2)              /* 960000 */

/**
 * @brief 一次分配两块整帧并清成白色。
 * @return 重复调用返回 ESP_OK；PSRAM 不足返回 ESP_ERR_NO_MEM。
 * @note 失败后绘图调用静默丢弃，刷屏返回错误。
 */
esp_err_t epd_fb_init(void);

/** @brief PSRAM 分配失败后恒为 false。 */
bool epd_fb_ready(void);

/** @brief 当前可写的那一帧。指针只在 epd_fb_present() 成功时改变，调用方不得释放。 */
uint8_t *epd_fb_back(void);

/**
 * @brief 最近一次成功上屏的那一帧。未就绪时为 nullptr。
 * @note init 之后两帧都是白色，并不代表面板当前的画面。
 */
const uint8_t *epd_fb_front(void);

/** @brief 把 back 整帧填成单一颜色。color 的高低半字节须相同。未就绪时直接返回。 */
void epd_fb_clear(uint8_t color);

/**
 * @brief 把一帧行优先的 4bpp 数据画进 back。
 * @param src 每行 EPD_FB_WIDTH/2 字节，共 EPD_FB_HEIGHT 行，偶数 x 在高半字节。
 * @note 每行前 300 字节进左半幅，后 300 字节进右半幅。未就绪时直接返回。
 */
void epd_fb_draw_rows(const uint8_t *src);

/**
 * @brief 写一个像素。x 0..599 属左 IC，600..1199 属右 IC。
 * @note 越界忽略。半字节顺序见文件头。
 */
void epd_fb_put_pixel(int x, int y, uint8_t color);

/** @brief 填一个矩形，超出帧的部分裁掉。w 或 h 不大于 0 时忽略。 */
void epd_fb_fill_rect(int x, int y, int w, int h, uint8_t color);

/**
 * @brief 把 back 刷上屏，成功后与 front 交换。
 * @return 未就绪为 ESP_ERR_INVALID_STATE；SPI 或面板时序失败为 ESP_FAIL。
 * @note 阻塞，DRF 最长等 60 s。失败不交换，面板上可能已是半帧。
 */
esp_err_t epd_fb_present(void);

#ifdef __cplusplus
}
#endif
