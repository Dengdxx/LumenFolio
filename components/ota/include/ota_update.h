/**
 * @file  ota_update.h
 * @brief OTA 写入、校验与回滚状态机。
 *
 * 状态迁移：otaIdle --Begin--> otaReceiving --Finish--> otaRebootPending
 * --重启--> otaPendingVerify --Confirm--> otaIdle；
 * 超时未确认则调用 SDK 回滚并重启，回滚不可用时停在 otaRollbackFailed。
 *
 * 所有状态读写由内部 mutex 保护，HTTP handler 与后台 poll 任务可并发调用。
 * 本模块不持有任何硬件资源；刷屏与 OTA 写 Flash 的互斥由调用方保证。
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

/** @brief 待确认状态的最长保持时间，60 s；超时触发回滚。 */
#define OTA_VERIFY_TIMEOUT_US (60LL * 1000000)
/** @brief 上传完成到重启的延迟，1 s，用于发出 HTTP 回包。 */
#define OTA_RESTART_DELAY_US  (1LL * 1000000)

/**
 * @brief OTA 状态机阶段。
 *
 * idle 空闲；receiving 接收中；reboot_pending 待重启；
 * pending_verify 待确认；rollback_failed 回滚失败。
 */
typedef enum {
    otaIdle, otaReceiving, otaRebootPending, otaPendingVerify, otaRollbackFailed
} otaPhase_t;

/** @brief OTA 状态快照。 */
typedef struct {
    /** 当前状态机阶段。 */
    otaPhase_t phase;
    /** ready 启动流程已返回、可确认镜像；pendingVerify 本次启动待确认。 */
    bool ready, pendingVerify;
    /** project/version 运行镜像的工程名与版本串。 */
    char project[33], version[33];
    /** 运行镜像构建标识，64 字符十六进制，取自 SDK 的 app_elf_sha256。 */
    char imageId[65];
    /** 运行分区 / boot 分区 / 下一个更新分区的 label。 */
    char runningPartition[17], bootPartition[17], updatePartition[17];
    /** capacity 更新分区容量；received 已写入字节；total 本次声明大小；单位 byte。 */
    size_t capacity, received, total;
    /** 最近一次错误码。 */
    esp_err_t lastError;
} otaStatus_t;

/**
 * @brief 初始化 OTA 状态机，并检测运行镜像是否处于待确认状态。
 *
 * @return ESP_OK 成功；ESP_ERR_NO_MEM 锁创建失败；ESP_ERR_NOT_FOUND 无运行分区。
 *
 * @note 由 app_main 在 nvs_flash_init() 之前调用：待确认判定依赖它，而 NVS
 *       错误时是否允许擦除取决于该判定。
 */
esp_err_t otaUpdateInit(void);

/**
 * @brief 本次启动是否在等待新镜像确认。
 *
 * @return 待确认返回 true。
 */
bool otaUpdatePendingVerification(void);

/**
 * @brief 标记启动流程已返回，允许 otaUpdateConfirm() 生效。
 */
void otaUpdateSetReady(void);

/**
 * @brief 获取状态快照。
 *
 * @param[out] status 输出状态。
 */
void otaUpdateGetStatus(otaStatus_t *status);

/**
 * @brief 开始一次 OTA 会话。
 *
 * @param[in] size 声明的固件字节数，必须非零且不超过更新分区容量。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 非空闲或存在待确认镜像；
 *         ESP_ERR_NOT_FOUND 无更新分区；ESP_ERR_INVALID_SIZE 大小非法。
 *
 * @note 要求 idle 且无待确认镜像。不要求启动流程已返回：ready 只约束确认，
 *       不约束上传，以便替换卡在初始化阶段的旧镜像。
 */
esp_err_t otaUpdateBegin(size_t size);

/**
 * @brief 追加一段固件数据。
 *
 * @param[in] data 数据指针。
 * @param[in] size 字节数，不能超过剩余声明长度。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 不在接收阶段；
 *         ESP_ERR_INVALID_SIZE 长度非法；写入失败时返回 SDK 错误码并中止。
 */
esp_err_t otaUpdateWrite(const void *data, size_t size);

/**
 * @brief 结束写入，校验镜像工程名并设置下次 boot 分区。
 *
 * @return ESP_OK 成功，状态进入 otaRebootPending；否则返回错误并中止。
 *
 * @warning 成功后约 1 s 重启。该窗口内不要再发起会操作 Flash 或 PSRAM 的动作。
 */
esp_err_t otaUpdateFinish(void);

/**
 * @brief 中止接收阶段的 OTA；其他阶段忽略本次调用。
 *
 * @param[in] reason 记录到 lastError 的原因。
 */
void otaUpdateAbort(esp_err_t reason);

/**
 * @brief 确认运行镜像并取消待验证镜像的回滚。
 *
 * @param[in] partition 运行分区 label，须与当前分区一致。
 * @param[in] version   运行镜像版本串，须一致。
 * @param[in] imageId   运行镜像 app_elf_sha256 十六进制串，须一致。
 * @return ESP_OK 成功；ESP_ERR_INVALID_STATE 未 ready、非待确认或已超时；
 *         ESP_ERR_INVALID_ARG 参数不匹配；SDK 标记失败时返回其错误码。
 */
esp_err_t otaUpdateConfirm(const char *partition, const char *version, const char *imageId);

/**
 * @brief 由后台任务周期驱动超时处理。
 *
 * otaRebootPending 到期后重启；otaPendingVerify 到期后请求 SDK 回滚并重启。
 * 不依赖 HTTP handler 返回。
 */
void otaUpdatePoll(void);

#ifdef __cplusplus
}
#endif
