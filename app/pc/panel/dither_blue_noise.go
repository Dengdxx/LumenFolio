package panel

import (
	_ "embed"
	"fmt"
	"image"
)

const blueNoiseSize = 64

// blueNoiseTile 按行存放 8 位阈值，取自 Christoph Peters 的 CC0 蓝噪声纹理。
//
//go:embed blue_noise_64.bin
var blueNoiseTile string

// NewBlueNoiseDither 返回平铺灰度阈值图的蓝噪声算法。
// tile 为 nil 时用内置 64×64 蓝噪声图。自定义图须由调用方选好，构造时复制像素。
// offset 以面板像素为单位平移阈值采样坐标，支持负值。
func NewBlueNoiseDither(tile *image.Gray, offset image.Point, options ThresholdOptions) (Ditherer, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	if tile == nil {
		return builtinBlueNoise(offset, options), nil
	}
	width, height := tile.Rect.Dx(), tile.Rect.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("蓝噪声阈值图为空")
	}
	pixels := make([]byte, width*height)
	for y := range height {
		row := tile.PixOffset(tile.Rect.Min.X, tile.Rect.Min.Y+y)
		copy(pixels[y*width:(y+1)*width], tile.Pix[row:row+width])
	}
	name := fmt.Sprintf("蓝噪声 %d×%d", width, height)
	return tiledDither(name, string(pixels), width, height, 256, offset, options), nil
}

func builtinBlueNoise(offset image.Point, options ThresholdOptions) Ditherer {
	return tiledDither("蓝噪声 64×64", blueNoiseTile, blueNoiseSize, blueNoiseSize, 256, offset, options)
}
