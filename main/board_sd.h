#pragma once

#include <stddef.h>
#include <stdint.h>

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

/** @brief SD 帧列表的单页上限和文件名缓冲上限。 */
inline constexpr int BOARD_SD_MAX_FRAMES = 32;
inline constexpr size_t BOARD_SD_FRAME_NAME_BYTES = 96;
using board_sd_frame_names = char[BOARD_SD_MAX_FRAMES][BOARD_SD_FRAME_NAME_BYTES];

/** @brief 单页帧元数据，给 App 区分同名文件的缓存版本。 */
struct board_sd_frame_revision {
    size_t bytes;
    int64_t modified;
};

/** @brief 由 HTTP 任务持有的一页目录快照，卡标识与文件元数据在同一次查询中取出。 */
struct board_sd_frame_page {
    board_sd_frame_names names;
    board_sd_frame_revision revisions[BOARD_SD_MAX_FRAMES];
    uint32_t card_manufacturer;
    uint32_t card_serial;
    int count;
    bool more;
};

/** @brief 卡座里是否插着卡（读 CD 引脚，插卡为低）。 */
bool board_sd_present();

/** @brief 文件系统是否已经挂上。 */
bool board_sd_mounted();

/** @brief 读取已挂载文件系统的已用与总字节数；未挂载返回 ESP_ERR_INVALID_STATE。 */
esp_err_t board_sd_storage(uint64_t &used, uint64_t &total);

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
 * @note 只统计根目录文件，不递归子目录。
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

/** @brief 查找指定文件在翻页顺序中的序号，未找到返回 -1。 */
int board_sd_frame_index(const char *name);

/**
 * @brief 查询根目录帧文件名，按翻页顺序跳过坏帧头或截断的文件。
 * @return ESP_OK 查询成功；ESP_ERR_INVALID_STATE 未挂载；ESP_FAIL 目录读取失败。
 * @note 只检查帧头和声明长度，压缩流由预览端解码时验证。
 */
esp_err_t board_sd_browse_frames(board_sd_frame_names &names, int &count);

/**
 * @brief 按文件名顺序查询一页合法帧，跳过坏帧头或截断文件。
 * @param after 排他游标，空字符串表示从头查询。
 * @param limit 返回条数上限，为 1 至 BOARD_SD_MAX_FRAMES。
 * @param[out] page 查询结果，more 为真时用最后一个文件名作为下一页游标。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 未挂载；ESP_ERR_INVALID_ARG 游标或条数非法。
 */
esp_err_t board_sd_browse_page(const char *after, int limit, board_sd_frame_page &page);

/**
 * @brief 打开用于 HTTP 预览的单个根目录帧，返回应发送的编码长度。
 * @return ESP_OK 成功；ESP_ERR_INVALID_ARG 文件名非法；ESP_ERR_NOT_FOUND 帧不可读取。
 * @note 浏览接口由 HTTP 任务独占，未读完或断开连接也要调用 close。
 *       若设备开始卸载，卸载方会关闭该文件，此后 read 返回错误。
 */
esp_err_t board_sd_browse_open(const char *name, size_t &size);

/**
 * @brief 在 SD 锁内校验根目录帧并删除原文件，不触发刷屏。
 * @return ESP_OK 成功；ESP_ERR_INVALID_ARG 名称非法；ESP_ERR_INVALID_STATE 未挂载；
 *         ESP_ERR_NOT_FOUND 文件不存在；ESP_ERR_INVALID_RESPONSE 帧头非法；ESP_FAIL 存储错误。
 */
esp_err_t board_sd_delete_frame(const char *name);

/** @brief 从当前浏览文件顺序读取一块。调用方按 open 返回的总长分块读取。 */
esp_err_t board_sd_browse_read(uint8_t *data, size_t size);

/** @brief 释放浏览文件，已由卸载方关闭时可安全调用。 */
void board_sd_browse_close();

/**
 * @brief 独立打开根目录帧并解码到调用方工作区，避免与浏览文件共享读位置。
 * @note 两块工作区均至少为 EPD_FRAME_PIXEL_BYTES 字节；读取期间持有 SD 锁。
 */
esp_err_t board_sd_decode_frame(const char *name, uint8_t *pixels, uint8_t *compressed);

/**
 * @brief 读取 EPD1 原始帧或 EPD2 压缩帧，完整验证后画进 back buffer。
 *
 * @param path board_sd_frame_at() 给出的完整路径。
 * @return ESP_OK 成功；ESP_ERR_INVALID_SIZE 尺寸不是 1200x1600 或文件不足一帧；
 *         ESP_ERR_NOT_FOUND 文件不存在或魔数不对；其余为读取、解压、校验或缓冲错误。
 *
 * @note 只画不刷：调用方拿到 ESP_OK 后再 epd_fb_present()。
 * @note 需要 epd_fb_init() 已完成，且中途不要有别的任务碰帧缓冲。
 * @note 借用另一块帧缓冲暂存行优先数据，画完它的内容就不再是上一次上屏的画面。
 */
esp_err_t board_sd_load_frame(const char *path);
