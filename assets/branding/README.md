# Lumen Folio 品牌标识

## 设计概念

- 竖置 3:4 圆角相框对应 7.09 英寸 E6 面板在竖屏使用时的 1600 × 1200（4:3）画面比例。
- 右上折页把「Folio / 页册」与相框叠合；左上一个四角星表达「Lumen / 光」。
- 图标内部只保留一条连续的六色平涂色带，去掉分散的圆角色条、重复色点和占位灰线，避免视觉杂乱。
- 字标使用本机 `Blackadder ITC`（`ITCBLKAD.TTF`）并已转为路径，SVG 不依赖外部字体文件；大写 `L` 与 `F` 的装饰横线是该字体的字形特征。

## 交付文件

- `lumen-folio-logo.svg`、`lumen-folio-logo.png` 为 1600 × 900 横版，背景透明。
- `lumen-folio-logo-wide.svg`、`lumen-folio-logo-wide.png` 为 3400 × 900 宽版，保留完整字标，背景透明。
- `lumen-folio-logo-portrait.svg`、`lumen-folio-logo-portrait.png` 为 1200 × 1600 竖版，图标在上，字标在下，背景透明。
- `lumen-folio-icon.svg`：独立方形图标，透明背景。
- `lumen-folio-icon.png`：512 × 512 透明背景 PNG。
- `lumen-folio-logo-preview.svg` 从宽版绘制，叠加浅底供项目首页使用。

## 色值

| 用途 | HEX |
| --- | --- |
| 主字 / 边框 | `#171B21` |
| 暖白 | `#F7F3EA` |
| 折页阴影 | `#EDE6D8` |
| 红 | `#CF493F` |
| 橙 | `#E37A43` |
| 黄 | `#DDA63A` |
| 绿 | `#3B8A6B` |
| 蓝 | `#346FA8` |

> 色值为视觉设计色，不是对具体 E6 面板实测色域或 ICC 配置的声明。

## 更新应用图标

Windows 侧栏和 Android 顶栏使用方形图标，系统图标也从同一份方形 PNG 派生。

更新 `lumen-folio-icon.png` 后，从仓库根目录运行以下命令，同步两个平台的资源。生成 Windows 资源时需安装 Pillow，并将 LLVM 的 `llvm-rc` 和 `llvm-cvtres` 加入 `PATH`。日常构建直接使用仓库中的图标资源。

```powershell
python app/pc/branding/generate.py
Copy-Item -LiteralPath assets/branding/lumen-folio-icon.png -Destination app/android/app/src/main/res/drawable-nodpi/lumenfolio_icon.png
```
