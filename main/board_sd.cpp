#include "board_sd.h"

#include <atomic>
#include <dirent.h>
#include <errno.h>
#include <mutex>
#include <stdio.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <unistd.h>

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

std::atomic<sdmmc_card_t *> s_card = nullptr;
// 只在访问卡时持锁，避免深睡卸载与读卡交错，网络发送在锁外进行。
std::mutex s_sd_mutex;
FILE *s_browse_file = nullptr;

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

esp_err_t board_sd_storage(uint64_t &used, uint64_t &total)
{
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    uint64_t free_bytes;
    const esp_err_t err = esp_vfs_fat_info(BOARD_SD_MOUNT_POINT, &total, &free_bytes);
    if (err == ESP_OK) {
        used = total - free_bytes;
    }
    return err;
}

esp_err_t board_sd_unmount()
{
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    if (s_browse_file != nullptr) {
        fclose(s_browse_file);
        s_browse_file = nullptr;
    }
    const esp_err_t err = esp_vfs_fat_sdcard_unmount(BOARD_SD_MOUNT_POINT, s_card);
    s_card = nullptr;
    return err;
}

esp_err_t board_sd_mount()
{
    std::lock_guard lock(s_sd_mutex);
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

    sdmmc_card_t *card = nullptr;
    const esp_err_t err = esp_vfs_fat_sdmmc_mount(BOARD_SD_MOUNT_POINT, &host, &slot, &mount, &card);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "挂载失败：%s", esp_err_to_name(err));
        return err;
    }

    s_card = card;
    sdmmc_card_print_info(stdout, card);
    ESP_LOGI(kTag, "已挂载到 %s", BOARD_SD_MOUNT_POINT);
    return ESP_OK;
}

int board_sd_list(const char *path, int max_entries)
{
    std::lock_guard lock(s_sd_mutex);
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

board_sd_frame_names s_display_frames;

bool is_frame_name(const char *name)
{
    const size_t len = strlen(name);
    return len >= 5 && len < BOARD_SD_FRAME_NAME_BYTES &&
           strpbrk(name, "/\\:") == nullptr && strcasecmp(name + len - 4, ".bin") == 0;
}

bool frame_entry(const struct dirent *entry)
{
    return entry->d_type == DT_REG && is_frame_name(entry->d_name);
}

/** @brief name 为空指针时统计全目录，否则返回该名称在翻页顺序中的序号。 */
int count_or_find_frame(const char *name)
{
    DIR *dir = opendir(BOARD_SD_MOUNT_POINT);
    if (dir == nullptr) {
        return -1;
    }
    int count = 0;
    bool found = name == nullptr;
    struct dirent *entry = nullptr;
    while ((entry = readdir(dir)) != nullptr) {
        if (frame_entry(entry)) {
            const int order = name == nullptr ? -1 : strcasecmp(entry->d_name, name);
            if (order < 0) ++count;
            if (order == 0) found = true;
        }
    }
    closedir(dir);
    return found ? count : -1;
}

bool name_before(const char *a, const char *b, bool reverse)
{
    return reverse ? strcasecmp(a, b) > 0 : strcasecmp(a, b) < 0;
}

/** @brief 全目录扫描只保留游标之后最近的一批文件名，调用者持有 SD 锁。 */
int collect_after(const char *after, int limit, bool reverse = false)
{
    DIR *dir = opendir(BOARD_SD_MOUNT_POINT);
    if (dir == nullptr) {
        return -1;
    }
    int count = 0;
    struct dirent *entry = nullptr;
    while ((entry = readdir(dir)) != nullptr) {
        const char *name = entry->d_name;
        if (!frame_entry(entry) || (*after && !name_before(after, name, reverse))) {
            continue;
        }
        if (count == limit && !name_before(name, s_display_frames[count - 1], reverse)) {
            continue;
        }
        int at = count < limit ? count++ : count - 1;
        while (at > 0 && name_before(name, s_display_frames[at - 1], reverse)) {
            strcpy(s_display_frames[at], s_display_frames[at - 1]);
            --at;
        }
        strcpy(s_display_frames[at], name);
    }
    closedir(dir);
    return count;
}

/**
 * @brief 扫根目录，把 .bin 文件名按字典序插入排序，返回个数。
 *
 * 插入排序而不是收完再排：目录项本来就不多，一遍下来直接有序。
 */
int collect_frames(board_sd_frame_names &names)
{
    DIR *dir = opendir(BOARD_SD_MOUNT_POINT);
    if (dir == nullptr) {
        return -1;
    }

    int count = 0;
    struct dirent *entry = nullptr;
    while ((entry = readdir(dir)) != nullptr) {
        const char *name = entry->d_name;
        if (!is_frame_name(name)) {
            continue;
        }
        if (count == BOARD_SD_MAX_FRAMES) {
            ESP_LOGW(kTag, "帧文件超过 %d 个，后面的忽略", BOARD_SD_MAX_FRAMES);
            break;
        }
        int at = count;
        while (at > 0 && strcasecmp(names[at - 1], name) > 0) {
            memcpy(names[at], names[at - 1], BOARD_SD_FRAME_NAME_BYTES);
            --at;
        }
        strcpy(names[at], name);
        ++count;
    }
    closedir(dir);
    return count;
}

/**
 * @brief 打开合法帧并定位到帧头，额外的文件尾字节不计入编码长度。
 * @note 调用者持有 SD 锁并负责关闭。只检查帧头，不解码像素。
 */
esp_err_t open_browse_path(const char *path, FILE *&file, size_t &size, board_sd_frame_revision *revision = nullptr)
{
    struct stat stat_info = {};
    if (stat(path, &stat_info) != 0) {
        return errno == ENOENT ? ESP_ERR_NOT_FOUND : ESP_FAIL;
    }
    if (!S_ISREG(stat_info.st_mode)) {
        return ESP_ERR_NOT_FOUND;
    }
    if (stat_info.st_size < EPD_FRAME_ZLIB_HEADER_BYTES) {
        return ESP_ERR_INVALID_RESPONSE;
    }
    FILE *opened = fopen(path, "rb");
    if (opened == nullptr) {
        return errno == ENOENT ? ESP_ERR_NOT_FOUND : ESP_FAIL;
    }
    uint8_t header[EPD_FRAME_ZLIB_HEADER_BYTES];
    epd_frame_info_t info = {};
    esp_err_t err = ESP_OK;
    if (fread(header, 1, sizeof(header), opened) != sizeof(header)) {
        err = ferror(opened) ? ESP_FAIL : ESP_ERR_INVALID_RESPONSE;
    } else if (epd_frame_parse_header(header, sizeof(header), &info) != ESP_OK ||
               info.frame_bytes > static_cast<size_t>(stat_info.st_size)) {
        err = ESP_ERR_INVALID_RESPONSE;
    } else if (fseek(opened, 0, SEEK_SET) != 0) {
        err = ESP_FAIL;
    }
    if (err != ESP_OK) {
        fclose(opened);
        return err;
    }
    file = opened;
    size = info.frame_bytes;
    if (revision != nullptr) {
        revision->bytes = static_cast<size_t>(stat_info.st_size);
        revision->modified = stat_info.st_mtime;
    }
    return ESP_OK;
}

FILE *open_browse_frame(const char *name, size_t &size, board_sd_frame_revision *revision = nullptr)
{
    char path[sizeof(BOARD_SD_MOUNT_POINT) + BOARD_SD_FRAME_NAME_BYTES];
    snprintf(path, sizeof(path), "%s/%s", BOARD_SD_MOUNT_POINT, name);
    FILE *file = nullptr;
    return open_browse_path(path, file, size, revision) == ESP_OK ? file : nullptr;
}

}  // namespace

int board_sd_frame_count(void)
{
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return 0;
    }
    const int count = count_or_find_frame(nullptr);
    return count < 0 ? 0 : count;
}

esp_err_t board_sd_frame_at(int index, char *path, size_t size)
{
    if (path == nullptr || size < 64) {
        return ESP_ERR_INVALID_ARG;
    }
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const int count = count_or_find_frame(nullptr);
    if (index < 0 || index >= count) {
        return ESP_ERR_NOT_FOUND;
    }
    // 从较近的一端分批找序号，内存占用与目录大小无关。
    const bool reverse = index > count / 2;
    if (reverse) index = count - 1 - index;
    char cursor[BOARD_SD_FRAME_NAME_BYTES] = {};
    for (;;) {
        const int batch = collect_after(cursor, BOARD_SD_MAX_FRAMES, reverse);
        if (batch <= 0) return ESP_ERR_NOT_FOUND;
        if (index < batch) {
            const int written = snprintf(path, size, "%s/%s", BOARD_SD_MOUNT_POINT, s_display_frames[index]);
            return written < 0 || static_cast<size_t>(written) >= size ? ESP_ERR_INVALID_SIZE : ESP_OK;
        }
        index -= batch;
        strcpy(cursor, s_display_frames[batch - 1]);
    }
}

int board_sd_frame_index(const char *name)
{
    std::lock_guard lock(s_sd_mutex);
    return s_card == nullptr ? -1 : count_or_find_frame(name);
}

esp_err_t board_sd_browse_frames(board_sd_frame_names &names, int &count)
{
    std::lock_guard lock(s_sd_mutex);
    count = 0;
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const int entries = collect_frames(names);
    if (entries < 0) {
        return ESP_FAIL;
    }
    for (int i = 0; i < entries; ++i) {
        size_t size;
        FILE *file = open_browse_frame(names[i], size);
        if (file != nullptr) {
            fclose(file);
            if (count != i) {
                strcpy(names[count], names[i]);
            }
            ++count;
        }
    }
    return ESP_OK;
}

esp_err_t board_sd_browse_page(const char *after, int limit, board_sd_frame_page &page)
{
    page.count = 0;
    page.more = false;
    if (limit < 1 || limit > BOARD_SD_MAX_FRAMES || (*after && !is_frame_name(after))) {
        return ESP_ERR_INVALID_ARG;
    }
    std::lock_guard lock(s_sd_mutex);
    const auto *card = s_card.load();
    if (card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    page.card_manufacturer = static_cast<uint32_t>(card->cid.mfg_id);
    page.card_serial = static_cast<uint32_t>(card->cid.serial);

    char cursor[BOARD_SD_FRAME_NAME_BYTES];
    strcpy(cursor, after);
    for (;;) {
        // 批量找候选，坏帧密集时也只按批重扫目录。校验到一个后继即返回。
        const int candidates = collect_after(cursor, BOARD_SD_MAX_FRAMES);
        if (candidates < 0) return ESP_FAIL;
        for (int i = 0; i < candidates; ++i) {
            size_t size;
            board_sd_frame_revision revision;
            FILE *file = open_browse_frame(s_display_frames[i], size, &revision);
            if (file == nullptr) continue;
            fclose(file);
            if (page.count == limit) {
                page.more = true;
                return ESP_OK;
            }
            strcpy(page.names[page.count], s_display_frames[i]);
            page.revisions[page.count++] = revision;
        }
        if (candidates < BOARD_SD_MAX_FRAMES) return ESP_OK;
        strcpy(cursor, s_display_frames[candidates - 1]);
    }
}

esp_err_t board_sd_browse_open(const char *name, size_t &size)
{
    if (!is_frame_name(name)) {
        return ESP_ERR_INVALID_ARG;
    }
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_NOT_FOUND;
    }
    s_browse_file = open_browse_frame(name, size);
    return s_browse_file != nullptr ? ESP_OK : ESP_ERR_NOT_FOUND;
}

esp_err_t board_sd_delete_frame(const char *name)
{
    if (!is_frame_name(name)) {
        return ESP_ERR_INVALID_ARG;
    }
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    char path[sizeof(BOARD_SD_MOUNT_POINT) + BOARD_SD_FRAME_NAME_BYTES];
    snprintf(path, sizeof(path), "%s/%s", BOARD_SD_MOUNT_POINT, name);
    FILE *file = nullptr;
    size_t size;
    const esp_err_t err = open_browse_path(path, file, size);
    if (err != ESP_OK) {
        return err;
    }
    fclose(file);
    if (unlink(path) != 0) {
        return errno == ENOENT ? ESP_ERR_NOT_FOUND : ESP_FAIL;
    }
    return ESP_OK;
}

esp_err_t board_sd_browse_read(uint8_t *data, size_t size)
{
    std::lock_guard lock(s_sd_mutex);
    if (s_browse_file == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    return fread(data, 1, size, s_browse_file) == size ? ESP_OK : ESP_FAIL;
}

esp_err_t board_sd_decode_frame(const char *name, uint8_t *pixels, uint8_t *compressed)
{
    if (!is_frame_name(name)) {
        return ESP_ERR_INVALID_ARG;
    }
    std::lock_guard lock(s_sd_mutex);
    if (s_card == nullptr) {
        return ESP_ERR_NOT_FOUND;
    }
    size_t size;
    FILE *file = open_browse_frame(name, size);
    if (file == nullptr) {
        return ESP_ERR_NOT_FOUND;
    }
    const esp_err_t err = epd_frame_decode(read_frame_file, file, size, pixels, compressed);
    fclose(file);
    return err;
}

void board_sd_browse_close()
{
    std::lock_guard lock(s_sd_mutex);
    if (s_browse_file != nullptr) {
        fclose(s_browse_file);
        s_browse_file = nullptr;
    }
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
    std::unique_lock lock(s_sd_mutex);
    if (s_card == nullptr) {
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
    fclose(file);
    lock.unlock();
    if (result == ESP_OK) {
        epd_fb_draw_rows(scratch);
    } else {
        ESP_LOGE(kTag, "%s 帧解码失败：%s", path, esp_err_to_name(result));
    }
    return result;
}
