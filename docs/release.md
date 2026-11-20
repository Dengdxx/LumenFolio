# CI 与发布

[构建工作流](../.github/workflows/build.yml) 在 PR 和推送到 `main` 时构建 ESP32-S3 固件、Windows x64 安装程序和 Android ARM64 APK。带标签的构建按产品发布，两条发布线的版本可以独立递增。

## CI 产物

从对应的 [Actions 运行](https://github.com/Dengdxx/LumenFolio/actions/workflows/build.yml)下载 Artifact：

| Artifact | 文件 | 条件 |
| --- | --- | --- |
| `firmware` | `LumenFolio.bin` | PR、`main`、固件标签 |
| `app-windows` | `LumenFolio-Windows-x64-Setup.exe` | PR、`main`、配套 App 标签 |
| `app-android` | `LumenFolio-Android-arm64.apk` | `main`、配套 App 标签 |
| `app-android-unsigned` | `app-release-unsigned.apk` | PR，仅供构建验证，不能直接安装 |

Windows 构建先跑 Go 测试和 `vet`，Android 构建先跑 `mobilepanel` 的 Go 检查。工作流不连接相框，不运行 Python 帧测试、板上解码测试或小程序测试；这些见[回归测试](development.md#回归测试)。小程序不随 `app-v` 标签发布。

## 发布带注释的标签

1. 选择已通过构建检查的提交，在该提交上创建带注释的标签。用 `firmware-vMAJOR.MINOR.PATCH` 发布固件，或用 `app-vMAJOR.MINOR.PATCH` 发布 Windows 和 Android 应用。注释会成为 Release 说明。
2. 只推送本次发布的标签。固件标签只触发固件发布，配套 App 标签只触发两个应用的发布。
3. 在 [GitHub Releases](https://github.com/Dengdxx/LumenFolio/releases) 检查产物和说明。固件 Release 标题形如「固件 v0.1.0」，用作仓库 Latest；应用 Release 标题形如「配套 App v0.1.0」，从 Releases 列表按 `app-v` 查找。

固件 CI 将标签去掉 `firmware-` 后的值传给 `PROJECT_VER`，例如 `v0.1.0`，含 `v` 最长 31 字符。PR 和 `main` 的版本字段用提交 SHA 前七位。本地构建未传 `PROJECT_VER` 时，不使用这套 CI 命名规则。

应用标签的三个分量均为十进制非负整数，不允许前导零。Android 的 `versionName` 形如 `0.1.0`，`versionCode` 为 `MAJOR × 1000000 + MINOR × 1000 + PATCH + 1`，不得超过 `2100000000`；`MINOR` 和 `PATCH` 各为 0 至 999。

## 安装程序与签名

Windows 安装程序由 Inno Setup 6 根据 [`LumenFolio.iss`](../app/pc/installer/LumenFolio.iss) 打包。默认安装至当前用户目录，提供开始菜单和卸载项，安装后运行单个 `LumenFolio.exe`。

Android 签名用别名为 `lumenfolio` 的 PKCS12 密钥库，库和密钥使用同一密码。把库的 Base64 内容和密码分别配置为 Actions Secrets `ANDROID_KEYSTORE_BASE64`、`ANDROID_KEYSTORE_PASSWORD`。`main` 推送和配套 App 标签都需要这两个 Secrets；PR 不签名。后续发布沿用同一密钥，确保能覆盖更新已安装的 APK。

## 易错点（务必遵守）

- 想升级相框，却下载了安装程序或 APK → 固件与应用在不同 Release → 下载「固件」的 `LumenFolio.bin`，按[固件 OTA](ota.md)操作。
- `main` 构建能生成 APK 却在签名失败 → 缺少 Secrets，或密钥别名与密码不符 → 核对本节配置和[签名脚本](../app/android/tools/sign-release.sh)。
- Android 安装提示签名冲突 → 已安装版本由另一把密钥签名 → 使用原 Release 密钥签名的 APK 更新。
