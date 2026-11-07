package panel

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestNewCropFitsPanel(t *testing.T) {
	// 宽图 (2400×1200)：应按宽度缩放 fitScale = 1200 / 2400 = 0.5
	wide := NewCrop(image.Rect(0, 0, 2400, 1200))
	if math.Abs(wide.Scale-0.5) > 1e-6 {
		t.Fatalf("宽图初始缩放比例应为 0.5，实际 %.4f", wide.Scale)
	}
	if wide.OX != 0 || wide.OY != 0 || wide.Angle != 0 {
		t.Fatalf("初始偏移和角度应为 0：ox=%.1f, oy=%.1f, angle=%.1f", wide.OX, wide.OY, wide.Angle)
	}

	// 高图 (600×1600)：应按高度缩放 fitScale = 1600 / 1600 = 1.0
	tall := NewCrop(image.Rect(0, 0, 600, 1600))
	if math.Abs(tall.Scale-1.0) > 1e-6 {
		t.Fatalf("高图初始缩放比例应为 1.0，实际 %.4f", tall.Scale)
	}
}

func TestMoveShiftsSample(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 400))
	// 在原图中心 (200, 200) 处画一个 40×40 的红色方块
	red := color.NRGBA{200, 0, 0, 255}
	for y := 180; y < 220; y++ {
		for x := 180; x < 220; x++ {
			src.SetNRGBA(x, y, red)
		}
	}
	c := Crop{
		OX:     0,
		OY:     0,
		Scale:  1.0,
		Angle:  0,
		Bounds: src.Bounds(),
	}
	// 初始状态下，红块正好在面板中心 (600, 800)
	dst := c.Sample(src)
	if got := dst.NRGBAAt(Width/2, Height/2); got.R < 180 {
		t.Fatalf("初始面板中心应为红色，实际 %v", got)
	}

	// 沿 x 轴向右移动图片 100 像素，红块在面板上的位置应右移到 700 处，中心 (600, 800) 变为白色留白
	c.Move(100, 0)
	dst = c.Sample(src)
	if got := dst.NRGBAAt(Width/2, Height/2); got != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("图片右移后面板中心应为透明区域叠白底的白色，实际 %v", got)
	}
	if got := dst.NRGBAAt(Width/2+100, Height/2); got.R < 180 {
		t.Fatalf("图片右移后 (700, 800) 应为红色，实际 %v", got)
	}
}

func TestZoomKeepsAnchor(t *testing.T) {
	c := NewCrop(image.Rect(0, 0, 1000, 1000))
	// 锚点取在面板上非中心位置 (300, 400)
	anchorX, anchorY := 300.0, 400.0

	// 放大前，锚点对应的原图坐标：
	sin, cos := math.Sincos(-c.Angle)
	cxImg := float64(c.Bounds.Dx()) / 2.0
	cyImg := float64(c.Bounds.Dy()) / 2.0
	pu := anchorX - float64(Width)/2.0 - c.OX
	pv := anchorY - float64(Height)/2.0 - c.OY
	rxBefore := pu*cos - pv*sin
	ryBefore := pu*sin + pv*cos
	imgXBefore := cxImg + rxBefore/c.Scale
	imgYBefore := cyImg + ryBefore/c.Scale

	// 执行缩放 1.5 倍
	c.Zoom(1.5, anchorX, anchorY)

	// 放大后，锚点对应的原图坐标必须相同
	puAfter := anchorX - float64(Width)/2.0 - c.OX
	pvAfter := anchorY - float64(Height)/2.0 - c.OY
	rxAfter := puAfter*cos - pvAfter*sin
	ryAfter := puAfter*sin + pvAfter*cos
	imgXAfter := cxImg + rxAfter/c.Scale
	imgYAfter := cyImg + ryAfter/c.Scale

	if math.Abs(imgXBefore-imgXAfter) > 1e-6 || math.Abs(imgYBefore-imgYAfter) > 1e-6 {
		t.Fatalf("缩放前后锚点原图坐标改变：(%.4f, %.4f) -> (%.4f, %.4f)", imgXBefore, imgYBefore, imgXAfter, imgYAfter)
	}
}
