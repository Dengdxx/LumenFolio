package panel

import (
	_ "embed"
	"image"
)

const blueNoiseSize = 64

// blueNoiseTile 按行存放 8 位阈值，取自 Christoph Peters 的 CC0 蓝噪声纹理。
//
//go:embed blue_noise_64.bin
var blueNoiseTile string

type blueNoiseDither struct{}

func (blueNoiseDither) Name() string { return "蓝噪声 64×64" }

// Dither 用周期蓝噪声阈值替代 Bayer 网格，仍按同一色差规则选墨水。
func (blueNoiseDither) Dither(src *image.NRGBA) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := range bounds.Dy() {
		maskRow := (y % blueNoiseSize) * blueNoiseSize
		srcRow := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstRow := dst.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for x := range bounds.Dx() {
			i := srcRow + 4*x
			threshold := (float64(blueNoiseTile[maskRow+x%blueNoiseSize]) + 0.5) / 256
			ink := nearestBiased(src.Pix[i], src.Pix[i+1], src.Pix[i+2], threshold)
			put(dst, dstRow+4*x, ink)
		}
	}
	return dst
}
