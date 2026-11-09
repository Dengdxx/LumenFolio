package desktop

import (
	"image"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// moveArea 交给 Windows 处理窗口拖动、双击最大化和贴靠。
func moveArea(gtx layout.Context, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	area.Pop()
}

func (u *ui) windowControls(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(u.windowButton(&u.minimizeBtn, "最小化", system.ActionMinimize)),
			layout.Rigid(u.windowButton(&u.maximizeBtn, "最大化或还原", system.ActionMaximize)),
			layout.Rigid(u.windowButton(&u.closeBtn, "关闭", system.ActionClose)),
		)
	})
}

func (u *ui) windowButton(button *widget.Clickable, label string, action system.Action) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if button.Clicked(gtx) && u.appWindow != nil {
			if action == system.ActionMaximize && u.windowMode == app.Maximized {
				u.appWindow.Perform(system.ActionUnmaximize)
			} else {
				u.appWindow.Perform(action)
			}
		}
		size := image.Pt(gtx.Dp(unit.Dp(42)), gtx.Dp(unit.Dp(34)))
		gtx.Constraints = layout.Exact(size)
		return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.LabelOp(label).Add(gtx.Ops)
			if button.Hovered() {
				bg := u.colors.field
				if action == system.ActionClose {
					bg = u.colors.error
				}
				roundedSurface(gtx, size, 6, bg, u.colors.divider)
			}
			c := u.colors.text
			if action == system.ActionClose && button.Hovered() {
				c = argb(0xFFFFFFFF)
			}
			center := f32.Pt(float32(size.X)/2, float32(size.Y)/2)
			scale := float32(gtx.Dp(1))
			var path clip.Path
			path.Begin(gtx.Ops)
			line := func(x1, y1, x2, y2 float32) {
				path.MoveTo(center.Add(f32.Pt(x1*scale, y1*scale)))
				path.LineTo(center.Add(f32.Pt(x2*scale, y2*scale)))
			}
			switch action {
			case system.ActionMinimize:
				line(-5, 2, 5, 2)
			case system.ActionMaximize:
				line(-5, -5, 5, -5)
				line(5, -5, 5, 5)
				line(5, 5, -5, 5)
				line(-5, 5, -5, -5)
			case system.ActionClose:
				line(-5, -5, 5, 5)
				line(5, -5, -5, 5)
			}
			paint.FillShape(gtx.Ops, c, clip.Stroke{Path: path.End(), Width: scale}.Op())
			return layout.Dimensions{Size: size}
		})
	}
}
