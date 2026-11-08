package panel

import (
	"image"
	"math"
)

// 面板是 1200×1600 竖屏。
// 取景框在屏幕中央保持固定（比例严格为 1200:1600），用户通过平移、旋转和滚轮缩放操作背后的原图。
const cropAspect = float64(Width) / float64(Height)

// Crop 记录背后的原图相对于固定取景框的变换状态。
// 所有坐标均以 1200×1600 面板像素为基准：
// 面板中心为 (600, 800)；
// ox, oy 是原图中心相对于面板中心的平移偏移量；
// scale 是面板像素与原图像素的比例（scale 越大，原图在框内显示得越大）；
// angle 是原图逆时针旋转的弧度。
type Crop struct {
	OX, OY float64 // 原图中心相对面板中心的偏移（面板像素）
	Scale  float64 // 面板像素 / 原图像素
	Angle  float64 // 旋转弧度（逆时针）
	Bounds image.Rectangle
}

// NewCrop 创建初始取景状态。默认等比缩放到恰好完整容纳在 1200×1600 内，居中，不旋转。
func NewCrop(bounds image.Rectangle) Crop {
	w := float64(bounds.Dx())
	h := float64(bounds.Dy())
	if w <= 0 || h <= 0 {
		return Crop{Scale: 1, Bounds: bounds}
	}
	// 初始缩放：等比适应面板（留白模式），让用户能先看到完整原图
	scale := math.Min(float64(Width)/w, float64(Height)/h)
	return Crop{Scale: scale, Bounds: bounds}
}

// MaxFrameSize 返回能放进给定可用矩形的最大 1200:1600 面板比例尺寸。
func MaxFrameSize(width, height int) (int, int) {
	w := float64(width)
	h := w / cropAspect
	if h > float64(height) {
		h = float64(height)
		w = h * cropAspect
	}
	return int(math.Round(w)), int(math.Round(h))
}

// Move 在面板像素单位下平移背后的图片。
func (c *Crop) Move(dx, dy float64) {
	c.OX += dx
	c.OY += dy
}

// RotateAroundFrameCenter 围绕面板中心旋转原图（度为单位）。
func (c *Crop) RotateAroundFrameCenter(degrees float64) {
	dAngle := degrees * math.Pi / 180
	c.Angle += dAngle
	// 原图中心 (ox, oy) 围绕面板中心 (0, 0) 旋转
	sin, cos := math.Sincos(dAngle)
	nox := c.OX*cos - c.OY*sin
	noy := c.OX*sin + c.OY*cos
	c.OX = nox
	c.OY = noy
}

// Zoom 缩放背后的图片，保持面板上的锚点 anchor（以面板像素坐标，(0,0)为面板左上角）在原图上不动。
// factor 大于 1 放大图片，小于 1 缩小图片。
func (c *Crop) Zoom(factor float64, anchorX, anchorY float64) {
	if factor <= 0 {
		return
	}
	// 锚点相对于当前原图中心的向量
	curCenterX := float64(Width)/2.0 + c.OX
	curCenterY := float64(Height)/2.0 + c.OY
	rx := anchorX - curCenterX
	ry := anchorY - curCenterY

	newScale := c.Scale * factor
	// 限制缩放范围在 0.05 到 50 之间
	minScale := 0.05 * math.Min(float64(Width)/float64(c.Bounds.Dx()), float64(Height)/float64(c.Bounds.Dy()))
	maxScale := 50.0 * math.Max(float64(Width)/float64(c.Bounds.Dx()), float64(Height)/float64(c.Bounds.Dy()))
	if newScale < minScale {
		newScale = minScale
	}
	if newScale > maxScale {
		newScale = maxScale
	}
	actualFactor := newScale / c.Scale
	c.Scale = newScale

	// 锚点不动：anchor - newCenter = (anchor - curCenter) * actualFactor
	// => newCenter = anchor - (anchor - curCenter) * actualFactor
	newCenterX := anchorX - rx*actualFactor
	newCenterY := anchorY - ry*actualFactor
	c.OX = newCenterX - float64(Width)/2.0
	c.OY = newCenterY - float64(Height)/2.0
}

// Sample 从变换后的原图采样出 1200×1600 面板分辨率像素缓冲。
// 框外或留白区域自动填充纯白 (255, 255, 255)，透明像素也先与白底合成。
func (c Crop) Sample(src image.Image) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	sin, cos := math.Sincos(-c.Angle)
	cxImg := float64(c.Bounds.Min.X) + float64(c.Bounds.Dx())/2.0
	cyImg := float64(c.Bounds.Min.Y) + float64(c.Bounds.Dy())/2.0

	for y := range Height {
		pv := float64(y) + 0.5 - float64(Height)/2.0 - c.OY
		for x := range Width {
			pu := float64(x) + 0.5 - float64(Width)/2.0 - c.OX
			// 逆旋转
			rx := pu*cos - pv*sin
			ry := pu*sin + pv*cos
			// 缩放回原图像素
			ix := cxImg + rx/c.Scale
			iy := cyImg + ry/c.Scale

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
