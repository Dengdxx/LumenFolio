# FrameFolio 局域网 OTA

相框固件作为 STA 接入外部热点，在这条连接上开 HTTP 服务接收应用镜像：写入备用槽，重启，由客户端确认后生效。未确认的新镜像由 bootloader 回滚。

首版按可信台架使用。热点密码固定为 `88888888`，HTTP 上传没有账号、TLS 或签名认证，同一热点下的任何设备都能刷写固件。不要在公共场合用这个配置。SDK 的镜像完整性校验只保证文件没损坏，不代表来源可信。

## 分区

8 MB Flash，`partitions.csv`：

| 分区 | 偏移 | 大小 |
| --- | --- | --- |
| nvs | 0x9000 | 0x6000 |
| phy_init | 0xF000 | 0x1000 |
| factory | 0x10000 | 0x200000 |
| otadata | 0x210000 | 0x2000 |
| ota_0 | 0x220000 | 0x200000 |
| ota_1 | 0x420000 | 0x200000 |
| artifacts | 0x620000 | 0x1E0000 |

otadata 结束于 0x212000，但应用分区偏移必须按 64 KB 对齐，所以 ota_0 从 0x220000 起，中间空出的 56 KB 不使用。两个 OTA 槽与 factory 等大：包括金色 factory 在内的任何镜像都必须装进 0x200000（2,097,152 字节）。取 2 MB 是按早期把开机画面嵌进应用、合计约 1.79 MB 定的；画面移到 artifacts 之后应用降到约 891 KB，槽位暂未缩小。

artifacts 从 ota_1 结束处 0x620000 起，占满剩余的 0x1E0000，放开机画面和之后的其他资产。它不随应用 OTA 更新：换图只重烧这一段，升级固件也不会覆盖它。

## 首次部署

OTA 只更新应用槽，不更新 bootloader 和分区表。改过分区表、或设备上还没有带 rollback 的 bootloader 时，必须整片擦除后有线烧录一次：

```powershell
idf.py erase-flash
idf.py flash monitor
```

之后的日常升级走无线，不再擦除。`erase-flash` 会清掉 NVS 和 otadata，等于回到 factory。

已使用当前分区表和回滚 bootloader 的旧品牌设备，首次迁移时用 USB 执行 `idf.py flash monitor`。这会写入 FrameFolio factory 镜像并重置 otadata，保留 NVS 和 artifacts。旧固件的 OTA 会拒绝工程名不同的镜像。

## 日常升级

设备开机后以 STA 身份连接热点 `smd`（密码 `88888888`），地址由热点侧的 DHCP 分配，每次开机可能不同。连上后串口会打印一行：

```text
I (…) wifi: 已连上 smd，IP 192.168.137.42，网关 192.168.137.1，OTA 端口 8080
```

设备只有 2.4 GHz 射频。电脑热点若被系统放在 5 GHz，设备扫不到也连不上，需要把热点的频段改成 2.4 GHz。

在 ESP-IDF 终端从仓库根目录执行 `idf.py build`，生成 `build/FrameFolio.bin`。用 `python -m esptool image-info build/FrameFolio.bin` 查看镜像的 App version 和 ELF file SHA256，重启后确认时会用到。电脑接在同一个热点上，替换下方示例 IP，然后上传应用镜像。

```powershell
$device = 'http://192.168.137.42:8080'
$before = Invoke-RestMethod "$device/ota/status"
$before
curl.exe --fail-with-body -H 'Content-Type: application/octet-stream' --data-binary '@build/FrameFolio.bin' "$device/ota"
```

设备收到有效镜像后返回 HTTP 202，约 1 秒后重启。重新连上热点后查询状态，如果 IP 变了，先更新 `$device`。

```powershell
$after = Invoke-RestMethod "$device/ota/status"
$after
```

在新镜像启动后的 60 秒内，确认 `project` 为 `FrameFolio`、`phase` 为 `pending_verify`、`ready` 为 `true`、`running_partition` 与 `$before.update_partition` 一致，并核对 `version` 和 `image_id` 分别与新镜像的 App version 和 ELF file SHA256 一致，然后发送确认。确认成功后返回 `phase: idle` 和 `pending_verify: false`。

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

状态还可包含 `battery_mv`、`battery_percent`、`charge`、`temperature_c`、`humidity_pct`。对应传感器尚无采样时，相关字段缺省。

| phase | 含义 |
| --- | --- |
| `idle` | 没有进行中的事务，可以上传 |
| `receiving` | 正在写入备用槽 |
| `reboot_pending` | 校验与启动选择已完成，约 1 秒后重启 |
| `pending_verify` | 新镜像首次启动，等待确认；60 秒超时回滚 |
| `rollback_failed` | 回滚调用返回错误（例如没有可回退的旧镜像），保持当前镜像运行 |

`ready` 只表示 `app_main` 已返回，不表示屏幕或传感器正常。上传不要求 `ready` 为 true，这样卡在初始化里的旧镜像仍可被替换；确认则要求 `ready` 为 true。

`image_id` 是 SDK 写入 `esp_app_desc_t` 的 `app_elf_sha256`，64 个十六进制字符。同一版本号可能对应不同构建，分区名也会被反复复用，所以确认时同时核对分区、版本和 `image_id`。

## 状态与失败

- 上传中断、超过 120 秒、镜像校验失败或工程名与运行中的固件不符时不切换启动分区。设备回到 `idle` 后可重新上传。
- 上传答复丢失或命令中途被打断时，重连后先查状态。设备已经运行待确认的目标镜像时，直接继续确认。
- 确认答复丢失时，查询运行分区与 `pending_verify`，核实新镜像是否已确认。
- 未确认就复位，由启用了 rollback 的 bootloader 回退；正常调度下 60 秒超时由应用主动回滚。

## 恢复

确认之后才暴露的问题不在自动回滚范围内。本板不启用 bootloader 的按键恢复工厂：`CONFIG_BOOTLOADER_FACTORY_RESET` 依赖的 GPIO3 在这块板上是 KEY1，正常按键会被当成恢复操作，而且 GPIO3 是启动绑带脚。确认后发现固件有问题，用 USB 重新烧录 factory：

```powershell
idf.py erase-flash
idf.py flash
```

## 易错点（务必遵守）

- 现象：上传返回「设备没有 OTA 槽」或 `capacity` 为 0。原因：设备上仍是旧分区表，只有 factory。解决：按「首次部署」执行一次 `idf.py erase-flash` 再 `idf.py flash`，OTA 更新不了分区表。
- 现象：新镜像每次都在约一分钟后自己重启回旧版本。原因：没调用 `otaServiceReady()`，或 `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE` 没进 sdkconfig（本地残留的 sdkconfig 会压过 sdkconfig.defaults）。解决：确认 `app_main` 末尾调用了 `otaServiceReady()`，并在生成的 sdkconfig 里grep 到 `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y`。
- 现象：改了分区表后启动异常、NVS 报错或分区重叠。原因：Flash 上还留着旧分区表和旧 otadata。解决：改分区表后必须 `idf.py erase-flash` 一次。
- 现象：上传过程中屏幕花掉或设备崩溃。原因：`esp_ota_write` 写 Flash 时会关闭 cache，而帧缓冲放在 PSRAM 里，这段窗口内访问 PSRAM 会出错。解决：上传期间不要刷新屏幕；开机那次自检刷屏也要错开上传。
- 现象：现有 Go 地面站拒绝上传 `FrameFolio.bin`。原因：它在本地校验的是旧工程名。解决：按「日常升级」直接调用设备 HTTP 接口。
- 现象：设备返回 HTTP 422，提示 `ESP_ERR_INVALID_ARG`。原因：上传的镜像与运行中的固件工程名不同。解决：用 `GET /ota/status` 检查 `project`，旧品牌设备先按「首次部署」走 USB 迁移，已迁移设备使用 `idf.py build` 生成的 `build/FrameFolio.bin`。
- 现象：待确认期间 NVS 初始化失败后参数全没了。原因：在待确认状态下擦除了 NVS，回滚回去的旧镜像依赖这些数据。解决：保持 `app_main` 里的判断，`otaServicePendingVerification()` 为 true 时不得调用 `nvs_flash_erase()`。
- 现象：按 KEY1 时设备意外回到 factory 或改变启动模式。原因：打开了 `CONFIG_BOOTLOADER_FACTORY_RESET` 且绑在 GPIO3 上。解决：不要添加该项，sdkconfig 里不应出现 `CONFIG_BOOTLOADER_FACTORY_RESET`。
- 现象：镜像能编译但烧不进去，或 OTA 槽装不下。原因：应用超过 0x200000。解决：用 `idf.py size` 确认应用小于 2,097,152 字节；超出就减体积，不要加大 factory，OTA 槽与它等大。
