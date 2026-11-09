/**
 * @file  ota_update.c
 * @brief OTA 写入与回滚状态机的实现。
 *
 * 所有状态读写由静态 mutex 保护，HTTP handler 与后台 poll 任务可并发调用。
 * 相框没有电机与解锁状态，写 Flash 期间不做硬件互斥；
 * 上传期间不要刷屏，esp_ota_write 会关闭 cache，此时访问 PSRAM 中的帧缓冲会崩溃。
 */
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "esp_app_desc.h"
#include "esp_ota_ops.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "ota_update.h"

/** 状态机互斥锁的静态存储与句柄。 */
static StaticSemaphore_t lockStorage;
static SemaphoreHandle_t lock;
/** 当前阶段与启动流程已返回标志。 */
static otaPhase_t phase;
static bool ready, pendingVerify, handleOpen;
/** 当前 OTA 写入句柄。 */
static esp_ota_handle_t handle;
/** running 运行分区；target 本次写入的更新分区。 */
static const esp_partition_t *running, *target;
/** 已写入字节数与本次声明的总字节数。 */
static size_t received, total;
/** 重启或确认窗口的截止时刻，单位 us。 */
static int64_t deadline;
/** 最近一次错误码。 */
static esp_err_t lastError;

/**
 * @brief 生成当前运行镜像的 app_elf_sha256 十六进制字符串。
 *
 * @param[out] out 输出缓冲区，至少 65 字节。
 */
static void currentImageId(char out[65])
{
    static const char hex[] = "0123456789abcdef";
    const uint8_t *id = esp_app_get_description()->app_elf_sha256;
    for (unsigned i = 0; i < 32; i++) {
        out[2 * i] = hex[id[i] >> 4];
        out[2 * i + 1] = hex[id[i] & 15];
    }
    out[64] = 0;
}

/**
 * @brief 中止本次 OTA：关闭句柄并回到 idle。
 *
 * @param[in] reason 记录到 lastError 的原因。
 */
static void abortUpdate(esp_err_t reason)
{
    if (handleOpen) {
        esp_ota_abort(handle);
        handleOpen = false;
    }
    phase = otaIdle;
    lastError = reason;
}

esp_err_t otaUpdateInit(void)
{
    lock = xSemaphoreCreateMutexStatic(&lockStorage);
    if (!lock) return ESP_ERR_NO_MEM;
    running = esp_ota_get_running_partition();
    if (!running) return ESP_ERR_NOT_FOUND;
    ready = pendingVerify = handleOpen = false;
    received = total = 0;
    lastError = ESP_OK;
    target = NULL;
    phase = otaIdle;
    esp_ota_img_states_t state;
    /* 本次启动运行的是待验证镜像：开启 60 s 确认窗口，
     * 超时由 otaUpdatePoll() 触发 SDK 回滚。 */
    if (esp_ota_get_state_partition(running, &state) == ESP_OK && state == ESP_OTA_IMG_PENDING_VERIFY) {
        pendingVerify = true;
        phase = otaPendingVerify;
        deadline = esp_timer_get_time() + OTA_VERIFY_TIMEOUT_US;
    }
    return ESP_OK;
}

bool otaUpdatePendingVerification(void)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    const bool pending = pendingVerify;
    xSemaphoreGive(lock);
    return pending;
}

void otaUpdateSetReady(void)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    ready = true;
    xSemaphoreGive(lock);
}

void otaUpdateGetStatus(otaStatus_t *out)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    const esp_app_desc_t *app = esp_app_get_description();
    const esp_partition_t *boot = esp_ota_get_boot_partition();
    const esp_partition_t *next = esp_ota_get_next_update_partition(NULL);
    *out = (otaStatus_t){.phase = phase, .ready = ready, .pendingVerify = pendingVerify,
        .capacity = next ? next->size : 0, .received = received, .total = total, .lastError = lastError};
    memcpy(out->project, app->project_name, sizeof(app->project_name));
    memcpy(out->version, app->version, sizeof(app->version));
    currentImageId(out->imageId);
    if (running) memcpy(out->runningPartition, running->label, sizeof(running->label));
    if (boot) memcpy(out->bootPartition, boot->label, sizeof(boot->label));
    if (next) memcpy(out->updatePartition, next->label, sizeof(next->label));
    xSemaphoreGive(lock);
}

esp_err_t otaUpdateBegin(size_t size)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    esp_err_t error = ESP_ERR_INVALID_STATE;
    // 上传入口本身已证明网络服务可用；允许替换卡在初始化阶段的旧镜像。
    // ready 仅约束新镜像的确认，不能把无线维修依赖在硬件初始化完成上。
    if (phase != otaIdle || pendingVerify) goto done;
    target = esp_ota_get_next_update_partition(NULL);
    if (!target) { error = ESP_ERR_NOT_FOUND; goto done; }
    if (!size || size > target->size) { error = ESP_ERR_INVALID_SIZE; goto done; }
    received = 0;
    total = size;
    // HTTP 长度单独约束；SDK 在连续写入时逐段擦除，避免一次擦除整个槽。
    error = esp_ota_begin(target, OTA_WITH_SEQUENTIAL_WRITES, &handle);
    if (error != ESP_OK) { abortUpdate(error); goto done; }
    handleOpen = true;
    phase = otaReceiving;
    lastError = ESP_OK;
done:
    xSemaphoreGive(lock);
    return error;
}

esp_err_t otaUpdateWrite(const void *data, size_t size)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    esp_err_t error = ESP_ERR_INVALID_STATE;
    if (phase != otaReceiving) goto done;
    if (!size || size > total - received) {
        error = ESP_ERR_INVALID_SIZE;
    } else {
        error = esp_ota_write(handle, data, size);
    }
    if (error != ESP_OK) abortUpdate(error);
    else received += size;
done:
    xSemaphoreGive(lock);
    return error;
}

esp_err_t otaUpdateFinish(void)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    esp_err_t error = ESP_ERR_INVALID_STATE;
    if (phase != otaReceiving) goto done;
    if (received != total) { error = ESP_ERR_INVALID_SIZE; goto failed; }
    error = esp_ota_end(handle);
    handleOpen = false; // end 无论成功失败都已消费句柄。
    if (error != ESP_OK) goto failed;
    esp_app_desc_t app;
    error = esp_ota_get_partition_description(target, &app);
    if (error != ESP_OK) goto failed;
    /* 新镜像工程名必须与当前一致，防止把其它工程镜像设为 boot 分区。 */
    if (strncmp(app.project_name, esp_app_get_description()->project_name, sizeof(app.project_name)) != 0) {
        error = ESP_ERR_INVALID_ARG;
        goto failed;
    }
    error = esp_ota_set_boot_partition(target);
    if (error != ESP_OK) goto failed;
    phase = otaRebootPending;
    deadline = esp_timer_get_time() + OTA_RESTART_DELAY_US;
    goto done;
failed:
    abortUpdate(error);
done:
    xSemaphoreGive(lock);
    return error;
}

void otaUpdateAbort(esp_err_t reason)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    if (phase == otaReceiving) abortUpdate(reason);
    xSemaphoreGive(lock);
}

esp_err_t otaUpdateConfirm(const char *partition, const char *version, const char *imageId)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    esp_err_t error = ESP_ERR_INVALID_STATE;
    /* 只有在启动流程返回后的 60 s 窗口内才允许确认。 */
    if (!ready || phase != otaPendingVerify || esp_timer_get_time() >= deadline) goto done;
    const esp_app_desc_t *app = esp_app_get_description();
    char currentId[65];
    currentImageId(currentId);
    // 相同版本号的不同构建不能互相确认；直接使用 SDK 的构建标识。
    if (strcmp(partition, running->label) != 0 || strncmp(version, app->version, sizeof(app->version)) != 0 ||
        strcmp(imageId, currentId) != 0) {
        error = ESP_ERR_INVALID_ARG;
        goto done;
    }
    error = esp_ota_mark_app_valid_cancel_rollback();
    if (error == ESP_OK) {
        pendingVerify = false;
        phase = otaIdle;
    }
    lastError = error;
done:
    xSemaphoreGive(lock);
    return error;
}

void otaUpdatePoll(void)
{
    xSemaphoreTake(lock, portMAX_DELAY);
    const int64_t now = esp_timer_get_time();
    /* 超时驱动：reboot_pending 到点重启；pending_verify 到点请求 SDK 回滚。 */
    if (phase == otaRebootPending && now >= deadline) {
        esp_restart();
    } else if (phase == otaPendingVerify && now >= deadline) {
        lastError = esp_ota_mark_app_invalid_rollback_and_reboot();
        // 成功不会返回；没有可回退镜像时保持当前镜像运行并允许读取错误状态。
        phase = otaRollbackFailed;
    }
    xSemaphoreGive(lock);
}
