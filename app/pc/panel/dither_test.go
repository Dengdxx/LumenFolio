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

func TestAlgorithmsKeepSharedOrder(t *testing.T) {
	want := []string{
		"最近色", "Floyd-Steinberg", "Bayer 4×4", "Bayer 8×8", "随机噪声",
		"Floyd-Steinberg（蛇形）", "Atkinson", "Sierra Lite", "Burkes", "Sierra Two-Row",
		"Sierra", "Stucki", "Jarvis–Judice–Ninke", "蓝噪声 64×64",
	}
	algs := Algorithms()
	if len(algs) != len(want) {
		t.Fatalf("算法数量 %d", len(algs))
	}
	seen := map[string]bool{}
	for index, alg := range algs {
		name := alg.Name()
		if name != want[index] || seen[name] {
			t.Fatalf("序号 %d 是 %s", index, name)
		}
		seen[name] = true
	}
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
	base := Algorithms()[0].Dither(src)
	for _, alg := range Algorithms()[1:] {
		dst := alg.Dither(src)
		if bytesEqual(base.Pix, dst.Pix) {
			t.Errorf("%s 的结果与最近色完全相同", alg.Name())
		}
	}
}

func TestDitherIsDeterministic(t *testing.T) {
	src := gradient()
	for _, alg := range Algorithms()[1:] {
		t.Run(alg.Name(), func(t *testing.T) {
			a := alg.Dither(src)
			b := alg.Dither(src)
			if !bytesEqual(a.Pix, b.Pix) {
				t.Fatal("两次抖动的结果不同")
			}
		})
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
	for _, alg := range Algorithms()[2:] {
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

func TestAtkinsonMirrorsRowsAndSpreadsTwoRows(t *testing.T) {
	gray := [4][4]byte{
		{111, 119, 92, 211},
		{104, 215, 82, 102},
		{209, 92, 78, 63},
		{181, 99, 120, 52},
	}
	// 按 1/8 权重逐点演算，固定奇数行反向、跨两行和边界误差丢弃的结果。
	want := [4][4]int{
		{5, 5, 4, 1},
		{4, 1, 3, 5},
		{1, 4, 4, 0},
		{2, 3, 5, 0},
	}
	bounds := image.Rect(2, 3, 6, 7)
	src := image.NewNRGBA(image.Rect(0, 0, 9, 9)).SubImage(bounds).(*image.NRGBA)
	for y, row := range gray {
		for x, v := range row {
			src.SetNRGBA(bounds.Min.X+x, bounds.Min.Y+y, color.NRGBA{v, v, v, 255})
		}
	}
	for _, alg := range Algorithms() {
		if alg.Name() != "Atkinson" {
			continue
		}
		dst := alg.Dither(src)
		for y, row := range want {
			for x, index := range row {
				if got := dst.NRGBAAt(bounds.Min.X+x, bounds.Min.Y+y); got != inks[index].Preview {
					t.Errorf("(%d, %d) 得到 %v，预期 %v", x, y, got, inks[index].Preview)
				}
			}
		}
		return
	}
	t.Fatal("算法列表未提供 Atkinson")
}

func TestBlueNoiseTileHasUniformThresholds(t *testing.T) {
	if len(blueNoiseTile) != blueNoiseSize*blueNoiseSize {
		t.Fatalf("蓝噪声阈值数量 %d", len(blueNoiseTile))
	}
	var counts [256]int
	for i := range len(blueNoiseTile) {
		counts[blueNoiseTile[i]]++
	}
	for value, count := range counts {
		if count != len(blueNoiseTile)/len(counts) {
			t.Errorf("阈值 %d 出现 %d 次", value, count)
		}
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
