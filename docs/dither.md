# 六色面板抖动

`framefolio/panel` 的 `AdditionalAlgorithms()` 返回可独立调用的额外抖动算法。每项实现现有的 `Ditherer`，调用 `Name()` 取得名称，再对取景采样后的 1200×1600 `*image.NRGBA` 调用 `Dither(src)`。结果是同尺寸的六色预览图，只使用面板调色板中的不透明颜色。桌面界面继续使用 `Algorithms()` 提供的选项。

| 算法 | 区别 |
| --- | --- |
| Floyd-Steinberg（蛇形） | 沿用 7:3:5:1 权重，逐行交替方向，适合和现有逐行同向版比较。 |
| Atkinson | 把误差的 6/8 分给六个位置，保留较鲜明的局部对比，可能丢失极亮或极暗区域的细层次。 |
| Sierra Lite | 三个扩散位置，适合优先控制计算量。 |
| Burkes、Sierra Two-Row | 两行七个扩散位置，权重分布不同。 |
| Sierra、Stucki、Jarvis–Judice–Ninke | 把误差分散到三行，适合与较窄的核比较大面积渐变，计算量也更大。 |
| 蓝噪声 64×64 | 以蓝噪声阈值图代替 Bayer 网格，按像素独立取色，适合想减少规则网格纹理的画面。 |

扩散算法沿用面板的 RGB 色差和限幅规则，偶数行从左往右，奇数行镜像扩散方向，超出画面的误差丢弃。蓝噪声与 Bayer 共用最近色和次近色的选择规则，使用固定阈值图，所以同一输入每次产生相同结果。

扩散核参考 [FFmpeg 的调色板滤镜](https://ffmpeg.org/doxygen/trunk/vf__paletteuse_8c_source.html)、[Crocker 的数字半色调资料](https://github.com/SixLabors/ImageSharp/blob/main/src/ImageSharp/Processing/Processors/Dithering/DHALF.TXT)和 [Jarvis、Judice、Ninke 的论文](https://doi.org/10.1016/S0146-664X(76)80003-2)。

阈值图 [blue_noise_64.bin](../framefolio/panel/blue_noise_64.bin) 是 Christoph Peters 以 CC0 发布的 `Data/64_64/LDR_LLL1_0.png` 的红色通道，按行展开为 4096 个 8 位值。它在 Go 构建时嵌入，运行时直接索引。 [Free blue noise textures](https://momentsingraphics.de/BlueNoise.html)

## 易错点（务必遵守）

- 画面缩放后颗粒与上屏不一致。原因是对预览尺寸做了抖动。应先采样到面板分辨率，抖动后再缩小显示。
- 新算法直接出现在现有 GUI 里。原因是把它们加进了界面读取的 `Algorithms()`。界面接入前，通过 `AdditionalAlgorithms()` 单独调用。
