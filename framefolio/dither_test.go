package main

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

// gradient 生成一张带灰阶、纯色和渐变的样图，用来区分几种抖动的特征。
func gradient() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	for y := range panelHeight {
		for x := range panelWidth {
			i := img.PixOffset(x, y)
			switch {
			case y < panelHeight/5:
				v := byte(x * 255 / (panelWidth - 1))
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
			case y < 2*panelHeight/5:
				img.Pix[i] = byte(x * 255 / (panelWidth - 1))
			case y < 3*panelHeight/5:
				img.Pix[i+1] = byte(x * 255 / (panelWidth - 1))
			case y < 4*panelHeight/5:
				img.Pix[i+2] = byte(x * 255 / (panelWidth - 1))
			default:
				img.Pix[i] = 255
				img.Pix[i+1] = byte(x * 255 / (panelWidth - 1))
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
		allowed[ink.preview] = true
	}
	for _, alg := range algorithms() {
		dst := alg.dither(src)
		if !dst.Bounds().Eq(src.Bounds()) {
			t.Fatalf("%s 尺寸 %v", alg.name(), dst.Bounds())
		}
		for i := 0; i < len(dst.Pix); i += 4 {
			c := color.NRGBA{dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3]}
			if !allowed[c] {
				t.Fatalf("%s 写出了调色板以外的颜色 %v", alg.name(), c)
			}
		}
	}
}

func TestDitherDiffersFromNearest(t *testing.T) {
	src := gradient()
	base := nearestDither{}.dither(src)
	for _, alg := range algorithms()[1:] {
		dst := alg.dither(src)
		if bytesEqual(base.Pix, dst.Pix) {
			t.Errorf("%s 的结果与最近色完全相同", alg.name())
		}
	}
}

func TestFloydSteinbergIsDeterministic(t *testing.T) {
	src := gradient()
	a := floydSteinberg{}.dither(src)
	b := floydSteinberg{}.dither(src)
	if !bytesEqual(a.Pix, b.Pix) {
		t.Fatal("Floyd-Steinberg 两次结果不同")
	}
}

func TestDitherKeepsInkColors(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	stripeWidth := panelWidth / len(inks)
	for i, ink := range inks {
		stripe := image.Rect(i*stripeWidth, 0, (i+1)*stripeWidth, panelHeight)
		draw.Draw(src, stripe, image.NewUniform(ink.preview), image.Point{}, draw.Src)
	}
	for _, alg := range algorithms() {
		t.Run(alg.name(), func(t *testing.T) {
			dst := alg.dither(src)
			if !bytesEqual(src.Pix, dst.Pix) {
				t.Fatal("已精确命中六色墨水的像素不应被改成其他墨水")
			}
		})
	}
}

func TestDitherKeepsWhiteBackground(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	draw.Draw(src, src.Bounds(), image.White, image.Point{}, draw.Src)
	for _, alg := range []ditherer{orderedDither{n: 4}, orderedDither{n: 8}, noiseDither{}} {
		t.Run(alg.name(), func(t *testing.T) {
			dst := alg.dither(src)
			whitePixels := 0
			for y := range panelHeight {
				for x := range panelWidth {
					if dst.NRGBAAt(x, y) == inks[1].preview {
						whitePixels++
					}
				}
			}
			if whitePixels < panelWidth*panelHeight*95/100 {
				t.Fatalf("纯白背景应以白色墨水为主，实际白色占比 %.2f%%", float64(whitePixels)*100/(panelWidth*panelHeight))
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
