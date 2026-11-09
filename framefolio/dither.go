package main

import (
	"image"
	"image/color"
	"math"
	"math/rand"
)

// ditherer 把一张图映射到面板的六种墨水上。
// src 必须已经是 panelWidth×panelHeight，返回的索引图与之等大。
type ditherer interface {
	name() string
	dither(src *image.NRGBA) *image.NRGBA
}

func algorithms() []ditherer {
	return []ditherer{
		nearestDither{},
		floydSteinberg{},
		orderedDither{n: 4},
		orderedDither{n: 8},
		noiseDither{},
	}
}

// nearestDither 每个像素取色差最小的墨水，不扩散误差。
type nearestDither struct{}

func (nearestDither) name() string { return "最近色" }

func (nearestDither) dither(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	for y := range panelHeight {
		for x := range panelWidth {
			i := src.PixOffset(x, y)
			put(dst, i, nearest(src.Pix[i], src.Pix[i+1], src.Pix[i+2]))
		}
	}
	return dst
}

// floydSteinberg 把量化误差按 7:3:5:1 扩散到右、左下、下、右下。
// 误差用 int16 累加，避免每次截断成字节后丢掉小数部分。
type floydSteinberg struct{}

func (floydSteinberg) name() string { return "Floyd-Steinberg" }

func (floydSteinberg) dither(src *image.NRGBA) *image.NRGBA {
	w := panelWidth
	err := make([][3]int16, (w+2)*2)
	dst := image.NewNRGBA(src.Bounds())
	for y := range panelHeight {
		cur := err[y&1*(w+2) : (y&1+1)*(w+2)]
		nxt := err[(y&1^1)*(w+2) : (y&1^1+1)*(w+2)]
		clear(nxt)
		for x := range w {
			i := src.PixOffset(x, y)
			r := clamp(int(src.Pix[i]) + int(cur[x+1][0])/16)
			g := clamp(int(src.Pix[i+1]) + int(cur[x+1][1])/16)
			b := clamp(int(src.Pix[i+2]) + int(cur[x+1][2])/16)
			ink := nearest(byte(r), byte(g), byte(b))
			put(dst, i, ink)
			spread(cur, nxt, x, int16(r)-int16(ink.preview.R), int16(g)-int16(ink.preview.G), int16(b)-int16(ink.preview.B))
		}
	}
	return dst
}

// spread 按 7/16、3/16、5/16、1/16 分配一个像素的量化误差。
// 缓冲每侧多出一列，x+1 才是当前列，所以边界不用单独判断。
func spread(cur, nxt [][3]int16, x int, er, eg, eb int16) {
	add := func(row [][3]int16, col int, w int16) {
		row[col][0] += er * w
		row[col][1] += eg * w
		row[col][2] += eb * w
	}
	add(cur, x+2, 7)
	add(nxt, x, 3)
	add(nxt, x+1, 5)
	add(nxt, x+2, 1)
}

// orderedDither 用 Bayer 矩阵给每个像素加一个固定偏置，再取最近墨水。
// 偏置幅度取相邻两级墨水的色差，而不是固定常数，这样深浅过渡也能出现纹理。
type orderedDither struct{ n int }

func (o orderedDither) name() string {
	if o.n == 4 {
		return "Bayer 4×4"
	}
	return "Bayer 8×8"
}

func (o orderedDither) dither(src *image.NRGBA) *image.NRGBA {
	matrix := bayer4
	if o.n == 8 {
		matrix = bayer8
	}
	dst := image.NewNRGBA(src.Bounds())
	mask := o.n - 1
	scale := 1 / float64(o.n*o.n)
	for y := range panelHeight {
		for x := range panelWidth {
			i := src.PixOffset(x, y)
			threshold := (float64(matrix[(y&mask)*o.n+(x&mask)]) + 0.5) * scale
			put(dst, i, nearestBiased(src.Pix[i], src.Pix[i+1], src.Pix[i+2], threshold))
		}
	}
	return dst
}

// bayer4 是 4×4 Bayer 矩阵，值域 0..15。
var bayer4 = []byte{
	0, 8, 2, 10,
	12, 4, 14, 6,
	3, 11, 1, 9,
	15, 7, 13, 5,
}

// bayer8 是 8×8 Bayer 矩阵，值域 0..63。
var bayer8 = []byte{
	0, 32, 8, 40, 2, 34, 10, 42,
	48, 16, 56, 24, 50, 18, 58, 26,
	12, 44, 4, 36, 14, 46, 6, 38,
	60, 28, 52, 20, 62, 30, 54, 22,
	3, 35, 11, 43, 1, 33, 9, 41,
	51, 19, 59, 27, 49, 17, 57, 25,
	15, 47, 7, 39, 13, 45, 5, 37,
	63, 31, 55, 23, 61, 29, 53, 21,
}

// noiseDither 给每个像素加一个确定的随机偏置再取最近墨水。
// 种子固定，同一张图每次出来的结果一样。
type noiseDither struct{}

func (noiseDither) name() string { return "随机噪声" }

func (noiseDither) dither(src *image.NRGBA) *image.NRGBA {
	rng := rand.New(rand.NewSource(1))
	dst := image.NewNRGBA(src.Bounds())
	for y := range panelHeight {
		for x := range panelWidth {
			i := src.PixOffset(x, y)
			put(dst, i, nearestBiased(src.Pix[i], src.Pix[i+1], src.Pix[i+2], rng.Float64()))
		}
	}
	return dst
}

// nearest 返回与给定颜色色差最小的墨水。
func nearest(r, g, b byte) ink {
	best := inks[0]
	bestDist := math.MaxFloat64
	for _, ink := range inks {
		if d := distance(r, g, b, ink.preview); d < bestDist {
			best, bestDist = ink, d
		}
	}
	return best
}

// nearestBiased 在最近墨水和次近墨水之间按阈值选择。
// threshold 均匀分布于 [0,1) 时，最近墨水的概率随其色差减小而增大；精确命中墨水时保持原色。
func nearestBiased(r, g, b byte, threshold float64) ink {
	first, second := inks[0], inks[1]
	firstDist, secondDist := math.MaxFloat64, math.MaxFloat64
	for _, ink := range inks {
		d := distance(r, g, b, ink.preview)
		switch {
		case d < firstDist:
			second, secondDist = first, firstDist
			first, firstDist = ink, d
		case d < secondDist:
			second, secondDist = ink, d
		}
	}
	if firstDist+secondDist == 0 || threshold >= firstDist/(firstDist+secondDist) {
		return first
	}
	return second
}

// distance 是两个颜色的感知色差平方，绿色权重最大。
func distance(r, g, b byte, c color.NRGBA) float64 {
	dr := float64(int(r) - int(c.R))
	dg := float64(int(g) - int(c.G))
	db := float64(int(b) - int(c.B))
	return 0.3*dr*dr + 0.59*dg*dg + 0.11*db*db
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
func put(dst *image.NRGBA, offset int, ink ink) {
	dst.Pix[offset] = ink.preview.R
	dst.Pix[offset+1] = ink.preview.G
	dst.Pix[offset+2] = ink.preview.B
	dst.Pix[offset+3] = 255
}
