package main

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/io/pointer"

	"lumenfolio/panel"
)

// frameRectOnScreen 计算固定取景框在当前可用区域 viewRect 中居中且等比最大化的屏幕像素矩形。
func frameRectOnScreen(viewRect image.Rectangle, margin int) image.Rectangle {
	availW := viewRect.Dx() - margin*2
	availH := viewRect.Dy() - margin*2
	if availW <= 0 || availH <= 0 {
		return viewRect
	}
	fw, fh := panel.MaxFrameSize(availW, availH)
	x0 := (viewRect.Min.X + viewRect.Dx()/2) - fw/2
	y0 := (viewRect.Min.Y + viewRect.Dy()/2) - fh/2
	return image.Rect(x0, y0, x0+fw, y0+fh)
}

// screenToPanel 把屏幕坐标点转换为面板像素坐标点（0..1200, 0..1600）。
func screenToPanel(p f32.Point, frameRect image.Rectangle) (float64, float64) {
	sScreen := float64(frameRect.Dx()) / float64(panel.Width)
	if sScreen <= 0 {
		return 0, 0
	}
	px := float64(p.X-float32(frameRect.Min.X)) / sScreen
	py := float64(p.Y-float32(frameRect.Min.Y)) / sScreen
	return px, py
}

// panByScreenDelta 把屏幕上的鼠标位移换算成面板像素后平移图片。
// 单独处理位移而不对坐标取绝对差，避免拖拽首帧坐标跳变被误当作移动。
func panByScreenDelta(c *panel.Crop, dx, dy float32, frameRect image.Rectangle) {
	if frameRect.Dx() <= 0 {
		return
	}
	sScreen := float64(frameRect.Dx()) / float64(panel.Width)
	c.Move(float64(dx)/sScreen, float64(dy)/sScreen)
}

// rotateByPointer 围绕固定取景框中心旋转图片，只使用相邻两次指针角度之差。
func rotateByPointer(c *panel.Crop, last, current f32.Point, center image.Point) float64 {
	lastAngle := math.Atan2(float64(last.Y-float32(center.Y)), float64(last.X-float32(center.X)))
	curAngle := math.Atan2(float64(current.Y-float32(center.Y)), float64(current.X-float32(center.X)))
	delta := curAngle - lastAngle
	c.RotateAroundFrameCenter(delta * 180 / math.Pi)
	return curAngle
}

// dragGesture 只跟踪一次拖拽的指针，忽略其他指针和鼠标中键。
// armed 为 false 时，下一次拖动只作为捕获后的坐标基准。
type dragGesture struct {
	mode      dragMode
	pointerID pointer.ID
	armed     bool
	last      f32.Point
}

// apply 处理一个指针事件。返回 true 表示取景状态发生了变化。
func (g *dragGesture) apply(c *panel.Crop, e pointer.Event, frame image.Rectangle, center image.Point, handle f32.Point, handleRadius float64) bool {
	switch e.Kind {
	case pointer.Press:
		if g.mode != dragNone || (e.Source != pointer.Touch && e.Buttons != pointer.ButtonPrimary && e.Buttons != pointer.ButtonSecondary) {
			return false
		}
		g.pointerID = e.PointerID
		dist := math.Hypot(float64(e.Position.X-handle.X), float64(e.Position.Y-handle.Y))
		if dist <= handleRadius || e.Buttons == pointer.ButtonSecondary {
			g.mode = dragRotate
		} else {
			g.mode = dragPan
		}
		g.armed = false
		g.last = e.Position
		return false
	case pointer.Drag:
		if g.mode == dragNone || e.PointerID != g.pointerID {
			return false
		}
		if !g.armed {
			g.armed = true
			g.last = e.Position
			return false
		}
		switch g.mode {
		case dragPan:
			panByScreenDelta(c, e.Position.X-g.last.X, e.Position.Y-g.last.Y, frame)
		case dragRotate:
			rotateByPointer(c, g.last, e.Position, center)
		default:
			return false
		}
		g.last = e.Position
		return true
	case pointer.Release:
		if e.PointerID != g.pointerID {
			return false
		}
		g.mode = dragNone
		g.armed = false
	case pointer.Cancel:
		g.mode = dragNone
		g.armed = false
	}
	return false
}
