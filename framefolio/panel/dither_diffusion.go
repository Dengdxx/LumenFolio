package panel

import "image"

// AdditionalAlgorithms 返回可独立调用的额外六色抖动算法。
// 桌面界面的选项仍由 Algorithms 提供。
func AdditionalAlgorithms() []Ditherer {
	return []Ditherer{
		errorDiffusion{"Floyd-Steinberg（蛇形）", 16, []diffusionTap{
			{1, 0, 7},
			{-1, 1, 3}, {0, 1, 5}, {1, 1, 1},
		}},
		errorDiffusion{"Atkinson", 8, []diffusionTap{
			{1, 0, 1}, {2, 0, 1},
			{-1, 1, 1}, {0, 1, 1}, {1, 1, 1},
			{0, 2, 1},
		}},
		errorDiffusion{"Sierra Lite", 4, []diffusionTap{
			{1, 0, 2},
			{-1, 1, 1}, {0, 1, 1},
		}},
		errorDiffusion{"Burkes", 32, []diffusionTap{
			{1, 0, 8}, {2, 0, 4},
			{-2, 1, 2}, {-1, 1, 4}, {0, 1, 8}, {1, 1, 4}, {2, 1, 2},
		}},
		errorDiffusion{"Sierra Two-Row", 16, []diffusionTap{
			{1, 0, 4}, {2, 0, 3},
			{-2, 1, 1}, {-1, 1, 2}, {0, 1, 3}, {1, 1, 2}, {2, 1, 1},
		}},
		errorDiffusion{"Sierra", 32, []diffusionTap{
			{1, 0, 5}, {2, 0, 3},
			{-2, 1, 2}, {-1, 1, 4}, {0, 1, 5}, {1, 1, 4}, {2, 1, 2},
			{-1, 2, 2}, {0, 2, 3}, {1, 2, 2},
		}},
		errorDiffusion{"Stucki", 42, []diffusionTap{
			{1, 0, 8}, {2, 0, 4},
			{-2, 1, 2}, {-1, 1, 4}, {0, 1, 8}, {1, 1, 4}, {2, 1, 2},
			{-2, 2, 1}, {-1, 2, 2}, {0, 2, 4}, {1, 2, 2}, {2, 2, 1},
		}},
		errorDiffusion{"Jarvis–Judice–Ninke", 48, []diffusionTap{
			{1, 0, 7}, {2, 0, 5},
			{-2, 1, 3}, {-1, 1, 5}, {0, 1, 7}, {1, 1, 5}, {2, 1, 3},
			{-2, 2, 1}, {-1, 2, 3}, {0, 2, 5}, {1, 2, 3}, {2, 2, 1},
		}},
		blueNoiseDither{},
	}
}

// diffusionTap 使用从左向右扫描时的相对坐标，权重由算法的分母归一化。
type diffusionTap struct {
	dx, dy, weight int
}

type errorDiffusion struct {
	name    string
	divisor int
	taps    []diffusionTap
}

func (d errorDiffusion) Name() string { return d.name }

// Dither 用蛇形扫描在每个像素量化 RGB，并把三通道误差传给尚未量化的像素。
// 奇数行反转横向偏移以减弱固定扫描方向的纹理，累加后才除以分母以保留小误差。
func (d errorDiffusion) Dither(src *image.NRGBA) *image.NRGBA {
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
		if y%2 == 1 {
			start, step = width-1, -1
		}
		srcRow := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstRow := dst.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for n := range width {
			x := start + n*step
			i := srcRow + 4*x
			col := x + pad
			current := rows[0][col]
			r := clamp(int(src.Pix[i]) + current[0]/d.divisor)
			g := clamp(int(src.Pix[i+1]) + current[1]/d.divisor)
			b := clamp(int(src.Pix[i+2]) + current[2]/d.divisor)
			ink := nearest(byte(r), byte(g), byte(b))
			put(dst, dstRow+4*x, ink)

			er := r - int(ink.Preview.R)
			eg := g - int(ink.Preview.G)
			eb := b - int(ink.Preview.B)
			for _, tap := range d.taps {
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
