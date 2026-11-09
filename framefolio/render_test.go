package main

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestPreviewFitsWholeImage(t *testing.T) {
	u := newTestUI()
	red := color.NRGBA{255, 0, 0, 255}
	green := color.NRGBA{0, 255, 0, 255}
	blue := color.NRGBA{0, 0, 255, 255}
	u.preview = image.NewNRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	draw.Draw(u.preview, u.preview.Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
	draw.Draw(u.preview, image.Rect(panelWidth/2, 0, panelWidth, panelHeight/2), image.NewUniform(green), image.Point{}, draw.Src)
	draw.Draw(u.preview, image.Rect(0, panelHeight/2, panelWidth, panelHeight), image.NewUniform(blue), image.Point{}, draw.Src)
	cases := []struct {
		name       string
		size       image.Point
		dpiScale   float32
		red, green image.Point
		blue       image.Point
	}{
		{"面板比例", image.Pt(348, 448), 1, image.Pt(99, 124), image.Pt(249, 124), image.Pt(174, 324)},
		{"横向留边", image.Pt(648, 448), 1, image.Pt(249, 124), image.Pt(399, 124), image.Pt(324, 324)},
		{"纵向留边", image.Pt(348, 848), 1, image.Pt(99, 324), image.Pt(249, 324), image.Pt(174, 524)},
		{"高 DPI", image.Pt(396, 496), 2, image.Pt(123, 148), image.Pt(273, 148), image.Pt(198, 348)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := headless.NewWindow(tc.size.X, tc.size.Y)
			if err != nil {
				t.Skipf("离屏 GPU 不可用：%v", err)
			}
			defer w.Release()
			var ops op.Ops
			u.picture(layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: tc.dpiScale, PxPerSp: tc.dpiScale}, Constraints: layout.Exact(tc.size)})
			if err := w.Frame(&ops); err != nil {
				t.Fatal(err)
			}
			shot := image.NewRGBA(image.Rectangle{Max: tc.size})
			if err := w.Screenshot(shot); err != nil {
				t.Fatal(err)
			}
			for _, sample := range []struct {
				at   image.Point
				want color.NRGBA
			}{{tc.red, red}, {tc.green, green}, {tc.blue, blue}} {
				got := color.NRGBAModel.Convert(shot.At(sample.at.X, sample.at.Y)).(color.NRGBA)
				if got != sample.want {
					t.Errorf("完整构图在 %v 应为 %v，实际 %v", sample.at, sample.want, got)
				}
			}
		})
	}
}
