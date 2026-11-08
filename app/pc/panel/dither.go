package panel

import (
	"image"
	"slices"
)

// Ditherer 把已经按面板取景采样的图像映射到六种墨水，返回同边界的 NRGBA 图。
// 输入像素应已与白底合成。Dither 不修改输入，同一实例可并发处理不同图像。
type Ditherer interface {
	Name() string
	Dither(src *image.NRGBA) *image.NRGBA
}

// Algorithms 返回界面和命令行共用的算法。
// 返回顺序就是界面按钮和 --alg 的序号，新预设只追加在末尾。
func Algorithms() []Ditherer {
	return slices.Clone(defaultAlgorithms[:])
}

var defaultAlgorithms = [...]Ditherer{
	pixelDither{name: "最近色"},
	diffusionPreset(FloydSteinberg, ScanRaster),
	bayerDither(4, image.Point{}, ThresholdOptions{Strength: 1}),
	bayerDither(8, image.Point{}, ThresholdOptions{Strength: 1}),
	randomDither(1, ThresholdOptions{Strength: 1}),
	diffusionPreset(FloydSteinberg, ScanSerpentine),
	diffusionPreset(Atkinson, ScanSerpentine),
	diffusionPreset(SierraLite, ScanSerpentine),
	diffusionPreset(Burkes, ScanSerpentine),
	diffusionPreset(SierraTwoRow, ScanSerpentine),
	diffusionPreset(Sierra, ScanSerpentine),
	diffusionPreset(Stucki, ScanSerpentine),
	diffusionPreset(JarvisJudiceNinke, ScanSerpentine),
	builtinBlueNoise(image.Point{}, ThresholdOptions{Strength: 1}),
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// put 把一种墨水的预览色写入像素缓冲。
func put(dst *image.NRGBA, offset int, ink Ink) {
	dst.Pix[offset] = ink.Preview.R
	dst.Pix[offset+1] = ink.Preview.G
	dst.Pix[offset+2] = ink.Preview.B
	dst.Pix[offset+3] = 255
}
