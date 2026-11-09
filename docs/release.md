# 构建与发布

[GitHub Actions](https://github.com/Dengdxx/LumenFolio/actions/workflows/build.yml) 在 `main` 上构建固件、Windows 安装程序和 Android Release APK。构建产物可从对应运行的 Artifacts 下载。[GitHub Releases](https://github.com/Dengdxx/LumenFolio/releases) 按标签分别发布固件与配套 App。

| 标签 | Release 标题 | 下载文件 |
| --- | --- | --- |
| `firmware-v0.1.0` | 固件 v0.1.0 | `LumenFolio.bin` |
| `app-v0.1.0` | 配套 App v0.1.0 | `LumenFolio-Windows-x64-Setup.exe`、`LumenFolio-Android-arm64.apk` |

在要发布的提交上打对应标签并推送。两个版本号可以独立递增。固件 Release 用作仓库的 Latest 入口，配套 App 从 Releases 列表按 `app-v` 标签查找。

## 配套 App

- Windows 用 Inno Setup 生成 x64 安装程序，默认安装到当前用户目录，提供开始菜单和卸载项。安装后启动单个 `LumenFolio.exe`。
- Android 产物为已签名的 ARM64 Release APK。`app-vMAJOR.MINOR.PATCH` 中的版本号作为 `versionName`，`versionCode` 按 `MAJOR × 1000000 + MINOR × 1000 + PATCH + 1` 计算，`MINOR` 和 `PATCH` 为 0 至 999。

Android 发行使用固定别名 `lumenfolio` 的 PKCS12 密钥。将密钥文件的 Base64 内容和密码分别配置为 Actions Secrets `ANDROID_KEYSTORE_BASE64` 和 `ANDROID_KEYSTORE_PASSWORD`。后续版本沿用同一密钥，保证已安装的应用能覆盖更新。拉取请求构建仅提供未签名的 APK 供验证。

## 易错点（务必遵守）

- **相框 OTA**：从「固件」Release 下载 `LumenFolio.bin`，按 [局域网 OTA](ota.md) 执行。
- **Android 安装提示签名冲突**：设备上装的是另一把密钥签名的包。正式发布后应使用同一 Release 密钥签名的 APK 更新。
