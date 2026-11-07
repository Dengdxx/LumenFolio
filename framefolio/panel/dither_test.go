package panel

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// gradient 生成一张带灰阶、纯色和渐变的样图，用来区分几种抖动的特征。
func gradient() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	for y := range Height {
		for x := range Width {
			i := img.PixOffset(x, y)
			switch {
			case y < Height/5:
				v := byte(x * 255 / (Width - 1))
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
			case y < 2*Height/5:
				img.Pix[i] = byte(x * 255 / (Width - 1))
			case y < 3*Height/5:
				img.Pix[i+1] = byte(x * 255 / (Width - 1))
			case y < 4*Height/5:
				img.Pix[i+2] = byte(x * 255 / (Width - 1))
			default:
				img.Pix[i] = 255
				img.Pix[i+1] = byte(x * 255 / (Width - 1))
			}
			img.Pix[i+3] = 255
		}
	}
	return img
}

func TestDitherUsesOnlyInks(t *testing.T) {
	src := gradient()
	allowed := map[color.NRGBA]bool{}
	for _, ink := range inks {
		allowed[ink.Preview] = true
	}
	for _, alg := range Algorithms() {
		dst := alg.Dither(src)
		if !dst.Bounds().Eq(src.Bounds()) {
			t.Fatalf("%s 尺寸 %v", alg.Name(), dst.Bounds())
		}
		for i := 0; i < len(dst.Pix); i += 4 {
			c := color.NRGBA{dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3]}
			if !allowed[c] {
				t.Fatalf("%s 写出了调色板以外的颜色 %v", alg.Name(), c)
			}
		}
	}
}

func TestDitherDiffersFromNearest(t *testing.T) {
	src := gradient()
	base := nearestDither{}.Dither(src)
	for _, alg := range Algorithms()[1:] {
		dst := alg.Dither(src)
		if bytesEqual(base.Pix, dst.Pix) {
			t.Errorf("%s 的结果与最近色完全相同", alg.Name())
		}
	}
}

func TestFloydSteinbergIsDeterministic(t *testing.T) {
	src := gradient()
	a := floydSteinberg{}.Dither(src)
	b := floydSteinberg{}.Dither(src)
	if !bytesEqual(a.Pix, b.Pix) {
		t.Fatal("Floyd-Steinberg 两次结果不同")
	}
}

func TestDitherKeepsInkColors(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	stripeWidth := Width / len(inks)
	for i, ink := range inks {
		stripe := image.Rect(i*stripeWidth, 0, (i+1)*stripeWidth, Height)
		draw.Draw(src, stripe, image.NewUniform(ink.Preview), image.Point{}, draw.Src)
	}
	for _, alg := range Algorithms() {
		t.Run(alg.Name(), func(t *testing.T) {
			dst := alg.Dither(src)
			if !bytesEqual(src.Pix, dst.Pix) {
				t.Fatal("已精确命中六色墨水的像素不应被改成其他墨水")
			}
		})
	}
}

func TestDitherKeepsWhiteBackground(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(src, src.Bounds(), image.White, image.Point{}, draw.Src)
	for _, alg := range []Ditherer{orderedDither{n: 4}, orderedDither{n: 8}, noiseDither{}} {
		t.Run(alg.Name(), func(t *testing.T) {
			dst := alg.Dither(src)
			whitePixels := 0
			for y := range Height {
				for x := range Width {
					if dst.NRGBAAt(x, y) == inks[1].Preview {
						whitePixels++
					}
				}
			}
			if whitePixels < Width*Height*95/100 {
				t.Fatalf("纯白背景应以白色墨水为主，实际白色占比 %.2f%%", float64(whitePixels)*100/(Width*Height))
			}
		})
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
