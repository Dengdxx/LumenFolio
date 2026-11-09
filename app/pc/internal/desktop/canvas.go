package desktop

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/panel"
)

type viewMode int

const (
	modeCrop viewMode = iota
	modePreview
)

func (u *ui) canvas(gtx layout.Context) layout.Dimensions {
	// 指针区域必须落在真正的画布上。Flex 子项在父级原点展开，
	// 若在 canvas 上注册，会从窗口左上角盖住侧栏按钮。
	return layout.Inset{Top: 8, Right: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.card(gtx, u.canvasHeader) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					bounds := image.Rectangle{Max: gtx.Constraints.Max}
					defer clip.UniformRRect(bounds, gtx.Dp(12)).Push(gtx.Ops).Pop()
					return u.card(gtx, u.canvasBody)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.card(gtx, u.statusBar) }),
		)
	})
}

// canvasHeader 显示取景/预览模式切换与快捷操作。
func (u *ui) canvasHeader(gtx layout.Context) layout.Dimensions {
	if u.rawImage == nil {
		return layout.Dimensions{}
	}
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(u.tabButton("取景选取", &u.modeCropBtn, u.mode == modeCrop, func() {
					u.mode = modeCrop
				})),
				layout.Rigid(gap(6)),
				layout.Rigid(u.tabButton("六色预览", &u.modePrevBtn, u.mode == modePreview, func() {
					u.mode = modePreview
					if u.preview == nil && !u.rendering {
						u.triggerDither()
					}
				})),
				layout.Rigid(gap(16)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					hint := "鼠标左键平移 · 滚轮缩放 · 顶部把手或右键旋转"
					if u.mode == modePreview {
						hint = "当前显示 1200×1600 面板的六色墨水效果"
					}
					lbl := material.Body2(u.theme, hint)
					lbl.Color = u.colors.muted
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.mode == modeCrop {
						if u.toPreviewBtn.Clicked(gtx) {
							u.mode = modePreview
							if u.preview == nil && !u.rendering {
								u.triggerDither()
							}
						}
						btn := material.Button(u.theme, &u.toPreviewBtn, "查看效果 →")
						btn.Inset = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}
						return u.styledButton(gtx, btn, false)
					}
					if u.toCropBtn.Clicked(gtx) {
						u.mode = modeCrop
					}
					btn := material.Button(u.theme, &u.toCropBtn, "← 调整取景")
					btn.Inset = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}
					return u.styledButton(gtx, btn, false)
				}),
			)
		})
}

func (u *ui) canvasBody(gtx layout.Context) layout.Dimensions {
	return u.canvasArea(gtx, func(gtx layout.Context) layout.Dimensions {
		if u.rawImage == nil {
			message := "还没有图片，点击左侧「打开图片」"
			label := material.H6(u.theme, message)
			label.Color = u.colors.muted
			label.Alignment = text.Middle
			return layout.Center.Layout(gtx, label.Layout)
		}
		if u.mode == modeCrop {
			return u.cropView(gtx)
		}
		return u.picture(gtx)
	})
}

// canvasArea 把指针事件限制在画布内容区，避免盖住左侧控件。
func (u *ui) canvasArea(gtx layout.Context, content layout.Widget) layout.Dimensions {
	defer clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &u.canvasTag)
	return content(gtx)
}

// cropView 固定在中央显示 1200:1600 取景框。
// 鼠标左键平移背后的图片，滚轮缩放图片，右键拖拽或顶部把手旋转图片。
func (u *ui) cropView(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	viewRect := image.Rectangle{Max: size}

	// 1. 固定取景框在当前视口中央，严格保持 1200:1600 面板比例
	margin := gtx.Dp(unit.Dp(24))
	frameRect := frameRectOnScreen(viewRect, margin)
	sScreen := float64(frameRect.Dx()) / float64(panel.Width)
	fc := image.Pt(frameRect.Min.X+frameRect.Dx()/2, frameRect.Min.Y+frameRect.Dy()/2)

	// 把手位于取景框上方，靠近视口顶部时给圆形按钮留出空间。
	topCenter := f32.Pt(float32(fc.X), float32(frameRect.Min.Y))
	handlePos := topCenter.Sub(f32.Pt(0, float32(gtx.Dp(unit.Dp(24)))))
	handlePos.Y = max(handlePos.Y, float32(gtx.Dp(10)))

	// 2. 指针区域由 canvasArea 限定在画布内，这里只消费事件。
	filter := pointer.Filter{
		Target:  &u.canvasTag,
		Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll | pointer.Cancel,
		ScrollX: pointer.ScrollRange{Min: -1000000, Max: 1000000},
		ScrollY: pointer.ScrollRange{Min: -1000000, Max: 1000000},
	}
	for {
		ev, ok := gtx.Event(filter)
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		if e.Kind == pointer.Scroll {
			anchorX, anchorY := screenToPanel(e.Position, frameRect)
			factor := 1.0
			if e.Scroll.Y < 0 {
				factor = 1.08 // 向上滚轮放大图片
			} else if e.Scroll.Y > 0 {
				factor = 0.92 // 向下滚轮缩小图片
			}
			u.crop.Zoom(factor, anchorX, anchorY)
			u.scheduleDither()
			continue
		}

		// 直接接收左右键事件，避免 gesture.Drag 过滤右键；捕获后仍只跟踪发起拖拽的指针。
		wasDragging := u.gesture.mode != dragNone
		if u.gesture.apply(&u.crop, e, frameRect, fc, handlePos, float64(gtx.Dp(unit.Dp(18)))) {
			u.moved = true
			u.scheduleDither()
		}
		if e.Kind == pointer.Press && !wasDragging && u.gesture.mode != dragNone {
			gtx.Execute(pointer.GrabCmd{Tag: &u.canvasTag, ID: e.PointerID})
		}
		if wasDragging && u.gesture.mode == dragNone {
			if u.moved {
				u.triggerDither()
			}
			u.moved = false
		}
	}

	// 3. 在取景框内打底填充白色（代表相框底纸/留白）
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect(frameRect).Op())

	// 4. 绘制背后的原图（根据平移、缩放与旋转变换）
	if u.rawImageOp == nil || u.rawImageOpSrc != u.rawImage {
		u.rawImageOpSrc = u.rawImage
		op := paint.NewImageOp(u.rawImage)
		u.rawImageOp = &op
	}
	cxImg := float64(u.crop.Bounds.Min.X) + float64(u.crop.Bounds.Dx())/2.0
	cyImg := float64(u.crop.Bounds.Min.Y) + float64(u.crop.Bounds.Dy())/2.0
	scX := float32(float64(fc.X) + u.crop.OX*sScreen)
	scY := float32(float64(fc.Y) + u.crop.OY*sScreen)
	sPhoto := float32(u.crop.Scale * sScreen)

	// 限制原图在 viewRect 内渲染
	viewClip := clip.Rect(viewRect).Push(gtx.Ops)
	trans := f32.AffineId().Offset(f32.Pt(-float32(cxImg), -float32(cyImg)))
	trans = trans.Scale(f32.Point{}, f32.Pt(sPhoto, sPhoto))
	trans = trans.Rotate(f32.Point{}, float32(u.crop.Angle))
	trans = trans.Offset(f32.Pt(scX, scY))
	imgClip := op.Affine(trans).Push(gtx.Ops)
	u.rawImageOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	imgClip.Pop()
	viewClip.Pop()

	// 5. 绘制取景框外的暗化遮罩（4 个外围矩形）
	maskColor := color.NRGBA{14, 14, 17, 190}
	if frameRect.Min.Y > viewRect.Min.Y {
		topMask := image.Rect(viewRect.Min.X, viewRect.Min.Y, viewRect.Max.X, frameRect.Min.Y)
		paint.FillShape(gtx.Ops, maskColor, clip.Rect(topMask).Op())
	}
	if frameRect.Max.Y < viewRect.Max.Y {
		bottomMask := image.Rect(viewRect.Min.X, frameRect.Max.Y, viewRect.Max.X, viewRect.Max.Y)
		paint.FillShape(gtx.Ops, maskColor, clip.Rect(bottomMask).Op())
	}
	if frameRect.Min.X > viewRect.Min.X {
		leftMask := image.Rect(viewRect.Min.X, frameRect.Min.Y, frameRect.Min.X, frameRect.Max.Y)
		paint.FillShape(gtx.Ops, maskColor, clip.Rect(leftMask).Op())
	}
	if frameRect.Max.X < viewRect.Max.X {
		rightMask := image.Rect(frameRect.Max.X, frameRect.Min.Y, viewRect.Max.X, frameRect.Max.Y)
		paint.FillShape(gtx.Ops, maskColor, clip.Rect(rightMask).Op())
	}

	// 6. 绘制固定的取景框外边框 (白色 1.5dp)
	var border clip.Path
	border.Begin(gtx.Ops)
	border.MoveTo(f32.Pt(float32(frameRect.Min.X), float32(frameRect.Min.Y)))
	border.LineTo(f32.Pt(float32(frameRect.Max.X), float32(frameRect.Min.Y)))
	border.LineTo(f32.Pt(float32(frameRect.Max.X), float32(frameRect.Max.Y)))
	border.LineTo(f32.Pt(float32(frameRect.Min.X), float32(frameRect.Max.Y)))
	border.Close()
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 230}, clip.Stroke{Path: border.End(), Width: float32(gtx.Dp(unit.Dp(1.5)))}.Op())

	// 7. 绘制固定的三分网格线 (低透明度白色)
	drawLine := func(p1, p2 f32.Point, c color.NRGBA, w float32) {
		var lp clip.Path
		lp.Begin(gtx.Ops)
		lp.MoveTo(p1)
		lp.LineTo(p2)
		paint.FillShape(gtx.Ops, c, clip.Stroke{Path: lp.End(), Width: w}.Op())
	}
	gridColor := color.NRGBA{255, 255, 255, 45}
	gridWidth := float32(gtx.Dp(unit.Dp(1)))
	y1 := float32(frameRect.Min.Y + frameRect.Dy()/3)
	y2 := float32(frameRect.Min.Y + frameRect.Dy()*2/3)
	drawLine(f32.Pt(float32(frameRect.Min.X), y1), f32.Pt(float32(frameRect.Max.X), y1), gridColor, gridWidth)
	drawLine(f32.Pt(float32(frameRect.Min.X), y2), f32.Pt(float32(frameRect.Max.X), y2), gridColor, gridWidth)
	x1 := float32(frameRect.Min.X + frameRect.Dx()/3)
	x2 := float32(frameRect.Min.X + frameRect.Dx()*2/3)
	drawLine(f32.Pt(x1, float32(frameRect.Min.Y)), f32.Pt(x1, float32(frameRect.Max.Y)), gridColor, gridWidth)
	drawLine(f32.Pt(x2, float32(frameRect.Min.Y)), f32.Pt(x2, float32(frameRect.Max.Y)), gridColor, gridWidth)

	// 8. 绘制固定的四角 L 形手柄
	cornerBracket := func(ox, oy, dx, dy float32) {
		l := float32(gtx.Dp(unit.Dp(12)))
		w := float32(gtx.Dp(unit.Dp(2.5)))
		drawLine(f32.Pt(ox, oy), f32.Pt(ox+dx*l, oy), u.colors.primary, w)
		drawLine(f32.Pt(ox, oy), f32.Pt(ox, oy+dy*l), u.colors.primary, w)
	}
	cornerBracket(float32(frameRect.Min.X), float32(frameRect.Min.Y), 1, 1)
	cornerBracket(float32(frameRect.Max.X), float32(frameRect.Min.Y), -1, 1)
	cornerBracket(float32(frameRect.Max.X), float32(frameRect.Max.Y), -1, -1)
	cornerBracket(float32(frameRect.Min.X), float32(frameRect.Max.Y), 1, -1)

	// 9. 绘制顶部固定旋转把手与连线
	drawLine(topCenter, handlePos, u.colors.primary, float32(gtx.Dp(unit.Dp(1.5))))
	hr := gtx.Dp(unit.Dp(7))
	hrect := image.Rect(int(handlePos.X)-hr, int(handlePos.Y)-hr, int(handlePos.X)+hr, int(handlePos.Y)+hr)
	paint.FillShape(gtx.Ops, u.colors.primary, clip.Ellipse(hrect).Op(gtx.Ops))
	ir := gtx.Dp(unit.Dp(2))
	irect := image.Rect(int(handlePos.X)-ir, int(handlePos.Y)-ir, int(handlePos.X)+ir, int(handlePos.Y)+ir)
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Ellipse(irect).Op(gtx.Ops))

	// 10. 旋转角度指示徽标（非零时显示）
	deg := u.crop.Angle * 180 / math.Pi
	deg = math.Mod(deg, 360)
	if deg > 180 {
		deg -= 360
	} else if deg < -180 {
		deg += 360
	}
	if math.Abs(deg) > 0.05 {
		degStr := fmt.Sprintf("%.1f°", deg)
		macro := op.Record(gtx.Ops)
		lbl := material.Caption(u.theme, degStr)
		lbl.Color = color.NRGBA{255, 255, 255, 255}
		dims := layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, lbl.Layout)
		call := macro.Stop()

		badgePos := handlePos.Add(f32.Pt(-float32(dims.Size.X)/2, -float32(dims.Size.Y)-float32(gtx.Dp(unit.Dp(8)))))
		btrans := op.Offset(image.Pt(int(badgePos.X), int(badgePos.Y))).Push(gtx.Ops)
		brect := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, SE: 4, SW: 4, NW: 4, NE: 4}
		paint.FillShape(gtx.Ops, color.NRGBA{20, 20, 24, 210}, brect.Op(gtx.Ops))
		call.Add(gtx.Ops)
		btrans.Pop()
	}

	return layout.Dimensions{Size: size}
}

// picture 把抖动结果按面板比例居中显示。
func (u *ui) picture(gtx layout.Context) layout.Dimensions {
	return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if u.preview == nil {
			message := "还没有图片"
			if u.rendering {
				message = "正在采样并计算抖动…"
			}
			label := material.H6(u.theme, message)
			label.Color = u.colors.muted
			label.Alignment = text.Middle
			return layout.Center.Layout(gtx, label.Layout)
		}
		return framePicture(gtx, paint.NewImageOp(u.preview))
	})
}

// framePicture 保持面板比例，用于本地取景结果和从相框读回的帧。
func framePicture(gtx layout.Context, imageOp paint.ImageOp) layout.Dimensions {
	max := gtx.Constraints.Max
	width := max.X
	height := width * panel.Height / panel.Width
	if height > max.Y {
		height = max.Y
		width = height * panel.Width / panel.Height
	}
	imageOp.Filter = paint.FilterNearest
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		fill(gtx, color.NRGBA{210, 210, 205, 255})
		return widget.Image{Src: imageOp, Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
	})
}

func (u *ui) statusBar(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.upload.cancelSend != nil {
						return u.uploadProgress(gtx)
					}
					label := material.Body2(u.theme, u.status)
					label.Color = u.colors.muted
					return label.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.rawImage == nil {
						return layout.Dimensions{}
					}
					b := u.rawImage.Bounds()
					info := fmt.Sprintf("原图: %d×%d", b.Dx(), b.Dy())
					label := material.Caption(u.theme, info)
					label.Color = u.colors.muted
					return label.Layout(gtx)
				}),
			)
		})
}
