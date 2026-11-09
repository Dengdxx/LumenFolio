# LumenFolio Android

Android 版与 [Windows 图形界面](../../docs/preview.md) 共用取景、六色抖动和帧编码。界面跟随 Android 的深浅色设置。

## 使用

1. 点「打开图片」选择 PNG 或 JPEG，也可以在系统文件应用中用 LumenFolio 打开图片。
2. 在固定的 3:4 取景框内拖动平移、双指缩放或旋转。用「−90°」「+90°」和「复位」快速调整。原图未覆盖的区域和透明区域按白底处理。
3. 选择抖动预设并切到「六色预览」。预设列表来自桌面端，初始选择为「最近色」。预览在 1200×1600 分辨率完成抖动，再等比缩小显示。
4. 在「上传与导出」中输入同一可信局域网内的相框地址，或按 [蓝牙连接步骤](../../docs/bluetooth.md) 选择附近的相框。App 优先选择已挂载的 SD 卡，其余情况默认选 Flash artifacts。可以手动选择存储位置，再点「上传到相框」。SD 卡按原图名生成新帧文件，Flash artifacts 覆盖上传槽。
5. 新固件会回报上传、保存、排队和 SPI 写屏阶段，进度到 100% 表示 SPI 写完。屏幕之后还需完成刷新。旧固件可上传到 Flash artifacts，收到写入回执后结束，并提示该固件的进度限制。
6. 点「导出帧文件」可选本地保存位置。上传和导出均使用当前取景和预设，压缩更小时用 EPD2，否则用 EPD1。

## 查看相框图片

在顶部点「相框图片」，用相框地址或蓝牙连接后读取列表。切换「Flash artifacts」查看开机画面和最近上传的画面，切换「SD 卡」查看卡上的帧文件。页面同时显示两处的已用容量、总容量和占用比例，格式如 `3 MB/12 MB（25.0%）`，以及相框的电量、充电状态、温度和湿度。点「刷新状态」更新容量和设备状态，当前列表和缩略图保留。点「刷新列表」重新读取当前来源的图片。

使用支持分页的固件时，滚动到已加载条目的末尾附近会继续读取下一页。缩略图优先加载可见条目，然后预取下方两张。设备提供图片版本时，App 可在本次运行中复用匹配版本的小图。支持小图接口的固件每张只传 38,400 字节，旧固件可从整帧生成缩略图。

点开条目可查看完整六色预览。选择「在相框上显示」后，相框从原存储读取选中的画面并安排刷新，整帧刷新约需 35 秒。

预览 SD 卡画面或 Flash artifacts 的上传画面时，可以点「删除」，核对名称和来源后再确认。删除成功后，App 会刷新列表和容量。开机画面受保护。

## 固件升级

1. 将应用固件 `LumenFolio.bin` 下载到手机，唤醒相框并通过 Wi-Fi 或蓝牙连接。
2. 在「上传与导出」中点「固件升级」，选择本地 `.bin`，核对显示的版本。
3. 点「开始升级」，核对相框和版本后确认。保持相框供电和连接，让 App 保持打开，直到显示新固件已启动并确认。

首次部署、旧分区布局的迁移和故障恢复见 [局域网 OTA](../../docs/ota.md)。

## 工程准备

需要 Go 1.27、JDK 17、Android SDK 36、Android NDK 和 Gradle 8.13。设置 `ANDROID_HOME` 指向 SDK。NDK 可以由 SDK Manager 并排安装，也可以用 `ANDROID_NDK_HOME` 指定。App 的最低系统版本为 Android 8.0。

构建 Debug 或 Release 前，在仓库根目录执行以下 PowerShell 命令安装绑定工具，并把工具所在目录加入当前终端的 `PATH`。工具版本由 `app/android/mobilepanel/go.mod` 指定。

```powershell
Push-Location app/android/mobilepanel
go install tool
$toolBin = go env GOBIN
if (-not $toolBin) { $toolBin = Join-Path (go env GOPATH) 'bin' }
$env:Path = "$toolBin;$env:Path"
Pop-Location
```

### Debug

从仓库根目录手动生成 AAR，再构建 Debug。Debug 保留 `arm64-v8a`、`armeabi-v7a`、`x86` 和 `x86_64`，可用于手机和模拟器。修改 Go 绑定或共用 Go 代码后，重新生成 Debug AAR。

```powershell
New-Item -ItemType Directory -Force app/android/app/libs | Out-Null
Push-Location app/android/mobilepanel
gomobile bind -target=android -androidapi 26 '-javapkg=org.lumenfolio' -o ../app/libs/mobilepanel.aar .
Pop-Location
gradle -p app/android :app:assembleDebug
```

生成 AAR 后，也可用 Android Studio 打开 `app/android` 构建 Debug。

### Release

```powershell
gradle -p app/android :app:assembleRelease
```

Release 通过 Gradle 的 `bindReleaseMobilePanel` 任务自动生成 `arm64-v8a` 绑定。Go 链接使用 `-s -w` 省略符号表和调试信息，APK 内的原生库启用压缩。默认产物为 `app/android/app/build/outputs/apk/release/app-release-unsigned.apk`。配套 App Release 提供已签名的 `LumenFolio-Android-arm64.apk`，详见 [构建与发布](../../docs/release.md)。

## 易错点（务必遵守）

- Debug 构建找不到 `org.lumenfolio.mobilepanel` → Debug AAR 尚未生成 → 先执行 Debug 的绑定命令。
- Release 绑定找不到 `gomobile` 或 `gobind` → 当前终端的 `PATH` 缺少绑定工具目录 → 先完成工具安装和 `PATH` 配置，再从同一终端运行 Gradle。
- Release 安装时报 `INSTALL_FAILED_NO_MATCHING_ABIS` → APK 只包含 `arm64-v8a` → 在 arm64 设备安装 Release，或在 x86 模拟器使用 Debug。
- 上传返回 HTTP 404 → 固件可能还没有传图接口 → 换成支持 `POST /frame` 的固件后再试。
- 读取列表时提示固件不支持浏览 → 当前固件缺少读图接口 → 更新相框固件后刷新列表。
- 提示 SD 卡未挂载 → 相框当前无法读卡 → 插好 SD 卡并唤醒相框，再刷新列表。
- 上传完成后提示固件升级未确认 → 相框重启后仍需通过当前链路确认镜像 → 保持相框供电，核对连接后选择同一固件重试。设备已运行该镜像并等待确认时，App 会继续确认。
- 设备无响应 → 可能处于深睡 → 按机身键唤醒后确认局域网地址，或长按重刷键重新打开蓝牙连接窗口。
- SPI 已写完但画面还在变 → 电子纸仍在刷新 → 等屏幕稳定后查看画面。
- SD 卡上传提示需更新固件 → 当前固件只支持写入 Flash artifacts → 更新固件后再上传到 SD 卡。
