package panel

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

func requireDitherer(t *testing.T) func(Ditherer, error) Ditherer {
	return func(d Ditherer, err error) Ditherer {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
}

func solidDitherInput(width, height int, c color.NRGBA) *image.NRGBA {
	src := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(src, src.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return src
}

func TestDitherColorOptions(t *testing.T) {
	must := requireDitherer(t)
	gray := solidDitherInput(1, 1, color.NRGBA{70, 70, 70, 255})
	srgb := must(NewNearestDither(ColorOptions{}))
	linear := must(NewNearestDither(ColorOptions{Metric: MetricLinearRGB}))
	if got := srgb.Dither(gray).NRGBAAt(0, 0); got != inks[4].Preview {
		t.Fatalf("默认色差选色 %v，预期蓝色墨水", got)
	}
	if got := linear.Dither(gray).NRGBAAt(0, 0); got != inks[0].Preview {
		t.Fatalf("线性色差选色 %v，预期黑色墨水", got)
	}
	src := solidDitherInput(1, 1, color.NRGBA{220, 70, 150, 255})
	redWeight := must(NewNearestDither(ColorOptions{RGBWeights: [3]float64{1, 0, 0}}))
	greenWeight := must(NewNearestDither(ColorOptions{RGBWeights: [3]float64{0, 1, 0}}))
	if redWeight.Dither(src).NRGBAAt(0, 0) != inks[2].Preview ||
		greenWeight.Dither(src).NRGBAAt(0, 0) != inks[4].Preview {
		t.Fatal("自定义 RGB 权重未改变墨水选择")
	}
}

func TestDiffusionStrengthAndScan(t *testing.T) {
	must := requireDitherer(t)
	src := solidDitherInput(32, 32, color.NRGBA{116, 117, 150, 255})
	colorOptions := ColorOptions{Metric: MetricLinearRGB}
	nearest := must(NewNearestDither(colorOptions)).Dither(src)
	for _, kernel := range []DiffusionKernel{
		FloydSteinberg, Atkinson, Burkes, SierraLite, SierraTwoRow, Sierra, Stucki, JarvisJudiceNinke,
	} {
		off := must(NewErrorDiffusion(kernel, DiffusionOptions{Color: colorOptions}))
		if !bytesEqual(nearest.Pix, off.Dither(src).Pix) {
			t.Fatalf("%s 零扩散强度没有退化到最近色", off.Name())
		}
	}
	raster := must(NewErrorDiffusion(FloydSteinberg, DiffusionOptions{Strength: 1, Scan: ScanRaster}))
	snake := must(NewErrorDiffusion(FloydSteinberg, DiffusionOptions{Strength: 1, Scan: ScanSerpentine}))
	weak := must(NewErrorDiffusion(FloydSteinberg, DiffusionOptions{Strength: 0.5, Scan: ScanRaster}))
	baseline := raster.Dither(src)
	if bytesEqual(baseline.Pix, snake.Dither(src).Pix) || bytesEqual(baseline.Pix, weak.Dither(src).Pix) {
		t.Fatal("修改扩散强度或扫描方向没有改变结果")
	}
	standard := must(NewErrorDiffusion(Atkinson, DiffusionOptions{Strength: 1}))
	full := must(NewErrorDiffusion(Atkinson, DiffusionOptions{Strength: 4.0 / 3.0}))
	if bytesEqual(standard.Dither(src).Pix, full.Dither(src).Pix) {
		t.Fatal("Atkinson 全量误差传播与标准 6/8 的结果相同")
	}
}

func TestThresholdOptions(t *testing.T) {
	must := requireDitherer(t)
	src := solidDitherInput(32, 32, color.NRGBA{130, 135, 150, 255})
	on := ThresholdOptions{Strength: 1}
	off := ThresholdOptions{Color: ColorOptions{Metric: MetricLinearRGB}, Mix: MixBestPair}
	nearest := must(NewNearestDither(off.Color)).Dither(src)
	for _, alg := range []Ditherer{
		must(NewBayerDither(2, image.Pt(1, 1), off)),
		must(NewRandomDither(0, off)),
		must(NewBlueNoiseDither(nil, image.Pt(5, -7), off)),
	} {
		if !bytesEqual(nearest.Pix, alg.Dither(src).Pix) {
			t.Errorf("%s 零阈值强度没有退化到最近色", alg.Name())
		}
	}

	base := must(NewBayerDither(4, image.Point{}, on)).Dither(src)
	phase := must(NewBayerDither(4, image.Pt(-1, 1), on)).Dither(src)
	wrapped := must(NewBayerDither(4, image.Pt(3, -3), on)).Dither(src)
	wide := must(NewBayerDither(16, image.Point{}, on)).Dither(src)
	weak := must(NewBayerDither(4, image.Point{}, ThresholdOptions{Strength: 0.5})).Dither(src)
	for setting, changed := range map[string]*image.NRGBA{"偏移": phase, "尺寸": wide, "强度": weak} {
		if bytesEqual(base.Pix, changed.Pix) {
			t.Errorf("修改 Bayer 的%s没有改变结果", setting)
		}
	}
	if !bytesEqual(phase.Pix, wrapped.Pix) {
		t.Fatal("Bayer 负偏移没有按矩阵尺寸平铺")
	}
	a := must(NewRandomDither(12, on))
	b := must(NewRandomDither(13, on))
	random := a.Dither(src)
	if !bytesEqual(random.Pix, a.Dither(src).Pix) || bytesEqual(random.Pix, b.Dither(src).Pix) {
		t.Fatal("随机阈值的种子没有控制可复现的颗粒布局")
	}
}

func TestBestPairAndCustomBlueNoiseTile(t *testing.T) {
	must := requireDitherer(t)
	// 20% 白墨和 80% 黑墨在 sRGB 坐标中恰好得到此输入色。
	src := solidDitherInput(2, 1, color.NRGBA{65, 65, 64, 255})
	gray := image.NewGray(image.Rect(0, 0, 4, 3))
	bounds := image.Rect(1, 1, 3, 2)
	mask := gray.SubImage(bounds).(*image.Gray)
	mask.SetGray(1, 1, color.Gray{Y: 0})
	mask.SetGray(2, 1, color.Gray{Y: 255})
	options := ThresholdOptions{Strength: 1, Mix: MixBestPair}
	base := must(NewBlueNoiseDither(mask, image.Point{}, options))
	shift := must(NewBlueNoiseDither(mask, image.Pt(-1, 0), options))
	mask.SetGray(1, 1, color.Gray{Y: 255})
	normal, shifted := base.Dither(src), shift.Dither(src)
	if normal.NRGBAAt(0, 0) != inks[1].Preview || normal.NRGBAAt(1, 0) != inks[0].Preview ||
		shifted.NRGBAAt(0, 0) != inks[0].Preview || shifted.NRGBAAt(1, 0) != inks[1].Preview {
		t.Fatal("最佳双色混合未按所选比例使用复制后的阈值图和偏移")
	}
}

func TestDitherRejectsInvalidOptions(t *testing.T) {
	_, metric := NewNearestDither(ColorOptions{Metric: 99})
	_, weight := NewNearestDither(ColorOptions{RGBWeights: [3]float64{-1}})
	_, kernel := NewErrorDiffusion(99, DiffusionOptions{})
	_, scan := NewErrorDiffusion(FloydSteinberg, DiffusionOptions{Scan: 99})
	_, diffusion := NewErrorDiffusion(Burkes, DiffusionOptions{Strength: 1.01})
	_, size := NewBayerDither(7, image.Point{}, ThresholdOptions{})
	_, strength := NewRandomDither(1, ThresholdOptions{Strength: math.NaN()})
	_, mix := NewRandomDither(1, ThresholdOptions{Mix: 99})
	_, mask := NewBlueNoiseDither(image.NewGray(image.Rectangle{}), image.Point{}, ThresholdOptions{})
	for name, err := range map[string]error{
		"色差": metric, "权重": weight, "扩散核": kernel, "扫描方向": scan,
		"扩散强度": diffusion, "矩阵尺寸": size, "阈值强度": strength, "混色": mix, "阈值图": mask,
	} {
		if err == nil {
			t.Errorf("没有拒绝无效的%s", name)
		}
	}
}
