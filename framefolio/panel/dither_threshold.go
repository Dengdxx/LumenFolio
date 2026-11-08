package panel

import (
	"fmt"
	"image"
	"math/rand"
)

// InkMix 决定阈值算法用哪些墨水及什么比例混合。
type InkMix uint8

const (
	// MixNearestTwo 按现有的最近色、次近色色差比例混合。
	MixNearestTwo InkMix = iota
	// MixBestPair 选择在色差空间里最接近输入颜色的双色混合。
	MixBestPair
)

// ThresholdOptions 由 Bayer、随机噪声和蓝噪声共用。
type ThresholdOptions struct {
	Color ColorOptions
	Mix   InkMix
	// Strength 取 [0,1]。0 是最近色，1 完整应用所选混色规则。
	Strength float64
}

func (options ThresholdOptions) validate() error {
	if !(0 <= options.Strength && options.Strength <= 1) {
		return fmt.Errorf("阈值强度 %v 超出 [0,1]", options.Strength)
	}
	if options.Mix != MixNearestTwo && options.Mix != MixBestPair {
		return fmt.Errorf("未知墨水混合规则 %d", options.Mix)
	}
	return options.Color.validate()
}

// NewBayerDither 返回指定矩阵尺寸的 Bayer 算法。尺寸支持 2、4、8、16。
// offset 以面板像素为单位平移阈值采样坐标，支持负值。
func NewBayerDither(size int, offset image.Point, options ThresholdOptions) (Ditherer, error) {
	switch size {
	case 2, 4, 8, 16:
	default:
		return nil, fmt.Errorf("Bayer 矩阵尺寸 %d 不受支持", size)
	}
	if err := options.validate(); err != nil {
		return nil, err
	}
	return bayerDither(size, offset, options), nil
}

func bayerDither(size int, offset image.Point, options ThresholdOptions) Ditherer {
	// 四象限按 0、2、3、1 加秩，每扩一层保留前一层的空间排列。
	ranks := []byte{0}
	for n := 1; n < size; n *= 2 {
		width := 2 * n
		next := make([]byte, width*width)
		for y := range n {
			for x := range n {
				base := 4 * ranks[y*n+x]
				next[y*width+x] = base
				next[y*width+x+n] = base + 2
				next[(y+n)*width+x] = base + 3
				next[(y+n)*width+x+n] = base + 1
			}
		}
		ranks = next
	}
	name := fmt.Sprintf("Bayer %d×%d", size, size)
	return tiledDither(name, string(ranks), size, size, size*size, offset, options)
}

// NewRandomDither 返回固定种子的随机阈值算法，每次 Dither 从该种子重新开始。
func NewRandomDither(seed int64, options ThresholdOptions) (Ditherer, error) {
	if err := options.validate(); err != nil {
		return nil, err
	}
	return randomDither(seed, options), nil
}

func randomDither(seed int64, options ThresholdOptions) Ditherer {
	return pixelDither{
		name: "随机噪声", color: options.Color, mix: options.Mix, strength: options.Strength,
		thresholds: func() func(x, y int) float64 {
			rng := rand.New(rand.NewSource(seed))
			return func(_, _ int) float64 { return rng.Float64() }
		},
	}
}

func tiledDither(name, tile string, width, height, levels int, offset image.Point, options ThresholdOptions) Ditherer {
	// 先把负偏移归一化，避免在逐像素路径上处理负数取模。
	phaseX := (offset.X%width + width) % width
	phaseY := (offset.Y%height + height) % height
	scale := 1 / float64(levels)
	return pixelDither{
		name: name, color: options.Color, mix: options.Mix, strength: options.Strength,
		thresholds: func() func(x, y int) float64 {
			return func(x, y int) float64 {
				row := ((y + phaseY) % height) * width
				rank := tile[row+(x+phaseX)%width]
				return (float64(rank) + 0.5) * scale
			}
		},
	}
}

// pixelDither 处理独立像素。阈值发生器在每次渲染时创建，种子状态不会跨次共享。
type pixelDither struct {
	name       string
	color      ColorOptions
	mix        InkMix
	strength   float64
	thresholds func() func(x, y int) float64
}

func (d pixelDither) Name() string { return d.name }

func (d pixelDither) Dither(src *image.NRGBA) *image.NRGBA {
	matcher := d.color.matcher()
	selectInk := func(_, _ int, r, g, b byte) int { return matcher.nearest(r, g, b) }
	if d.strength > 0 {
		threshold := d.thresholds()
		mix := matcher.nearestTwo
		if d.mix == MixBestPair {
			mix = matcher.bestPair
		}
		selectInk = func(x, y int, r, g, b byte) int {
			return mix(r, g, b, threshold(x, y), d.strength)
		}
	}
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	for y := range bounds.Dy() {
		srcRow := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		dstRow := dst.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		for x := range bounds.Dx() {
			i := srcRow + 4*x
			index := selectInk(x, y, src.Pix[i], src.Pix[i+1], src.Pix[i+2])
			put(dst, dstRow+4*x, inks[index])
		}
	}
	return dst
}
