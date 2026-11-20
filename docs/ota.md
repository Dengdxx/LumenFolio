# 固件 OTA

OTA 只更新应用镜像，不改 bootloader、分区表或图片。第一次部署、旧分区迁移和已确认镜像的故障恢复，请走[有线部署](firmware.md)。

相框通过 Wi-Fi 或 BLE 接收应用镜像，写入备用槽并重启。两条链路共用 HTTP 升级服务。客户端确认新镜像后更新才生效；未确认时由应用或 bootloader 回滚。

只在可信局域网使用 Wi-Fi。相框的 HTTP 服务没有账号验证或 TLS，同一网络中能访问服务的设备可以传图、删除图片、发控制台命令或升级固件。BLE 用机身按键打开连接窗口并要求 [Just Works 加密配对](bluetooth.md#传输边界)。镜像校验不验证发布者身份，只使用可信来源的固件。

## 通过配套应用升级

1. 在 [GitHub Releases](https://github.com/Dengdxx/LumenFolio/releases) 的「固件」Release 下载应用镜像 `LumenFolio.bin`。自行构建时使用 [Release preset](firmware.md#构建固件) 生成的 `build/release/LumenFolio.bin`。
2. 唤醒相框并通过 [Wi-Fi](quickstart.md#改用-wi-fi) 或[蓝牙](bluetooth.md)连接。在 Windows 或 Android 应用的 **固件升级**页选择镜像，核对目标和版本后确认。小程序的文件入口见[小程序固件升级](../app/miniprogram/README.md#固件升级)。
3. 保持相框供电、应用在前台，升级期间不要触发刷屏。等应用提示新固件已启动并确认，再退出。

## 通过命令行升级

将电脑接入同一热点，[构建 Windows 应用](development.md#构建-windows-应用)，然后在仓库根目录用设备的实际 IP 查询状态并升级。客户端会等待设备重启，并在核对镜像后自动确认。

```powershell
.\app\pc\LumenFolio.exe --host 192.168.137.42 ota status
.\app\pc\LumenFolio.exe --host 192.168.137.42 ota .\build\release\LumenFolio.bin
```

## 通过 HTTP 手动升级

在 ESP-IDF 环境中用 `python -m esptool image-info build/release/LumenFolio.bin` 查看镜像的 App version 和 ELF file SHA256，重启后确认时会用到。

```powershell
$device = 'http://192.168.137.42:8080'
$before = Invoke-RestMethod "$device/ota/status"
$before
curl.exe --fail-with-body -H 'Content-Type: application/octet-stream' --data-binary '@build/release/LumenFolio.bin' "$device/ota"
```

设备收到有效镜像后返回 HTTP 202，约 1 秒后重启。重新连上热点后查询状态，如果 IP 变了，先更新 `$device`。

```powershell
$after = Invoke-RestMethod "$device/ota/status"
$after
```

在新镜像启动后的 60 秒内，确认 `project` 为 `LumenFolio`、`phase` 为 `pending_verify`、`ready` 为 `true`、`running_partition` 与 `$before.update_partition` 一致，并核对 `version` 和 `image_id` 分别与新镜像的 App version 和 ELF file SHA256 一致，然后发送确认。确认成功后返回 `phase: idle` 和 `pending_verify: false`。

```powershell
Invoke-RestMethod -Method Post -Uri "$device/ota/confirm" -Headers @{
    'X-OTA-Partition' = $after.running_partition
    'X-OTA-Version'   = $after.version
    'X-OTA-Image'     = $after.image_id
}
```

## 接口

| 请求 | 约定 |
| --- | --- |
| `GET /ota/status` | 返回 JSON，`Cache-Control: no-store` |
| `POST /ota` | 原始应用 bin，`Content-Type: application/octet-stream`，必须带明确的 `Content-Length`；不接受 chunked 或 multipart |
| `POST /ota/confirm` | 无请求体，携带 `X-OTA-Partition`、`X-OTA-Version`、`X-OTA-Image` 三个头，核对运行中的镜像后取消回滚 |

状态 JSON 字段：`phase`、`ready`、`pending_verify`、`project`、`version`、`image_id`、`running_partition`、`boot_partition`、`update_partition`、`capacity`、`received`、`total`、`last_error`。

状态还可包含 `id`、`battery_mv`、`battery_percent`、`charge`、`temperature_c`、`humidity_pct`。`id` 是芯片出厂基 MAC，形如 `7c:df:a1:e2:3f:28`，Wi-Fi 和蓝牙连的是不是同一台靠它区分；读不到时字段缺省。传感器尚无采样时，对应字段缺省。

| phase | 含义 |
| --- | --- |
| `idle` | 没有进行中的事务，可以上传 |
| `receiving` | 正在写入备用槽 |
| `reboot_pending` | 校验与启动选择已完成，约 1 秒后重启 |
| `pending_verify` | 新镜像首次启动，等待确认；60 秒超时回滚 |
| `rollback_failed` | 回滚调用返回错误（例如没有可回退的旧镜像），保持当前镜像运行 |

`ready` 只表示固件执行到了 `app_main` 末尾的 `otaServiceReady()`，不表示屏幕或传感器正常。上传不要求 `ready` 为 true，这样卡在初始化里的旧镜像仍可被替换；确认则要求 `ready` 为 true。

`image_id` 是 SDK 写入 `esp_app_desc_t` 的 `app_elf_sha256`，64 个十六进制字符。同一版本号可能对应不同构建，分区名也会被反复复用，所以确认时同时核对分区、版本和 `image_id`。

## 状态与失败

- 上传中断、超过传输时限（设备端 Wi-Fi 为 120 秒，蓝牙为 15 分钟；客户端也有自己的超时）、镜像校验失败或工程名与运行中的固件不符时不切换启动分区。设备回到 `idle` 后可重新上传。
- 上传答复丢失或命令中途被打断时，重连后先查状态。设备已经运行待确认的目标镜像时，直接继续确认。
- 确认答复丢失时，查询运行分区与 `pending_verify`，核实新镜像是否已确认。
- 未确认就复位，由启用了 rollback 的 bootloader 回退；正常调度下 60 秒超时由应用主动回滚。

## 易错点（务必遵守）

- 提示没有 OTA 槽，或 `capacity` 为 `0` → 设备可能仍使用只有 factory 的旧分区表 → 先核对布局，再按[有线部署](firmware.md#首次部署与容量更新)更新，别默认整片擦除。
- 提示工程名不一致，或上传返回 HTTP 422 和 `ESP_ERR_INVALID_ARG` → 镜像的工程名与设备运行的固件不同 → 用 `ota status` 检查 `project`。旧工程名先走[有线部署](firmware.md#首次部署与容量更新)。
- 提示尚未确认 → 相框重启后仍需确认当前镜像 → 保持供电，先查询状态。如果设备正在等待确认，确认当前目标镜像；Windows 和 Android 可选择同一固件继续，手动操作使用上面的确认请求。
- 新镜像启动约一分钟后回退 → 没在 60 秒内收到确认 → 检查重启后能否连上同一设备，以及状态是否为 `ready: true`。维护固件时，确认 `app_main` 末尾仍调用 `otaServiceReady()`，并检查实际生成的 `sdkconfig` 含 `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y`。
- 写 Flash 时刷屏异常或系统崩溃 → `esp_ota_write` 期间 cache 会关闭，帧缓冲在 PSRAM 中 → 将刷屏与升级错开。
- 待确认期间 NVS 初始化失败，回滚后参数丢失 → 新镜像擦了旧镜像需要的数据 → `otaServicePendingVerification()` 为 true 时不得调用 `nvs_flash_erase()`。
