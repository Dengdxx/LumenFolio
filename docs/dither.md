# 六色面板抖动

Go 包 `lumenfolio/panel` 的 `Algorithms()` 返回界面和命令行共用的全部抖动预设。每项实现 `Ditherer`，调用 `Name()` 取得名称，再对取景采样后的 1200×1600 `*image.NRGBA` 调用 `Dither(src)`。结果是同尺寸的六色预览图，只使用面板调色板中的不透明颜色。返回顺序就是界面按钮和 `--alg` 的序号。

## 预设

顺序见 [`Algorithms()`](../app/pc/panel/dither.go)。Windows 和 Android 的六色预览按面板分辨率做抖动；微信小程序为减少等待，用较小的图做预览，上传时再按完整分辨率计算。

| 序号 | 算法 | 区别 |
| --- | --- | --- |
| 0 | 最近色 | 每个像素取色差最小的墨水，不传播误差。 |
| 1 | Floyd-Steinberg | 每行从左往右，按 7:3:5:1 扩散误差。 |
| 2、3 | Bayer 4×4、Bayer 8×8 | 用规则的阈值网格决定每一像素的取色。 |
| 4 | 随机噪声 | 用固定种子的伪随机阈值取色。 |
| 5 | Floyd-Steinberg（蛇形） | 核权重不变，逐行反向扫描。 |
| 6 | Atkinson | 把误差的 6/8 分给六个位置，保留局部对比，可能丢失极亮或极暗处的层次。 |
| 7 | Sierra Lite | 三个扩散位置，计算量较小。 |
| 8、9 | Burkes、Sierra Two-Row | 两行七个扩散位置，权重分布不同。 |
| 10、11、12 | Sierra、Stucki、Jarvis–Judice–Ninke | 在三行内扩散，适合比较大面积渐变，计算量较大。 |
| 13 | 蓝噪声 64×64 | 用蓝噪声阈值图代替 Bayer 网格，减少规则纹理。 |

扩散算法丢弃超出画面的误差。蛇形扫描从偶数行左端、奇数行右端开始，反向时也镜像扩散核。同向扫描不镜像。蓝噪声和 Bayer 使用固定阈值图，对同一输入重复处理会得到同一结果。

扩散核参考 [FFmpeg 的调色板滤镜](https://ffmpeg.org/doxygen/trunk/vf__paletteuse_8c_source.html)、[Crocker 的数字半色调资料](https://github.com/SixLabors/ImageSharp/blob/main/src/ImageSharp/Processing/Processors/Dithering/DHALF.TXT)和 [Jarvis、Judice、Ninke 的论文](https://doi.org/10.1016/S0146-664X(76)80003-2)。

阈值图 [blue_noise_64.bin](../app/pc/panel/blue_noise_64.bin) 是 Christoph Peters 以 CC0 发布的 `Data/64_64/LDR_LLL1_0.png` 的红色通道，按行展开为 4096 个 8 位值。它在 Go 构建时嵌入，运行时直接索引。 [Free blue noise textures](https://momentsingraphics.de/BlueNoise.html)

## 调参接口

调用方通过构造函数生成 `Ditherer`，再走同一条 `Dither(src)` 路径。实例可复用，构造时校验参数。`Algorithms()` 提供界面和命令行共用的预设；自定义参数使用下面的构造函数。

| 构造函数 | 参数 |
| --- | --- |
| `NewNearestDither(color)` | 选色空间和 RGB 权重。 |
| `NewErrorDiffusion(kernel, options)` | Floyd–Steinberg、Atkinson、Sierra Lite、Burkes、Sierra Two-Row、Sierra、Stucki 或 JJN，另选扩散强度、同向或蛇形扫描，以及选色设置。 |
| `NewBayerDither(size, offset, options)` | 矩阵尺寸为 2、4、8 或 16，偏移按矩阵尺寸回绕。 |
| `NewRandomDither(seed, options)` | 每次渲染从指定种子重新开始，原预设用 `1`。 |
| `NewBlueNoiseDither(tile, offset, options)` | `nil` 使用内置 64×64 图，或传入 `*image.Gray` 自定义阈值图。构造时复制像素，偏移按图的宽高回绕。 |

- `ColorOptions` 的零值用 `MetricSRGB` 和 `[0.3, 0.59, 0.11]`。可改用 `MetricLinearRGB`，先线性化 sRGB 再比较色差。权重各取 `[0,1]`，全零沿用默认值。扩散误差继续在原来的 8 位 RGB 通道中累加。 [sRGB 线性化公式](https://www.w3.org/Graphics/Color/srgb)
- `DiffusionOptions.Strength` 为 `0` 时输出最近色，`1` 使用标准核权重。Atkinson 额外支持到 `4/3`，即把原本传播的 `6/8` 提到全部误差。原 Floyd–Steinberg 预设同向扫描，其余扩散预设逐行反向。
- Bayer、随机和蓝噪声共用 `ThresholdOptions`。强度 `0` 输出最近色，`1` 完整应用混色规则。默认 `MixNearestTwo` 按最近色与次近色的色差取比例。也可选 `MixBestPair`，比较调色板里所有双色组合，在所选色差空间找最接近输入的混合比例。自定义阈值图宜使用均匀分布的秩值，混色比例才能体现在平铺后的像素占比上。

```go
dither, err := panel.NewErrorDiffusion(panel.Stucki, panel.DiffusionOptions{
    Scan:     panel.ScanSerpentine,
    Strength: 0.8,
})
if err != nil {
    return err
}
preview := dither.Dither(sampled)
```

## 易错点（务必遵守）

- 画面缩放后颗粒与上屏不一致。原因是对预览尺寸做了抖动。应先采样到面板分辨率，抖动后再缩小显示。
- 新增预设插到 `Algorithms()` 中间会让旧的 `--alg` 序号指向另一种算法。新预设只追加在末尾；界面按钮和命令行共用这个顺序，默认 `1` 仍是同向扫描的 Floyd-Steinberg。
