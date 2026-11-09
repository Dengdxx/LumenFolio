#pragma once

#include <stddef.h>

#include "esp_err.h"

/**
 * @file board_sd.h
 * @brief TF 卡：SDMMC 4-bit 加 FATFS 挂载。
 *
 * 引脚见 board_pins.h：CLK 7、CMD 15、D0–D3 6/5/17/16，卡检测 CD 4（插卡接地，
 * 内部上拉由 board_gpio_init() 打开）。数据线已有 47k 外部上拉，不再开内部上拉。
 *
 * 总线先跑 20 MHz 默认档：4-bit 全速对走线和卡的质量都敏感，确认稳定后再提频。
 *
 * 不支持带电拔插：挂载期间拔卡会让后续读写返回 I/O 错误，插回需要重新挂载。
 * 卡检测只在挂载前判断一次，别在别的任务里轮询后直接停用文件系统。
 */

/** @brief 挂载点。 */
#define BOARD_SD_MOUNT_POINT "/sdcard"

/** @brief 卡座里是否插着卡（读 CD 引脚，插卡为低）。 */
bool board_sd_present();

/** @brief 文件系统是否已经挂上。 */
bool board_sd_mounted();

/**
 * @brief 挂载 FATFS 并打印卡信息。
 *
 * @return ESP_OK 成功；ESP_ERR_NOT_FOUND 没插卡；其余为 SDMMC 或 FATFS 的错误。
 *
 * @note 重复调用返回 ESP_OK。
 * @note 不格式化：format_if_mount_failed 固定为 false，卡上文件系统坏了就报错，
 *       不能替用户清空一张装了照片的卡。
 */
esp_err_t board_sd_mount();

/**
 * @brief 卸载文件系统并让卡回到空闲态。
 *
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 当前没有挂载；其余为底层错误。
 *
 * @note 进入深度睡眠前调用：让 FAT 元数据落盘、卡退出传输态，睡眠期间不会留下
 *       半挂载的文件系统。调用之后 board_sd_mounted() 变为 false。
 */
esp_err_t board_sd_unmount();

/**
 * @brief 打印目录内容，用于上板确认挂载结果。
 *
 * @param path        目录，通常是 BOARD_SD_MOUNT_POINT。
 * @param max_entries 最多打印多少条。
 * @return 实际条目数；目录打不开返回 -1。
 *
 * @note 只读，不修改卡上任何内容。
 */
int board_sd_list(const char *path, int max_entries);

/**
 * @brief 根目录里 .bin 帧文件的个数（按文件名排序，不区分大小写）。
 *
 * @return 个数；未挂载或卡上没有帧文件返回 0。
 *
 * @note 每次调用都重新扫目录，卡上增删文件后不需要额外通知。
 * @note 最多认 32 个，超出的只记一条警告；不递归子目录。
 */
int board_sd_frame_count(void);

/**
 * @brief 取排序后第 index 个帧文件的完整路径。
 *
 * @param[in]  index 从 0 开始，序号来自 board_sd_frame_count() 的同一套排序。
 * @param[out] path  输出完整路径，形如 `/sdcard/frame1.bin`。
 * @param[in]  size  缓冲长度，至少 64 字节。
 * @return ESP_OK 找到；ESP_ERR_NOT_FOUND 序号越界；ESP_ERR_INVALID_STATE 未挂载。
 *
 * @note 文件名超过 95 字节的会被跳过（FATFS 最长 255，但这里按 96 截）。
 */
esp_err_t board_sd_frame_at(int index, char *path, size_t size);

/**
 * @brief 读一个 EPD1 帧文件，画进 back buffer。
 *
 * @param path board_sd_first_frame() 给出的完整路径。
 * @return ESP_OK 成功；ESP_ERR_INVALID_SIZE 尺寸不是 1200x1600 或文件不足一帧；
 *         ESP_ERR_NOT_FOUND 文件不存在或魔数不对；ESP_ERR_INVALID_STATE 帧缓冲未就绪。
 *
 * @note 只画不刷：调用方拿到 ESP_OK 后再 epd_fb_present()。
 * @note 需要 epd_fb_init() 已完成，且中途不要有别的任务碰帧缓冲。
 * @note 借用另一块帧缓冲暂存行优先数据，画完它的内容就不再是上一次上屏的画面。
 */
esp_err_t board_sd_load_frame(const char *path);
