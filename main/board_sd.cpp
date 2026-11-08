#include "board_sd.h"

#include <dirent.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>

#include "board_pins.h"
#include "driver/gpio.h"
#include "driver/sdmmc_host.h"
#include "epd_fb.h"
#include "epd_frame.h"
#include "esp_log.h"
#include "esp_vfs_fat.h"
#include "sdmmc_cmd.h"

namespace {

constexpr const char *kTag = "sd";

constexpr int kMountMaxFiles = 5;
constexpr size_t kAllocationUnit = 16 * 1024;
// 4-bit 全速对走线敏感，先用 SDMMC_FREQ_DEFAULT（20 MHz），确认稳定后再提到 40 MHz。
constexpr int kMaxFreqKhz = SDMMC_FREQ_DEFAULT;

sdmmc_card_t *s_card = nullptr;

/** @brief 为共享解码器提供精确读取；FATFS 短读或定位失败都视为帧读取错误。 */
static esp_err_t read_frame_file(void *context, size_t offset, uint8_t *data, size_t size)
{
    FILE *file = static_cast<FILE *>(context);
    if (fseek(file, static_cast<long>(offset), SEEK_SET) != 0 ||
        fread(data, 1, size, file) != size) {
        return ESP_FAIL;
    }
    return ESP_OK;
}

}  // namespace

bool board_sd_present()
{
    return gpio_get_level(lumenfolio::board::kSdCardDetect) == 0;
}

bool board_sd_mounted()
{
    return s_card != nullptr;
}

esp_err_t board_sd_unmount()
{
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const esp_err_t err = esp_vfs_fat_sdcard_unmount(BOARD_SD_MOUNT_POINT, s_card);
    s_card = nullptr;
    return err;
}

esp_err_t board_sd_mount()
{
    if (s_card != nullptr) {
        return ESP_OK;
    }
    if (!board_sd_present()) {
        ESP_LOGW(kTag, "卡座里没有卡");
        return ESP_ERR_NOT_FOUND;
    }

    sdmmc_host_t host = SDMMC_HOST_DEFAULT();
    host.max_freq_khz = kMaxFreqKhz;

    sdmmc_slot_config_t slot = SDMMC_SLOT_CONFIG_DEFAULT();
    slot.width = 4;
    slot.clk   = lumenfolio::board::kSdClk;
    slot.cmd   = lumenfolio::board::kSdCmd;
    slot.d0    = lumenfolio::board::kSdD0;
    slot.d1    = lumenfolio::board::kSdD1;
    slot.d2    = lumenfolio::board::kSdD2;
    slot.d3    = lumenfolio::board::kSdD3;
    slot.cd    = SDMMC_SLOT_NO_CD;  // 卡检测由 board_sd_present() 自己读，不交给驱动
    slot.wp    = SDMMC_SLOT_NO_WP;
    slot.flags = 0;                 // 板上已有 47k 外部上拉

    esp_vfs_fat_mount_config_t mount = {};
    mount.format_if_mount_failed = false;  // 绝不替用户格式化
    mount.max_files              = kMountMaxFiles;
    mount.allocation_unit_size   = kAllocationUnit;

    const esp_err_t err = esp_vfs_fat_sdmmc_mount(BOARD_SD_MOUNT_POINT, &host, &slot, &mount, &s_card);
    if (err != ESP_OK) {
        s_card = nullptr;
        ESP_LOGE(kTag, "挂载失败：%s", esp_err_to_name(err));
        return err;
    }

    sdmmc_card_print_info(stdout, s_card);
    ESP_LOGI(kTag, "已挂载到 %s", BOARD_SD_MOUNT_POINT);
    return ESP_OK;
}

int board_sd_list(const char *path, int max_entries)
{
    DIR *dir = opendir(path);
    if (dir == nullptr) {
        ESP_LOGE(kTag, "%s 打不开", path);
        return -1;
    }

    int count = 0;
    struct dirent *entry = nullptr;
    while ((entry = readdir(dir)) != nullptr && count < max_entries) {
        const bool is_dir = entry->d_type == DT_DIR;
        long size = 0;
        if (!is_dir && entry->d_type == DT_REG) {
            char full[300];
            struct stat info = {};
            snprintf(full, sizeof(full), "%s/%s", path, entry->d_name);
            if (stat(full, &info) == 0) {
                size = static_cast<long>(info.st_size);
            }
        }
        ESP_LOGI(kTag, "  %s %-40s %ld", is_dir ? "DIR " : "FILE", entry->d_name, size);
        ++count;
    }
    closedir(dir);
    return count;
}

namespace {

constexpr int kMaxFrames = 32;
constexpr size_t kMaxFrameName = 96;

// 只有显示任务会调这两个函数，列表不需要加锁。
char s_frames[kMaxFrames][kMaxFrameName];

/**
 * @brief 扫根目录，把 .bin 文件名按字典序插入排序，返回个数。
 *
 * 插入排序而不是收完再排：目录项本来就不多，一遍下来直接有序。
 */
int collect_frames()
{
    DIR *dir = opendir(BOARD_SD_MOUNT_POINT);
    if (dir == nullptr) {
        return 0;
    }

    int count = 0;
    struct dirent *entry = nullptr;
    while ((entry = readdir(dir)) != nullptr) {
        const char *name = entry->d_name;
        const size_t len = strlen(name);
        if (len < 5 || len >= kMaxFrameName || strcasecmp(name + len - 4, ".bin") != 0) {
            continue;
        }
        if (count == kMaxFrames) {
            ESP_LOGW(kTag, "帧文件超过 %d 个，后面的忽略", kMaxFrames);
            break;
        }
        int at = count;
        while (at > 0 && strcasecmp(s_frames[at - 1], name) > 0) {
            memcpy(s_frames[at], s_frames[at - 1], kMaxFrameName);
            --at;
        }
        // 上面已按 kMaxFrameName 挡过长名字，这里按实际长度拷贝，不用 snprintf
        // 以免编译器对 255 字节的源缓冲报警。
        memcpy(s_frames[at], name, len + 1);
        ++count;
    }
    closedir(dir);
    return count;
}

}  // namespace

int board_sd_frame_count(void)
{
    if (s_card == nullptr) {
        return 0;
    }
    return collect_frames();
}

esp_err_t board_sd_frame_at(int index, char *path, size_t size)
{
    if (path == nullptr || size < 64) {
        return ESP_ERR_INVALID_ARG;
    }
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const int count = collect_frames();
    if (index < 0 || index >= count) {
        return ESP_ERR_NOT_FOUND;
    }
    const int written = snprintf(path, size, "%s/%s", BOARD_SD_MOUNT_POINT, s_frames[index]);
    if (written < 0 || static_cast<size_t>(written) >= size) {
        ESP_LOGE(kTag, "路径太长：%s/%s", BOARD_SD_MOUNT_POINT, s_frames[index]);
        return ESP_ERR_INVALID_SIZE;
    }
    return ESP_OK;
}

esp_err_t board_sd_load_frame(const char *path)
{
    if (path == nullptr) {
        return ESP_ERR_INVALID_ARG;
    }
    uint8_t *back = epd_fb_back();
    if (back == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }

    struct stat info = {};
    if (stat(path, &info) != 0) {
        ESP_LOGE(kTag, "%s 不存在", path);
        return ESP_ERR_NOT_FOUND;
    }

    FILE *file = fopen(path, "rb");
    if (file == nullptr) {
        ESP_LOGE(kTag, "%s 打不开", path);
        return ESP_ERR_NOT_FOUND;
    }

    uint8_t *scratch = const_cast<uint8_t *>(epd_fb_front());
    const esp_err_t result = epd_frame_decode(read_frame_file, file,
                                             static_cast<size_t>(info.st_size), scratch, back);
    if (result == ESP_OK) {
        epd_fb_draw_rows(scratch);
    } else {
        ESP_LOGE(kTag, "%s 帧解码失败：%s", path, esp_err_to_name(result));
    }
    fclose(file);
    return result;
}
