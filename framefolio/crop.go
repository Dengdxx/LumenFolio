package main

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/io/pointer"
)

// 面板是 1200×1600 竖屏。
// 取景框在屏幕中央保持固定（比例严格为 1200:1600），用户通过平移、旋转和滚轮缩放操作背后的原图。
const cropAspect = float64(panelWidth) / float64(panelHeight)

// crop 记录背后的原图相对于固定取景框的变换状态。
// 所有坐标均以 1200×1600 面板像素为基准：
// 面板中心为 (600, 800)；
// ox, oy 是原图中心相对于面板中心的平移偏移量；
// scale 是面板像素与原图像素的比例（scale 越大，原图在框内显示得越大）；
// angle 是原图逆时针旋转的弧度。
type crop struct {
	ox, oy float64 // 原图中心相对面板中心的偏移（面板像素）
	scale  float64 // 面板像素 / 原图像素
	angle  float64 // 旋转弧度（逆时针）
	bounds image.Rectangle
}

// newCrop 创建初始取景状态。默认等比缩放到恰好完整容纳在 1200×1600 内，居中，不旋转。
func newCrop(bounds image.Rectangle) crop {
	w := float64(bounds.Dx())
	h := float64(bounds.Dy())
	if w <= 0 || h <= 0 {
		return crop{scale: 1, bounds: bounds}
	}
	// 初始缩放：等比适应面板（留白模式），让用户能先看到完整原图
	scale := math.Min(float64(panelWidth)/w, float64(panelHeight)/h)
	return crop{
		ox:     0,
		oy:     0,
		scale:  scale,
		angle:  0,
		bounds: bounds,
	}
}

// maxCropSize 返回能放进给定可用矩形的最大 1200:1600 面板比例尺寸。
func maxCropSize(width, height int) (int, int) {
	w := float64(width)
	h := w / cropAspect
	if h > float64(height) {
		h = float64(height)
		w = h * cropAspect
	}
	return int(math.Round(w)), int(math.Round(h))
}

// move 在面板像素单位下平移背后的图片。
func (c *crop) move(dx, dy float64) {
	c.ox += dx
	c.oy += dy
}

// rotateAroundFrameCenter 围绕面板中心旋转原图（度为单位）。
func (c *crop) rotateAroundFrameCenter(degrees float64) {
	dAngle := degrees * math.Pi / 180
	c.angle += dAngle
	// 原图中心 (ox, oy) 围绕面板中心 (0, 0) 旋转
	sin, cos := math.Sincos(dAngle)
	nox := c.ox*cos - c.oy*sin
	noy := c.ox*sin + c.oy*cos
	c.ox = nox
	c.oy = noy
}

// zoom 缩放背后的图片，保持面板上的锚点 anchor（以面板像素坐标，(0,0)为面板左上角）在原图上不动。
// factor 大于 1 放大图片，小于 1 缩小图片。
func (c *crop) zoom(factor float64, anchorX, anchorY float64) {
	if factor <= 0 {
		return
	}
	// 锚点相对于当前原图中心的向量
	curCenterX := float64(panelWidth)/2.0 + c.ox
	curCenterY := float64(panelHeight)/2.0 + c.oy
	rx := anchorX - curCenterX
	ry := anchorY - curCenterY

	newScale := c.scale * factor
	// 限制缩放范围在 0.05 到 50 之间
	minScale := 0.05 * math.Min(float64(panelWidth)/float64(c.bounds.Dx()), float64(panelHeight)/float64(c.bounds.Dy()))
	maxScale := 50.0 * math.Max(float64(panelWidth)/float64(c.bounds.Dx()), float64(panelHeight)/float64(c.bounds.Dy()))
	if newScale < minScale {
		newScale = minScale
	}
	if newScale > maxScale {
		newScale = maxScale
	}
	actualFactor := newScale / c.scale
	c.scale = newScale

	// 锚点不动：anchor - newCenter = (anchor - curCenter) * actualFactor
	// => newCenter = anchor - (anchor - curCenter) * actualFactor
	newCenterX := anchorX - rx*actualFactor
	newCenterY := anchorY - ry*actualFactor
	c.ox = newCenterX - float64(panelWidth)/2.0
	c.oy = newCenterY - float64(panelHeight)/2.0
}

// sample 从变换后的原图采样出 1200×1600 面板分辨率像素缓冲。
// 框外或留白区域自动填充纯白 (255, 255, 255)，透明像素也先与白底合成。
func (c crop) sample(src image.Image) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, panelWidth, panelHeight))
	sin, cos := math.Sincos(-c.angle)
	cxImg := float64(c.bounds.Min.X) + float64(c.bounds.Dx())/2.0
	cyImg := float64(c.bounds.Min.Y) + float64(c.bounds.Dy())/2.0

	for y := range panelHeight {
		pv := float64(y) + 0.5 - float64(panelHeight)/2.0 - c.oy
		for x := range panelWidth {
			pu := float64(x) + 0.5 - float64(panelWidth)/2.0 - c.ox
			// 逆旋转
			rx := pu*cos - pv*sin
			ry := pu*sin + pv*cos
			// 缩放回原图像素
			ix := cxImg + rx/c.scale
			iy := cyImg + ry/c.scale

			r, g, b := sampleBilinear(src, ix-0.5, iy-0.5)
			i := dst.PixOffset(x, y)
			dst.Pix[i] = r
			dst.Pix[i+1] = g
			dst.Pix[i+2] = b
			dst.Pix[i+3] = 255
		}
	}
	return dst
}

// sampleBilinear 双线性插值取色，超出原图边界处返回纯白。
func sampleBilinear(src image.Image, x, y float64) (byte, byte, byte) {
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	r00, g00, b00 := pixelOrWhite(src, x0, y0)
	r10, g10, b10 := pixelOrWhite(src, x0+1, y0)
	r01, g01, b01 := pixelOrWhite(src, x0, y0+1)
	r11, g11, b11 := pixelOrWhite(src, x0+1, y0+1)
	mix := func(a, b, c, d float64) byte {
		top := a*(1-fx) + b*fx
		bottom := c*(1-fx) + d*fx
		return byte(math.Round(top*(1-fy) + bottom*fy))
	}
	return mix(r00, r10, r01, r11), mix(g00, g10, g01, g11), mix(b00, b10, b01, b11)
}

// pixelOrWhite 将预乘 alpha 的颜色叠在白底后取色，使采样与取景视图的透明度合成一致。
func pixelOrWhite(src image.Image, x, y int) (float64, float64, float64) {
	if !image.Pt(x, y).In(src.Bounds()) {
		return 255, 255, 255
	}
	r, g, b, a := src.At(x, y).RGBA()
	white := 0xffff - a
	return float64((r + white) >> 8), float64((g + white) >> 8), float64((b + white) >> 8)
}

// frameRectOnScreen 计算固定取景框在当前可用区域 viewRect 中居中且等比最大化的屏幕像素矩形。
func frameRectOnScreen(viewRect image.Rectangle, margin int) image.Rectangle {
	availW := viewRect.Dx() - margin*2
	availH := viewRect.Dy() - margin*2
	if availW <= 0 || availH <= 0 {
		return viewRect
	}
	fw, fh := maxCropSize(availW, availH)
	x0 := (viewRect.Min.X + viewRect.Dx()/2) - fw/2
	y0 := (viewRect.Min.Y + viewRect.Dy()/2) - fh/2
	return image.Rect(x0, y0, x0+fw, y0+fh)
}

// screenToPanel 把屏幕坐标点转换为面板像素坐标点（0..1200, 0..1600）。
func screenToPanel(p f32.Point, frameRect image.Rectangle) (float64, float64) {
	sScreen := float64(frameRect.Dx()) / float64(panelWidth)
	if sScreen <= 0 {
		return 0, 0
	}
	px := float64(p.X-float32(frameRect.Min.X)) / sScreen
	py := float64(p.Y-float32(frameRect.Min.Y)) / sScreen
	return px, py
}

// panByScreenDelta 把屏幕上的鼠标位移换算成面板像素后平移图片。
// 单独处理位移而不对坐标取绝对差，避免拖拽首帧坐标跳变被误当作移动。
func (c *crop) panByScreenDelta(dx, dy float32, frameRect image.Rectangle) {
	if frameRect.Dx() <= 0 {
		return
	}
	sScreen := float64(frameRect.Dx()) / float64(panelWidth)
	c.move(float64(dx)/sScreen, float64(dy)/sScreen)
}

// rotateByPointer 围绕固定取景框中心旋转图片，只使用相邻两次指针角度之差。
func (c *crop) rotateByPointer(last, current f32.Point, center image.Point) float64 {
	lastAngle := math.Atan2(float64(last.Y-float32(center.Y)), float64(last.X-float32(center.X)))
	curAngle := math.Atan2(float64(current.Y-float32(center.Y)), float64(current.X-float32(center.X)))
	delta := curAngle - lastAngle
	c.rotateAroundFrameCenter(delta * 180 / math.Pi)
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
func (g *dragGesture) apply(c *crop, e pointer.Event, frame image.Rectangle, center image.Point, handle f32.Point, handleRadius float64) bool {
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
			c.panByScreenDelta(e.Position.X-g.last.X, e.Position.Y-g.last.Y, frame)
		case dragRotate:
			c.rotateByPointer(g.last, e.Position, center)
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
