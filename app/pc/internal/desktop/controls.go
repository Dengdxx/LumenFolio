package desktop

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func (u *ui) section(title string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.Body2(u.theme, title)
		label.Color = u.colors.muted
		return label.Layout(gtx)
	}
}

func (u *ui) button(label string, clickable *widget.Clickable, action func()) layout.Widget {
	return u.commandButton(label, clickable, action, false)
}

func (u *ui) primaryButton(label string, clickable *widget.Clickable, action func()) layout.Widget {
	return u.commandButton(label, clickable, action, true)
}

func (u *ui) commandButton(label string, clickable *widget.Clickable, action func(), primary bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// 忙碌时仍向 Gio 注册点击过滤器，避免恢复后的第一下鼠标点击丢失。
		if clickable.Clicked(gtx) && !u.fileBusy {
			action()
		}
		if u.fileBusy {
			gtx = gtx.Disabled()
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		button := material.Button(u.theme, clickable, label)
		button.Inset = layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10)}
		return u.styledButton(gtx, button, primary)
	}
}

func (u *ui) styledButton(gtx layout.Context, button material.ButtonStyle, primary bool) layout.Dimensions {
	button.CornerRadius = 8
	if !primary {
		button.Background = u.colors.surface
		button.Color = u.colors.onSelected
	}
	border := u.colors.outline
	if !gtx.Enabled() {
		button.Background = u.colors.surface
		button.Color = u.colors.muted
		border = u.colors.divider
	} else if gtx.Focused(button.Button) {
		border = u.colors.primary
	} else if primary {
		border = color.NRGBA{}
	}
	dims := button.Layout(gtx)
	strokeRounded(gtx, dims.Size, button.CornerRadius, border)
	return dims
}

// choice 在列表内撑满宽度，并沿用手机端选中色。
func (u *ui) choice(gtx layout.Context, clickable *widget.Clickable, label string, selected bool) layout.Dimensions {
	return u.choiceLayout(gtx, clickable, selected, func(gtx layout.Context, foreground color.NRGBA) layout.Dimensions {
		l := material.Body1(u.theme, label)
		l.Color = foreground
		return l.Layout(gtx)
	})
}

func (u *ui) choiceLayout(gtx layout.Context, clickable *widget.Clickable, selected bool, content func(layout.Context, color.NRGBA) layout.Dimensions) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return clickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg, fg := u.colors.surface, u.colors.text
		switch {
		case selected:
			bg, fg = u.colors.selected, u.colors.onSelected
		case clickable.Hovered():
			bg = u.colors.field
		}
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions { return content(gtx, fg) })
		call := macro.Stop()

		size := image.Point{X: gtx.Constraints.Max.X, Y: dims.Size.Y}
		border := color.NRGBA{}
		if gtx.Focused(clickable) {
			border = u.colors.primary
		}
		roundedSurface(gtx, size, 8, bg, border)
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

func (u *ui) tabButton(label string, clickable *widget.Clickable, active bool, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if clickable.Clicked(gtx) {
			action()
			// active 在本帧布局前计算，额外重绘一帧使高亮与切换后的模式同步。
			gtx.Execute(op.InvalidateCmd{})
		}
		return clickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			bg, fg, border := u.colors.surface, u.colors.muted, u.colors.outline
			if active {
				bg, fg, border = u.colors.selected, u.colors.onSelected, color.NRGBA{}
			} else if !gtx.Enabled() {
				border = u.colors.divider
			} else if clickable.Hovered() {
				bg = u.colors.field
			}
			if gtx.Focused(clickable) {
				border = u.colors.primary
			}
			macro := op.Record(gtx.Ops)
			dims := layout.Inset{Top: 6, Bottom: 6, Left: 12, Right: 12}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(u.theme, label)
					l.Color = fg
					return l.Layout(gtx)
				})
			call := macro.Stop()

			roundedSurface(gtx, dims.Size, 8, bg, border)
			call.Add(gtx.Ops)
			return dims
		})
	}
}

func (u *ui) hostField(gtx layout.Context) layout.Dimensions {
	if u.bluetoothConnected() {
		return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, u.section(u.deviceLabel()))
	}
	if u.upload.cancelSend != nil {
		gtx = gtx.Disabled()
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	editor := material.Editor(u.theme, &u.hostEditor, "192.168.137.42")
	editor.Color = u.colors.text
	editor.HintColor = u.colors.muted
	editor.TextSize = unit.Sp(14)
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Min
		border := u.colors.outline
		if !gtx.Enabled() {
			border = u.colors.divider
		} else if gtx.Focused(&u.hostEditor) {
			border = u.colors.primary
		}
		roundedSurface(gtx, size, 8, u.colors.field, border)
		return layout.Dimensions{Size: size}
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 6, Bottom: 6, Left: 8, Right: 8}.Layout(gtx, editor.Layout)
	})
}

func gap(dp unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Spacer{Height: dp, Width: dp}.Layout(gtx)
	}
}

func fill(gtx layout.Context, c color.NRGBA) {
	rect := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	rect.Pop()
}
