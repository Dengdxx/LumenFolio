# LumenFolio 固件构建与 OTA

相框通过 Wi-Fi 或 BLE 接收应用镜像，写入备用槽并重启，由客户端确认后生效。两种连接共用 HTTP 升级服务和回滚流程。未确认的新镜像由 bootloader 回滚。

Wi-Fi OTA 用于可信局域网。热点密码固定为 `88888888`，HTTP 上传没有账号、TLS 或签名认证，同一热点下的任何设备都能刷写固件。BLE 使用机身按键打开连接窗口并要求加密配对。SDK 的镜像完整性校验只检查文件是否损坏。

## 分区

16 MB Flash（16,777,216 字节），布局以 `partitions.csv` 为准：

| 分区 | 偏移 | 大小 |
| --- | --- | --- |
| nvs | 0x9000 | 0x6000 |
| phy_init | 0xF000 | 0x1000 |
| factory | 0x10000 | 0x180000 |
| otadata | 0x190000 | 0x2000 |
| ota_0 | 0x1A0000 | 0x180000 |
| ota_1 | 0x320000 | 0x180000 |
| artifacts | 0x4A0000 | 0xB60000 |

otadata 结束于 0x192000，应用偏移必须按 64 KB 对齐，所以 ota_0 从 0x1A0000 起，中间的 56 KB 不使用。三个应用槽各 1.5 MiB，镜像上限为 0x180000（1,572,864 字节）。

artifacts 从 0x4A0000 延伸到 0x1000000，容量为 11.375 MiB。前 1 MiB 放开机画面，第二个 1 MiB 存局域网上传的那一帧，其余空间预留。应用 OTA 不覆盖资产分区。是否属于原地扩容，要看设备原有的应用槽大小与分区起点，不能仅凭旧容量是 8 MB 判断。

两个槽都接受 EPD1 原始帧或 EPD2 zlib 无损压缩帧，布局见 [帧格式与兼容性](preview.md#帧格式与兼容性)。新固件仍可读取已有 EPD1；旧固件只认 EPD1，所以必须先升级固件，再上传压缩帧或重写压缩开机图。压缩只减少载荷和传输字节数，两个现有槽仍各占 1 MiB，不自动增加照片张数。

## 构建固件

在 ESP-IDF 6.1 终端从仓库根目录执行。[CMakePresets.json](../CMakePresets.json) 为 Debug 和 Release 分别指定构建目录和 `sdkconfig`。省略 `--preset` 时使用 `debug`。

Debug 用于 USB 烧录和调试，生成 `build/debug/LumenFolio.bin`。

```bash
idf.py --preset debug build
```

Release 用于日常 OTA，在干净的 Git 提交上构建。它在 `sdkconfig.defaults` 的基础上应用 `sdkconfig.release.defaults` 的体积优化设置，生成 `build/release/LumenFolio.bin`。

```bash
idf.py --preset release build
```

## 首次部署与容量更新

OTA 只更新应用槽，不更新 bootloader、Flash 容量配置或分区表。首次部署和从 8 MB 配置扩展到 16 MB 时，先有线写入完整固件：

```bash
idf.py --preset debug flash monitor
```

烧录前必须逐项核对设备的旧分区表，区分两种布局：

- **应用槽各 1.5 MB、artifacts 起点为 0x4A0000**：只有在其他分区起点和大小也与本表一致时，8 MB 到 16 MB 的更新才只是把 artifacts 从 0x360000 扩为 0xB60000，可以直接有线更新，无需搬移 NVS 和图片，也无需整片擦除。
- **应用槽各 2 MB、artifacts 起点为 0x620000**：旧表的 otadata、ota_0、ota_1 分别从 0x210000、0x220000、0x420000 起，本表改为 0x190000、0x1A0000、0x320000。这不是单纯扩展末尾，必须先迁移旧资产并重置启动选择，不能直接承诺旧图片仍在新位置。NVS 起点仍为 0x9000。

任何不匹配的布局都应先停止烧录并安排数据迁移，不能把另一种布局直接套上去。

完整烧录会写入 factory 并重置 otadata；应用和启动选择会更新，NVS 和未列入烧录参数的资产区域保留。只有明确需要清空设备时才使用 `idf.py erase-flash`，该操作会删除 NVS、图片和 OTA 状态，容量更新本身不要求它。

旧固件的 OTA 要求镜像与运行中的固件工程名一致。如果 `GET /ota/status` 的 `project` 为 `FrameFolio` 或其他旧工程名，先按本节核对分区布局，再用上述命令通过 USB 烧录 LumenFolio。设备启动后确认 `project` 为 `LumenFolio`，即可走日常无线升级。

## 日常升级

设备开机后以 STA 身份连接热点 `smd`（密码 `88888888`），申请 DHCP 租约时上报主机名 `LumenFolio`。地址由热点分配，每次开机可能不同。连上后串口会打印一行：

```text
I (…) wifi: 已连上 smd，IP 192.168.137.42，网关 192.168.137.1，OTA 端口 8080
```

设备只有 2.4 GHz 射频。电脑热点若被系统放在 5 GHz，设备扫不到也连不上，需要把热点的频段改成 2.4 GHz。

准备可信来源的 Release `LumenFolio.bin` 应用镜像。自行构建时使用 `build/release/LumenFolio.bin`。

推送 `firmware-v` 开头的版本标签后，「固件」Release 会提供可用于 OTA 的 `LumenFolio.bin`。配套应用通过独立的 `app-v` 标签发布。详见 [构建与发布](release.md)。

### 通过配套 App 升级

唤醒相框，在 Windows 或 Android App 填写同一可信局域网内的相框地址，或使用 [蓝牙连接](bluetooth.md)。打开「固件升级」，选择本地的 Release `LumenFolio.bin`，核对文件版本和目标设备后确认升级。App 会上传镜像，等待相框重启，再核对并确认目标镜像。等到 App 提示升级成功再退出，期间保持相框供电并避免操作刷屏。

### 通过命令行升级

将电脑接入同一热点，[编译桌面端](preview.md#运行)，然后在仓库根目录用设备的实际 IP 查询状态并升级。客户端会等待设备重启，并在核对镜像后自动确认。

```powershell
.\app\pc\LumenFolio.exe --host 192.168.137.42 ota status
.\app\pc\LumenFolio.exe --host 192.168.137.42 ota .\build\release\LumenFolio.bin
```

### 通过 HTTP 手动升级

用 `python -m esptool image-info build/release/LumenFolio.bin` 查看镜像的 App version 和 ELF file SHA256，重启后确认时会用到。

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

- 上传中断、超过传输时限（Wi-Fi 为 120 秒，蓝牙为 15 分钟）、镜像校验失败或工程名与运行中的固件不符时不切换启动分区。设备回到 `idle` 后可重新上传。
- 上传答复丢失或命令中途被打断时，重连后先查状态。设备已经运行待确认的目标镜像时，直接继续确认。
- 确认答复丢失时，查询运行分区与 `pending_verify`，核实新镜像是否已确认。
- 未确认就复位，由启用了 rollback 的 bootloader 回退；正常调度下 60 秒超时由应用主动回滚。

## 恢复

确认之后才暴露的问题不在自动回滚范围内。本板不启用 bootloader 的按键恢复工厂：`CONFIG_BOOTLOADER_FACTORY_RESET` 依赖的 GPIO3 在这块板上是 KEY1，正常按键会被当成恢复操作，而且 GPIO3 是启动绑带脚。确认后发现固件有问题，用 USB 重新烧录 factory：

```bash
idf.py --preset debug flash
```

## 易错点（务必遵守）

- 现象：上传返回「设备没有 OTA 槽」或 `capacity` 为 0。原因：设备仍是只有 factory 的旧分区表，OTA 不会改分区。解决：按「首次部署与容量更新」核对旧数据位置，再有线更新分区表和固件，不要默认整片擦除。
- 现象：新镜像每次都在约一分钟后自己重启回旧版本。原因：没调用 `otaServiceReady()`，或 `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE` 没进 sdkconfig（本地残留的 sdkconfig 会压过 sdkconfig.defaults）。解决：确认 `app_main` 末尾调用了 `otaServiceReady()`，并在生成的 sdkconfig 里grep 到 `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y`。
- 现象：改了分区表后启动异常、NVS 报错或分区重叠。原因：新旧布局的起点不同，或启动选择仍指向旧槽。解决：先核对旧表；起点相同且仅扩大资产区时可有线更新，起点不同时先迁移数据，不要盲目整片擦除。
- 现象：16 MB Flash 仍按 8 MB 使用。原因：生成的 sdkconfig 覆盖了新的默认配置。解决：确认 `CONFIG_ESPTOOLPY_FLASHSIZE_16MB=y` 和 `CONFIG_ESPTOOLPY_FLASHSIZE="16MB"`，重新构建并有线更新 bootloader 和分区表；8 MiB 上界是 `0x800000`，16 MiB 上界才是 `0x1000000`，都不可越界访问。
- 现象：上传过程中屏幕花掉或设备崩溃。原因：`esp_ota_write` 写 Flash 时会关闭 cache，而帧缓冲放在 PSRAM 里，这段窗口内访问 PSRAM 会出错。解决：上传期间不要刷新屏幕；开机那次自检刷屏也要错开上传。
- 现象：桌面端提示设备与镜像工程名不一致，或手动上传返回 HTTP 422 和 `ESP_ERR_INVALID_ARG`。原因：镜像与设备当前固件的工程名不同。解决：用 `ota status` 或 `GET /ota/status` 检查 `project`。旧设备先按「首次部署与容量更新」走 USB 迁移，已迁移设备使用 Release `build/release/LumenFolio.bin`。
- 现象：待确认期间 NVS 初始化失败后参数全没了。原因：在待确认状态下擦除了 NVS，回滚回去的旧镜像依赖这些数据。解决：保持 `app_main` 里的判断，`otaServicePendingVerification()` 为 true 时不得调用 `nvs_flash_erase()`。
- 现象：按 KEY1 时设备意外回到 factory 或改变启动模式。原因：打开了 `CONFIG_BOOTLOADER_FACTORY_RESET` 且绑在 GPIO3 上。解决：不要添加该项，sdkconfig 里不应出现 `CONFIG_BOOTLOADER_FACTORY_RESET`。
- 现象：镜像能编译但烧不进去，或 OTA 槽装不下。原因：应用超过 0x180000。解决：用 `python -m esptool image-info build/release/LumenFolio.bin` 确认镜像不超过 1,572,864 字节。超出时缩减镜像体积，保持 factory 与 OTA 槽大小一致。
