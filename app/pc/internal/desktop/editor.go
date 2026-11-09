package desktop

import (
	"image"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/panel"
)

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops, u.colors.background)
	if u.gallery.host != "" {
		return u.galleryLayout(gtx)
	}
	if u.ota.open {
		return u.otaLayout(gtx)
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(u.sidebar),
		layout.Flexed(1, u.canvas),
	)
}

func (u *ui) sidebar(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(248))
	controls := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.heading),
			layout.Rigid(gap(12)),
			layout.Rigid(u.button("打开图片", &u.openButton, u.onOpen)),
			layout.Rigid(gap(14)),
			// 取景快捷调整（只有打开图片后显示）
			layout.Rigid(u.cropControls),
			layout.Rigid(u.section("抖动算法")),
			layout.Rigid(gap(6)),
			layout.Flexed(1, u.algorithmList),
			layout.Rigid(gap(10)),
			layout.Rigid(u.section("墨水")),
			layout.Rigid(gap(6)),
			layout.Rigid(u.swatches),
			layout.Rigid(gap(14)),
			layout.Rigid(u.section("相框 IP 或主机名")),
			layout.Rigid(gap(6)),
			layout.Rigid(u.hostField),
			layout.Rigid(gap(8)),
			layout.Rigid(u.button("查看相框图片", &u.gallery.openButton, u.openGallery)),
			layout.Rigid(gap(8)),
			layout.Rigid(u.button("固件升级", &u.ota.openButton, u.openOTA)),
			layout.Rigid(gap(8)),
			layout.Rigid(u.uploadLayout),
			layout.Rigid(gap(8)),
			layout.Rigid(u.button("导出帧文件", &u.exportButton, u.onExport)),
		)
	}
	return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(8).Layout(gtx, controls)
		})
	})
}

func (u *ui) heading(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Dp(26), gtx.Dp(26))
			roundedSurface(gtx, size, 8, argb(0xFFF7F3EA), u.colors.outline)
			gtx.Constraints = layout.Exact(size)
			icon := widget.Image{Src: brandIcon(), Fit: widget.Contain, Position: layout.Center}
			return layout.UniformInset(2).Layout(gtx, icon.Layout)
		}),
		layout.Rigid(gap(10)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(u.wordmarkLabel),
				layout.Rigid(gap(2)),
				layout.Rigid(u.section("1200×1600 · 六色")),
			)
		}),
	)
}

func (u *ui) wordmarkLabel(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Constrain(image.Pt(gtx.Dp(126), gtx.Dp(24)))
	gtx.Constraints = layout.Exact(size)
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	semantic.LabelOp("LumenFolio").Add(gtx.Ops)
	wordmark := widget.Image{Src: u.wordmark, Fit: widget.Contain, Position: layout.W}
	return wordmark.Layout(gtx)
}

func (u *ui) cropControls(gtx layout.Context) layout.Dimensions {
	if u.rawImage == nil {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.section("取景调整")),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.rotLeftBtn.Clicked(gtx) {
						u.crop.RotateAroundFrameCenter(-90)
						u.triggerDither()
					}
					b := material.Button(u.theme, &u.rotLeftBtn, "↺ -90°")
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return u.styledButton(gtx, b, false)
				}),
				layout.Rigid(gap(6)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.rotRightBtn.Clicked(gtx) {
						u.crop.RotateAroundFrameCenter(90)
						u.triggerDither()
					}
					b := material.Button(u.theme, &u.rotRightBtn, "↻ +90°")
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return u.styledButton(gtx, b, false)
				}),
				layout.Rigid(gap(6)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.resetCropBtn.Clicked(gtx) {
						u.crop = panel.NewCrop(u.rawImage.Bounds())
						u.triggerDither()
					}
					b := material.Button(u.theme, &u.resetCropBtn, "复位")
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return u.styledButton(gtx, b, false)
				}),
			)
		}),
		layout.Rigid(gap(14)),
	)
}

func (u *ui) algorithmList(gtx layout.Context) layout.Dimensions {
	algs := panel.Algorithms()
	// Gio 的 List 会在父级原点注册滚动区域。侧栏从窗口左上角开始，
	// 不先裁剪就会盖住列表上方的按钮。
	macro := op.Record(gtx.Ops)
	dims := u.algList.Layout(gtx, len(algs), func(gtx layout.Context, index int) layout.Dimensions {
		return u.algorithm(index, algs[index].Name())(gtx)
	})
	call := macro.Stop()
	defer clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return dims
}

func (u *ui) algorithm(index int, name string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if u.algButtons[index].Clicked(gtx) && index != u.alg && !u.rendering && !u.fileBusy {
			u.alg = index
			u.triggerDither()
		}
		return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return u.choice(gtx, &u.algButtons[index], name, index == u.alg)
		})
	}
}

func (u *ui) swatches(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
		layout.Rigid(u.swatch(panel.Inks()[0])),
		layout.Rigid(u.swatch(panel.Inks()[1])),
		layout.Rigid(u.swatch(panel.Inks()[2])),
		layout.Rigid(u.swatch(panel.Inks()[3])),
		layout.Rigid(u.swatch(panel.Inks()[4])),
		layout.Rigid(u.swatch(panel.Inks()[5])),
	)
}

func (u *ui) swatch(ink panel.Ink) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := gtx.Dp(unit.Dp(14))
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{X: size, Y: size}
				gtx.Constraints.Max = gtx.Constraints.Min
				roundedSurface(gtx, gtx.Constraints.Min, 7, ink.Preview, u.colors.outline)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(gap(4)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.Caption(u.theme, ink.Name)
				label.Color = u.colors.muted
				return label.Layout(gtx)
			}),
		)
	}
}
