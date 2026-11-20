# 配套应用开发与回归测试

## 构建 Windows 应用

在 Windows 上安装 Go 1.27，从仓库根目录执行：

```powershell
go -C app/pc build -o LumenFolio.exe .
.\app\pc\LumenFolio.exe
```

生成的 EXE 不入库。安装包还需要 Inno Setup 6；打包入口和产物见[构建与发布](release.md)。

## Android 与微信小程序

- Android：按[工程准备](../app/android/README.md#工程准备)安装 Go 绑定工具、生成 AAR 并构建 APK。
- 微信小程序：按[开发准备](../app/miniprogram/README.md#开发准备)在微信开发者工具中导入项目和配置可选的固件云存储。

## 代码边界

Windows 的 [`main.go`](../app/pc/main.go) 按参数分流到 Gio 图形界面或 CLI。Gio 和 Windows 原生蓝牙依赖分别位于 [`internal/desktop`](../app/pc/internal/desktop) 和 [`internal/blewindows`](../app/pc/internal/blewindows)；共用包不依赖窗口事件循环。图形入口在主 goroutine 调用 `app.Main`，工作任务把结果交回事件循环。

Android 通过 [`mobilepanel` Go 绑定](../app/android/mobilepanel)复用 Windows 端的取景、抖动和设备访问。变更两端的操作或相框接口时，在两端验证受影响的流程。触屏或鼠标坐标由各端换算后交给 `panel.Crop`。

| Go 包 | 职责 |
| --- | --- |
| [`panel`](../app/pc/panel) | 取景采样、[抖动](dither.md)、[帧编解码](frames.md) |
| [`frameclient`](../app/pc/frameclient) | 当前设备会话，统一访问传图、图库和 OTA |
| [`devicehttp`](../app/pc/devicehttp) | 选择普通 HTTP 或蓝牙传输 |
| [`frame`](../app/pc/frame)、[`gallery`](../app/pc/gallery)、[`ota`](../app/pc/ota) | 上传帧及查询写屏进度、读图及缓存缩略图、升级应用镜像并确认 |

微信小程序的[图像与连接代码](../app/miniprogram/miniprogram/frame)以 JavaScript 实现，沿用同一协议和算法顺序。改协议或帧格式时，同时核对小程序的行为；它不会随 Go 实现自动更新。

## 回归测试

在 Windows 上安装 Go 1.27 和 Node.js 22，从仓库根目录运行。Windows 端的离屏绘制测试需要可用的 GPU 后端，否则会跳过相应用例。

```bash
go -C app/pc test -p=1 ./...
go -C app/pc vet -p=1 ./...
go -C app/android/mobilepanel test ./...
node --test app/miniprogram/test/*.test.js
```

安装了 Pillow 的 Python 环境可检查产帧工具的色码、半字节顺序、原始帧回退和尺寸边界：

```bash
python -I -m unittest discover -s tools -p test_frame_format.py -v
```

在 ESP-IDF 6.1 环境中构建固件解码器的独立测试工程：

```bash
idf.py -C components/epd_frame/test_apps build
```

这个命令只编译测试程序。在带 8 MiB PSRAM 的 ESP32-S3 测试板上单独烧录运行，才能验证 ROM 解压、槽边界和损坏帧拒绝。它不是相框固件的开机自检，不要通过相框 OTA 上传测试镜像。

## 易错点（务必遵守）

- 透明 PNG 变黑 → `Color.RGBA()` 返回预乘 alpha 的颜色，不能丢掉透明度 → 采样先在白底合成，再插值和抖动。
- Windows 六色预览只显示左上角 → 裁剪不等于缩放 → 用 Gio `widget.Image` 的 `Contain` 保留完整构图。自绘模式标签仍须由 `Clickable.Layout` 注册点击区域。
- 右键旋转无效，或开始拖动时图片跳动 → `gesture.Drag` 过滤右键，Windows 捕获后首个拖拽坐标可能跳变 → 画布直接处理指针事件，只接受发起拖动的指针；用首个拖拽事件重设基准。
- 开图或预览完成后界面不动 → 后台结果入队后没有唤醒窗口，或满队列时丢弃了结果 → 唤醒并让 UI 循环处理所有就绪结果；抖动结果是否过期由 UI 按构图版本和算法判断。
- 重算预览时打不开图片，或取消选图后第一次点击丢失 → 两项工作共用忙碌标志，或忙碌时未读取 Gio 点击 → 分开文件操作和预览状态；保留点击事件注册，在绘制前应用已就绪的文件操作结果。
