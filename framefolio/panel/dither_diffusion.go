package panel

import (
	"fmt"
	"image"
)

// DiffusionKernel 选择误差扩散位置及各位置的权重比例。
type DiffusionKernel uint8

const (
	FloydSteinberg DiffusionKernel = iota
	Atkinson
	SierraLite
	Burkes
	SierraTwoRow
	Sierra
	Stucki
	JarvisJudiceNinke
)

// ScanDirection 选择误差扩散的扫描路径。
type ScanDirection uint8

const (
	// ScanRaster 每行都从左到右。
	ScanRaster ScanDirection = iota
	// ScanSerpentine 奇数行从右到左，扩散核也随之镜像。
	ScanSerpentine
)

// DiffusionOptions 调整扩散强度、扫描路径和六色选色。
type DiffusionOptions struct {
	Color ColorOptions
	Scan  ScanDirection
	// Strength 取 [0,1]。0 是最近色，1 使用标准权重。Atkinson 可取到 4/3。
	Strength float64
}

// AdditionalAlgorithms 返回可独立调用的额外六色抖动算法。
// 桌面界面的选项仍由 Algorithms 提供。
func AdditionalAlgorithms() []Ditherer {
	algorithms := make([]Ditherer, 0, len(diffusionKernels)+1)
	for _, kernel := range [...]DiffusionKernel{
		FloydSteinberg, Atkinson, SierraLite, Burkes, SierraTwoRow, Sierra, Stucki, JarvisJudiceNinke,
	} {
		algorithms = append(algorithms, diffusionPreset(kernel, ScanSerpentine))
	}
	return append(algorithms, builtinBlueNoise(image.Point{}, ThresholdOptions{Strength: 1}))
}

// NewErrorDiffusion 返回可调强度与扫描方向的六色误差扩散算法。
func NewErrorDiffusion(kernel DiffusionKernel, options DiffusionOptions) (Ditherer, error) {
	definition, ok := diffusionKernels[kernel]
	if !ok {
		return nil, fmt.Errorf("未知扩散核 %d", kernel)
	}
	if options.Scan != ScanRaster && options.Scan != ScanSerpentine {
		return nil, fmt.Errorf("未知扫描方向 %d", options.Scan)
	}
	limit := 1.0
	if kernel == Atkinson {
		// 标准 Atkinson 仅传播 6/8，4/3 倍时总传播量恰好为 1。
		limit = 4.0 / 3.0
	}
	if !(0 <= options.Strength && options.Strength <= limit) {
		return nil, fmt.Errorf("扩散强度 %v 超出 [0,%g]", options.Strength, limit)
	}
	if err := options.Color.validate(); err != nil {
		return nil, err
	}
	return errorDiffusion{definition: definition, options: options}, nil
}

func diffusionPreset(kernel DiffusionKernel, scan ScanDirection) Ditherer {
	return errorDiffusion{
		definition: diffusionKernels[kernel],
		options:    DiffusionOptions{Scan: scan, Strength: 1},
	}
}

type diffusionDefinition struct {
	name    string
	divisor int
	taps    []diffusionTap
}

var diffusionKernels = map[DiffusionKernel]diffusionDefinition{
	FloydSteinberg: {"Floyd-Steinberg", 16, []diffusionTap{
		{1, 0, 7},
		{-1, 1, 3}, {0, 1, 5}, {1, 1, 1},
	}},
	Atkinson: {"Atkinson", 8, []diffusionTap{
		{1, 0, 1}, {2, 0, 1},
		{-1, 1, 1}, {0, 1, 1}, {1, 1, 1},
		{0, 2, 1},
	}},
	SierraLite: {"Sierra Lite", 4, []diffusionTap{
		{1, 0, 2},
		{-1, 1, 1}, {0, 1, 1},
	}},
	Burkes: {"Burkes", 32, []diffusionTap{
		{1, 0, 8}, {2, 0, 4},
		{-2, 1, 2}, {-1, 1, 4}, {0, 1, 8}, {1, 1, 4}, {2, 1, 2},
	}},
	SierraTwoRow: {"Sierra Two-Row", 16, []diffusionTap{
		{1, 0, 4}, {2, 0, 3},
		{-2, 1, 1}, {-1, 1, 2}, {0, 1, 3}, {1, 1, 2}, {2, 1, 1},
	}},
	Sierra: {"Sierra", 32, []diffusionTap{
		{1, 0, 5}, {2, 0, 3},
		{-2, 1, 2}, {-1, 1, 4}, {0, 1, 5}, {1, 1, 4}, {2, 1, 2},
		{-1, 2, 2}, {0, 2, 3}, {1, 2, 2},
	}},
	Stucki: {"Stucki", 42, []diffusionTap{
		{1, 0, 8}, {2, 0, 4},
		{-2, 1, 2}, {-1, 1, 4}, {0, 1, 8}, {1, 1, 4}, {2, 1, 2},
		{-2, 2, 1}, {-1, 2, 2}, {0, 2, 4}, {1, 2, 2}, {2, 2, 1},
	}},
	JarvisJudiceNinke: {"Jarvis–Judice–Ninke", 48, []diffusionTap{
		{1, 0, 7}, {2, 0, 5},
		{-2, 1, 3}, {-1, 1, 5}, {0, 1, 7}, {1, 1, 5}, {2, 1, 3},
		{-2, 2, 1}, {-1, 2, 3}, {0, 2, 5}, {1, 2, 3}, {2, 2, 1},
	}},
}

// diffusionTap 使用从左向右扫描时的相对坐标，权重由算法的分母归一化。
type diffusionTap struct {
	dx, dy, weight int
}

type errorDiffusion struct {
	definition diffusionDefinition
	options    DiffusionOptions
}

func (d errorDiffusion) Name() string {
	if d.definition.name == "Floyd-Steinberg" && d.options.Scan == ScanSerpentine {
		return "Floyd-Steinberg（蛇形）"
	}
	return d.definition.name
}

// Dither 把 RGB 量化误差传给尚未量化的像素。
// 累加后才应用强度和分母，避免逐次分发时丢掉小误差。
func (d errorDiffusion) Dither(src *image.NRGBA) *image.NRGBA {
	if d.options.Strength == 0 {
		return pixelDither{color: d.options.Color}.Dither(src)
	}
	matcher := d.options.Color.matcher()
	divisor := float64(d.definition.divisor)
	strength := d.options.Strength
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	width := bounds.Dx()

	// 核最多触及后面两行和左右两列。两侧各留两列接收越界误差，
	// 这些位置不会被量化，随旧行一起清零，防止串到另一侧。
	const pad = 2
	stride := width + 2*pad
	buffer := make([][3]int, 3*stride)
	rows := [3][][3]int{buffer[:stride], buffer[stride : 2*stride], buffer[2*stride:]}
	for y := range bounds.Dy() {
		start, step := 0, 1
		if d.options.Scan == ScanSerpentine && y%2 == 1 {
			start, step = width-1, -1
		}
		srcRow := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstRow := dst.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for n := range width {
			x := start + n*step
			i := srcRow + 4*x
			col := x + pad
			current := rows[0][col]
			r := clamp(int(src.Pix[i]) + int(float64(current[0])*strength/divisor))
			g := clamp(int(src.Pix[i+1]) + int(float64(current[1])*strength/divisor))
			b := clamp(int(src.Pix[i+2]) + int(float64(current[2])*strength/divisor))
			ink := inks[matcher.nearest(byte(r), byte(g), byte(b))]
			put(dst, dstRow+4*x, ink)

			er := r - int(ink.Preview.R)
			eg := g - int(ink.Preview.G)
			eb := b - int(ink.Preview.B)
			for _, tap := range d.definition.taps {
				next := &rows[tap.dy][col+step*tap.dx]
				next[0] += er * tap.weight
				next[1] += eg * tap.weight
				next[2] += eb * tap.weight
			}
		}
		rows[0], rows[1], rows[2] = rows[1], rows[2], rows[0]
		clear(rows[2])
	}
	return dst
}
