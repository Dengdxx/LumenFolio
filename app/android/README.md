# LumenFolio Android

Android 版与 [Windows 图形界面](../../docs/preview.md) 共用取景、六色抖动和帧编码。界面跟随 Android 的深浅色设置。

## 使用

1. 点「打开图片」选择 PNG 或 JPEG，也可以在系统文件应用中用 LumenFolio 打开图片。
2. 在固定的 3:4 取景框内拖动平移、双指缩放或旋转。用「−90°」「+90°」和「复位」快速调整。原图未覆盖的区域和透明区域按白底处理。
3. 选择抖动预设并切到「六色预览」。预设列表来自桌面端，初始选择为「最近色」。预览在 1200×1600 分辨率完成抖动，再等比缩小显示。
4. 在「上传与导出」中，输入相框地址后点「上传到相框」，或点「导出帧文件」并选保存位置。两者使用当前取景和预设，压缩更小时用 EPD2，否则用 EPD1。相框需要支持 `POST /frame`，手机和相框需处于同一个可信局域网。

## 查看相框图片

在顶部点「相框图片」，输入相框地址并读取列表。切换「Flash artifacts」查看开机画面和最近上传的画面，切换「SD 卡」查看卡上的帧文件。页面同时显示两处的已用容量、总容量和占用比例，格式如 `3 MB/12 MB（25.0%）`，以及相框的电量、充电状态、温度和湿度。点「刷新状态」更新容量和设备状态，当前列表和缩略图保留。点「刷新列表」重新读取当前来源的图片。

列表逐张加载可见条目的缩略图，点开条目查看完整六色预览。选择「在相框上显示」后，相框从原存储读取选中的画面并安排刷新，整帧刷新约需 35 秒。

## 固件升级

1. 将应用固件 `LumenFolio.bin` 下载到手机，把手机和相框连到同一个可信局域网，并唤醒相框。
2. 在「上传与导出」中点「固件升级」，填写相框地址，选择本地 `.bin`，核对显示的版本。
3. 点「开始升级」，核对相框地址和版本后确认。保持相框供电和网络连接，让 App 保持打开，直到显示新固件已启动并确认。

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

Release 通过 Gradle 的 `bindReleaseMobilePanel` 任务自动生成 `arm64-v8a` 绑定。Go 链接使用 `-s -w` 省略符号表和调试信息，APK 内的原生库启用压缩。默认产物为 `app/android/app/build/outputs/apk/release/app-release-unsigned.apk`，安装或分发前按发布流程单独签名。

## 易错点（务必遵守）

- Debug 构建找不到 `org.lumenfolio.mobilepanel` → Debug AAR 尚未生成 → 先执行 Debug 的绑定命令。
- Release 绑定找不到 `gomobile` 或 `gobind` → 当前终端的 `PATH` 缺少绑定工具目录 → 先完成工具安装和 `PATH` 配置，再从同一终端运行 Gradle。
- Release 安装时报 `INSTALL_FAILED_NO_MATCHING_ABIS` → APK 只包含 `arm64-v8a` → 在 arm64 设备安装 Release，或在 x86 模拟器使用 Debug。
- 上传返回 HTTP 404 → 固件可能还没有传图接口 → 换成支持 `POST /frame` 的固件后再试。
- 读取列表时提示固件不支持浏览 → 当前固件缺少读图接口 → 更新相框固件后刷新列表。
- 提示 SD 卡未挂载 → 相框当前无法读卡 → 插好 SD 卡并唤醒相框，再刷新列表。
- 上传完成后提示固件升级未确认 → 相框重启后仍需联网确认镜像 → 保持相框供电和联网，核对相框地址后选择同一固件重试。设备已运行该镜像并等待确认时，App 会继续确认。
- 设备无响应 → 可能处于深睡 → 按机身键唤醒并确认手机与相框仍处于同一个局域网。
- 已接收但画面还没换 → 202 只确认写入，整帧刷新约 35 秒 → 以实际屏幕为准；若 App 提示刷新未排队，等当前操作结束后再发送。
