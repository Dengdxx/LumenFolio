# LumenFolio Android

Android 版与 [Windows 图形界面](../docs/preview.md) 共用取景、六色抖动和帧编码。界面跟随 Android 的深浅色设置。

## 使用

1. 点「打开图片」选择 PNG 或 JPEG，也可以在系统文件应用中用 LumenFolio 打开图片。
2. 在固定的 3:4 取景框内拖动平移、双指缩放或旋转。用「−90°」「+90°」和「复位」快速调整。原图未覆盖的区域和透明区域按白底处理。
3. 选择抖动预设并切到「六色预览」。预设列表来自桌面端，初始选择为「最近色」。预览在 1200×1600 分辨率完成抖动，再等比缩小显示。
4. 在「上传与导出」中，输入相框地址后点「上传到相框」，或点「导出帧文件」并选保存位置。两者使用当前取景和预设，压缩更小时用 EPD2，否则用 EPD1。相框需要支持 `POST /frame`，手机和相框需处于同一个可信局域网。

## 查看相框图片

在顶部点「相框图片」，输入相框地址并读取列表。切换「Flash artifacts」查看开机画面和最近上传的画面，切换「SD 卡」查看卡上的帧文件。点列表中的文件名预览相框保存的六色画面。

## 工程准备

需要 Go 1.27、JDK 17、Android SDK 36、Android NDK 和 Gradle 8.13。设置 `ANDROID_HOME` 指向 SDK。NDK 可以由 SDK Manager 并排安装，也可以用 `ANDROID_NDK_HOME` 指定。App 的最低系统版本为 Android 8.0。

先在仓库根目录执行以下 PowerShell 命令生成供 Gradle 引用的 AAR。绑定工具版本由 `lumenfolio/mobilepanel/go.mod` 指定。

```powershell
New-Item -ItemType Directory -Force android/app/libs | Out-Null
Push-Location lumenfolio/mobilepanel
go install tool
$toolBin = go env GOBIN
if (-not $toolBin) { $toolBin = Join-Path (go env GOPATH) 'bin' }
$env:Path = "$toolBin;$env:Path"
gomobile bind -target=android -androidapi 26 -javapkg=org.lumenfolio -o ../../android/app/libs/mobilepanel.aar .
Pop-Location
```

用 Android Studio 打开 `android`，也可以在仓库根目录用 `gradle -p android :app:assembleDebug`。Go 绑定和共用图像处理代码改动后，重新生成 AAR。

## 易错点（务必遵守）

- 找不到 `org.lumenfolio.mobilepanel` → Gradle 依赖的 AAR 尚未生成 → 先执行上面的绑定命令。
- 上传返回 HTTP 404 → 固件可能还没有传图接口 → 换成支持 `POST /frame` 的固件后再试。
- 读取列表时提示固件不支持浏览 → 当前固件缺少读图接口 → 更新相框固件后刷新列表。
- 提示 SD 卡未挂载 → 相框当前无法读卡 → 插好 SD 卡并唤醒相框，再刷新列表。
- 设备无响应 → 可能处于深睡 → 按机身键唤醒并确认手机与相框仍处于同一个局域网。
- 已接收但画面还没换 → 202 只确认写入，整帧刷新约 35 秒 → 以实际屏幕为准；若 App 提示刷新未排队，等当前操作结束后再发送。
