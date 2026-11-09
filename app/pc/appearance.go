package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"sync"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

//go:embed branding/lumen-folio-icon.png
var brandIconPNG []byte

var brandIcon = sync.OnceValue(func() paint.ImageOp {
	icon, err := png.Decode(bytes.NewReader(brandIconPNG))
	if err != nil {
		panic(err)
	}
	return paint.NewImageOp(icon)
})

type appColors struct {
	background, surface, field   color.NRGBA
	text, muted, primary         color.NRGBA
	onPrimary, selected          color.NRGBA
	onSelected, outline, divider color.NRGBA
	error                        color.NRGBA
}

// phoneColors 将 Android 的深浅色语义映射到桌面控件，面板墨水色独立定义。
func phoneColors(dark bool) appColors {
	if dark {
		return appColors{
			background: argb(0xFF0A0A0A), surface: argb(0xFF1C1C1E), field: argb(0xFF2C2C2E),
			text: argb(0xFFF5F5F7), muted: argb(0xFF98989D), primary: argb(0xFF0A84FF),
			onPrimary: argb(0xFF141413), selected: argb(0xFF12324D), onSelected: argb(0xFF8CC8FF),
			outline: argb(0x2EFFFFFF), divider: argb(0x1AFFFFFF), error: argb(0xFFFF6961),
		}
	}
	return appColors{
		background: argb(0xFFF7F6F0), surface: argb(0xFFFAFAF7), field: argb(0xFFF0EFE8),
		text: argb(0xFF141413), muted: argb(0xFF3D3C3A), primary: argb(0xFFD07044),
		onPrimary: argb(0xFF141413), selected: argb(0xFFF4E2D9), onSelected: argb(0xFF8C401D),
		outline: argb(0x381F1E1D), divider: argb(0x1F1F1E1D), error: argb(0xFFB83131),
	}
}

func argb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: uint8(v >> 24)}
}

// card 根据内容的实际尺寸绘制表面和描边，内边距由调用方控制。
func (u *ui) card(gtx layout.Context, content layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Min
		roundedSurface(gtx, size, 12, u.colors.surface, u.colors.divider)
		return layout.Dimensions{Size: size}
	}, content)
}

func roundedSurface(gtx layout.Context, size image.Point, radius unit.Dp, background, border color.NRGBA) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	shape := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(radius))
	paint.FillShape(gtx.Ops, background, shape.Op(gtx.Ops))
	strokeRounded(gtx, size, radius, border)
}

func strokeRounded(gtx layout.Context, size image.Point, radius unit.Dp, border color.NRGBA) {
	width := gtx.Dp(1)
	bounds := image.Rectangle{Max: size}.Inset(width)
	if border.A == 0 || bounds.Empty() {
		return
	}
	path := clip.UniformRRect(bounds, max(0, gtx.Dp(radius)-width)).Path(gtx.Ops)
	paint.FillShape(gtx.Ops, border, clip.Stroke{Path: path, Width: float32(width)}.Op())
}
