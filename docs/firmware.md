# 固件构建与部署

## 构建固件

在已加载 ESP-IDF 6.1 环境的终端，从仓库根目录执行。[CMakePresets.json](../CMakePresets.json) 为 Debug 和 Release 分别指定构建目录和 `sdkconfig`。省略 `--preset` 时使用 `debug`。

Debug 用于 USB 烧录和调试，生成 `build/debug/LumenFolio.bin`。

```bash
idf.py --preset debug build
```

Release 用于日常 OTA，应从干净的工作树构建。它在 `sdkconfig.defaults` 的基础上应用 `sdkconfig.release.defaults` 的体积优化设置，生成 `build/release/LumenFolio.bin`。

```bash
idf.py --preset release build
```

## 首次部署与容量更新

OTA 只更新应用槽，不更新 bootloader、Flash 容量配置或分区表。首次部署和从 8 MB 配置扩展到 16 MB 时，都需要通过 USB 更新。当前配置要求实机有 16 MiB Flash 和 8 MiB 八线 PSRAM；先核对芯片容量和 [SDK 默认配置](../sdkconfig.defaults)。硬件只有 8 MiB Flash 时，修改配置不能扩容。

已有设备烧录前必须逐项核对旧分区表：

- **应用槽各 1.5 MB、artifacts 起点为 0x4A0000**：只有在其他分区起点和大小也与本表一致时，8 MB 到 16 MB 的更新才只是把 artifacts 从 0x360000 扩为 0xB60000，可以直接有线更新，无需搬移 NVS 和图片，也无需整片擦除。
- **应用槽各 2 MB、artifacts 起点为 0x620000**：旧表的 otadata、ota_0、ota_1 分别从 0x210000、0x220000、0x420000 起，本表改为 0x190000、0x1A0000、0x320000。这不是单纯扩展末尾，NVS 起点仍为 0x9000，但资产需要迁移。现有烧录命令不搬移图片；有要保留的旧资产时，先停止并单独安排备份与迁移。

任何不匹配的布局都应先停止烧录并安排数据迁移，不能把另一种布局直接套上去。

确认设备没有旧数据，或旧布局允许直接有线更新后，接入 USB，从仓库根目录执行：

```bash
idf.py --preset debug flash monitor
```

完整烧录会写入 factory 并重置 otadata；应用和启动选择会更新，NVS 和未列入烧录参数的资产区域保留。只有明确需要清空设备时才使用 `idf.py erase-flash`，该操作会删除 NVS、图片和 OTA 状态，容量更新本身不要求它。

旧固件的 OTA 要求镜像与运行中的固件工程名一致。如果 `GET /ota/status` 的 `project` 为 `FrameFolio` 或其他旧工程名，先按本节核对分区布局，再用上述命令通过 USB 烧录 LumenFolio。设备启动后确认 `project` 为 `LumenFolio`，即可走[日常无线升级](ota.md)。

## 开机画面

[`main/boot_frame.bin`](../main/boot_frame.bin) 是独立资产，不在 `LumenFolio.bin` 中，普通 `flash` 也不会写入它。用 [`tools/make_frame.py`](../tools/make_frame.py) 生成开机画面时，目标是 artifacts 的第一个 1 MiB 槽（起点 `0x4A0000`），不要覆盖后面的上传槽。固件正常启动不会主动把开机画面送屏；可用[控制台的 `factory` 命令](控制接口.md#控制台)显示它。

## 通过 USB 恢复

确认之后才暴露的问题不在自动回滚范围内。本板不启用 bootloader 的按键恢复工厂：`CONFIG_BOOTLOADER_FACTORY_RESET` 依赖的 GPIO3 在这块板上是 KEY1，正常按键会被当成恢复操作，而且 GPIO3 是启动绑带脚。确认后发现固件有问题，用 USB 重新烧录 factory：

```bash
idf.py --preset debug flash
```

## 分区

16 MB Flash（16,777,216 字节），布局以 [`partitions.csv`](../partitions.csv) 为准：

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

artifacts 从 0x4A0000 延伸到 0x1000000，容量为 11.375 MiB。前 1 MiB 放开机画面，第二个 1 MiB 存最近一次上传到 Flash 的画面，其余空间预留。应用 OTA 不覆盖资产分区。

两个槽都接受 EPD1 原始帧或 EPD2 zlib 无损压缩帧，布局见 [帧格式与兼容性](frames.md)。新固件仍可读取已有 EPD1；旧固件只认 EPD1，所以必须先升级固件，再上传压缩帧或重写压缩开机图。压缩只减少载荷和传输字节数，两个现有槽仍各占 1 MiB，不自动增加照片张数。

## 易错点（务必遵守）

- 现象：改了分区表后启动异常、NVS 报错或分区重叠。原因：新旧布局的起点不同，或启动选择仍指向旧槽。解决：先核对旧表；起点相同且仅扩大资产区时可有线更新，起点不同时先迁移数据，不要盲目整片擦除。
- 现象：16 MB Flash 仍按 8 MB 使用。原因：生成的 sdkconfig 覆盖了新的默认配置。解决：确认 `CONFIG_ESPTOOLPY_FLASHSIZE_16MB=y` 和 `CONFIG_ESPTOOLPY_FLASHSIZE="16MB"`，重新构建并有线更新 bootloader 和分区表；8 MiB 上界是 `0x800000`，16 MiB 上界才是 `0x1000000`，都不可越界访问。
- 现象：按 KEY1 时设备意外回到 factory 或改变启动模式。原因：打开了 `CONFIG_BOOTLOADER_FACTORY_RESET` 且绑在 GPIO3 上。解决：不要添加该项，sdkconfig 里不应出现 `CONFIG_BOOTLOADER_FACTORY_RESET`。
- 现象：镜像能编译但烧不进去，或 OTA 槽装不下。原因：应用超过 0x180000。解决：检查构建报告和 `.bin` 的文件大小（不能超过 1,572,864 字节）。超出时缩减镜像体积，保持 factory 与 OTA 槽大小一致。
