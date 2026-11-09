package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
)

func TestNewCropFitsPanel(t *testing.T) {
	// 宽图 (2400×1200)：应按宽度缩放 fitScale = 1200 / 2400 = 0.5
	wide := newCrop(image.Rect(0, 0, 2400, 1200))
	if math.Abs(wide.scale-0.5) > 1e-6 {
		t.Fatalf("宽图初始缩放比例应为 0.5，实际 %.4f", wide.scale)
	}
	if wide.ox != 0 || wide.oy != 0 || wide.angle != 0 {
		t.Fatalf("初始偏移和角度应为 0：ox=%.1f, oy=%.1f, angle=%.1f", wide.ox, wide.oy, wide.angle)
	}

	// 高图 (600×1600)：应按高度缩放 fitScale = 1600 / 1600 = 1.0
	tall := newCrop(image.Rect(0, 0, 600, 1600))
	if math.Abs(tall.scale-1.0) > 1e-6 {
		t.Fatalf("高图初始缩放比例应为 1.0，实际 %.4f", tall.scale)
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
	c := crop{
		ox:     0,
		oy:     0,
		scale:  1.0,
		angle:  0,
		bounds: src.Bounds(),
	}
	// 初始状态下，红块正好在面板中心 (600, 800)
	dst := c.sample(src)
	if got := dst.NRGBAAt(panelWidth/2, panelHeight/2); got.R < 180 {
		t.Fatalf("初始面板中心应为红色，实际 %v", got)
	}

	// 沿 x 轴向右移动图片 100 像素，红块在面板上的位置应右移到 700 处，中心 (600, 800) 变为白色留白
	c.move(100, 0)
	dst = c.sample(src)
	if got := dst.NRGBAAt(panelWidth/2, panelHeight/2); got != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("图片右移后面板中心应为透明区域叠白底的白色，实际 %v", got)
	}
	if got := dst.NRGBAAt(panelWidth/2+100, panelHeight/2); got.R < 180 {
		t.Fatalf("图片右移后 (700, 800) 应为红色，实际 %v", got)
	}
}

func TestZoomKeepsAnchor(t *testing.T) {
	c := newCrop(image.Rect(0, 0, 1000, 1000))
	// 锚点取在面板上非中心位置 (300, 400)
	anchorX, anchorY := 300.0, 400.0

	// 放大前，锚点对应的原图坐标：
	sin, cos := math.Sincos(-c.angle)
	cxImg := float64(c.bounds.Dx()) / 2.0
	cyImg := float64(c.bounds.Dy()) / 2.0
	pu := anchorX - float64(panelWidth)/2.0 - c.ox
	pv := anchorY - float64(panelHeight)/2.0 - c.oy
	rxBefore := pu*cos - pv*sin
	ryBefore := pu*sin + pv*cos
	imgXBefore := cxImg + rxBefore/c.scale
	imgYBefore := cyImg + ryBefore/c.scale

	// 执行缩放 1.5 倍
	c.zoom(1.5, anchorX, anchorY)

	// 放大后，锚点对应的原图坐标必须相同
	puAfter := anchorX - float64(panelWidth)/2.0 - c.ox
	pvAfter := anchorY - float64(panelHeight)/2.0 - c.oy
	rxAfter := puAfter*cos - pvAfter*sin
	ryAfter := puAfter*sin + pvAfter*cos
	imgXAfter := cxImg + rxAfter/c.scale
	imgYAfter := cyImg + ryAfter/c.scale

	if math.Abs(imgXBefore-imgXAfter) > 1e-6 || math.Abs(imgYBefore-imgYAfter) > 1e-6 {
		t.Fatalf("缩放前后锚点原图坐标改变：(%.4f, %.4f) -> (%.4f, %.4f)", imgXBefore, imgYBefore, imgXAfter, imgYAfter)
	}
}

func TestPanByScreenDelta(t *testing.T) {
	c := newCrop(image.Rect(0, 0, 1200, 1600))
	frame := image.Rect(100, 100, 400, 500) // 300×400，屏幕与面板比例 0.25
	c.panByScreenDelta(20, -10, frame)
	if math.Abs(c.ox-80) > 1e-9 || math.Abs(c.oy+40) > 1e-9 {
		t.Fatalf("屏幕位移应换算为面板 (80,-40)，实际 (%.2f,%.2f)", c.ox, c.oy)
	}
	// 没有拖动位移时不得改变图片位置。
	c.panByScreenDelta(0, 0, frame)
	if c.ox != 80 || c.oy != -40 {
		t.Fatal("零位移改变了图片位置")
	}
}

func TestDragIgnoresCaptureJump(t *testing.T) {
	c := newCrop(image.Rect(0, 0, 1200, 1600))
	frame := image.Rect(100, 100, 400, 500)
	var g dragGesture
	handle := f32.Pt(250, 70)
	press := pointer.Event{Kind: pointer.Press, Position: f32.Pt(250, 300), Buttons: pointer.ButtonPrimary}
	if g.apply(&c, press, frame, image.Pt(250, 300), handle, 18) || c.ox != 0 || c.oy != 0 {
		t.Fatal("按下不应移动图片")
	}
	jump := pointer.Event{Kind: pointer.Drag, Position: f32.Pt(20, 40), Buttons: pointer.ButtonPrimary}
	if g.apply(&c, jump, frame, image.Pt(250, 300), handle, 18) || c.ox != 0 || c.oy != 0 {
		t.Fatal("捕获后的首个拖拽事件不应移动图片")
	}
	real := pointer.Event{Kind: pointer.Drag, Position: f32.Pt(30, 50), Buttons: pointer.ButtonPrimary}
	if !g.apply(&c, real, frame, image.Pt(250, 300), handle, 18) {
		t.Fatal("首帧之后的真实拖动应改变图片")
	}
	if math.Abs(c.ox-40) > 1e-9 || math.Abs(c.oy-40) > 1e-9 {
		t.Fatalf("只应计入首帧后的 (10,10) 屏幕位移，实际 (%.2f,%.2f)", c.ox, c.oy)
	}
}

func TestDragIgnoresOtherPointers(t *testing.T) {
	c := newCrop(image.Rect(0, 0, panelWidth, panelHeight))
	frame := image.Rect(100, 100, 400, 500)
	center := image.Pt(250, 300)
	handle := f32.Pt(250, 70)
	var g dragGesture
	apply := func(e pointer.Event) bool { return g.apply(&c, e, frame, center, handle, 18) }
	apply(pointer.Event{Kind: pointer.Press, PointerID: 1, Buttons: pointer.ButtonPrimary, Position: f32.Pt(250, 300)})
	apply(pointer.Event{Kind: pointer.Drag, PointerID: 1, Position: f32.Pt(260, 300)})
	for _, e := range []pointer.Event{
		{Kind: pointer.Press, PointerID: 2, Source: pointer.Touch, Position: f32.Pt(300, 350)},
		{Kind: pointer.Drag, PointerID: 2, Source: pointer.Touch, Position: f32.Pt(400, 450)},
		{Kind: pointer.Release, PointerID: 2, Source: pointer.Touch},
	} {
		if apply(e) || c.ox != 0 || c.oy != 0 || c.angle != 0 {
			t.Fatal("其他指针不应改变或终止当前拖拽")
		}
	}
	if !apply(pointer.Event{Kind: pointer.Drag, PointerID: 1, Position: f32.Pt(270, 310)}) || c.ox != 40 || c.oy != 40 {
		t.Fatal("原指针应继续平移图片")
	}
	apply(pointer.Event{Kind: pointer.Cancel})
	if apply(pointer.Event{Kind: pointer.Drag, PointerID: 1, Position: f32.Pt(280, 320)}) || c.ox != 40 || c.oy != 40 {
		t.Fatal("取消捕获后不应继续改变取景")
	}
}

func TestRotateByPointer(t *testing.T) {
	c := newCrop(image.Rect(0, 0, 1200, 1600))
	center := image.Pt(500, 400)
	last := f32.Pt(600, 400)    // 中心右侧，角度 0
	current := f32.Pt(500, 300) // 中心上方，角度 -90°
	got := c.rotateByPointer(last, current, center)
	if math.Abs(got+math.Pi/2) > 1e-9 {
		t.Fatalf("当前指针角度应为 -π/2，实际 %.4f", got)
	}
	if math.Abs(c.angle+math.Pi/2) > 1e-9 {
		t.Fatalf("图片角度应为 -π/2，实际 %.4f", c.angle)
	}
	// 相同位置重复拖动不能产生额外角度。
	c.rotateByPointer(current, current, center)
	if math.Abs(c.angle+math.Pi/2) > 1e-9 {
		t.Fatal("相同指针位置重复拖动改变了角度")
	}
}

func TestRotateAroundCenter(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for y := range 200 {
		for x := range 200 {
			src.SetNRGBA(x, y, color.NRGBA{byte(x), byte(y), 0, 255})
		}
	}
	c := crop{ox: 0, oy: 0, scale: 1.0, angle: 0, bounds: src.Bounds()}
	straight := c.sample(src)

	// 旋转 90 度后图像必定发生改变
	c.rotateAroundFrameCenter(90)
	turned90 := c.sample(src)
	if bytesEqual(straight.Pix, turned90.Pix) {
		t.Fatal("旋转 90 度后图像采样应改变")
	}

	// 累计旋转 4 次 90 度（整整一圈 360 度），图像必须精确还原
	c.rotateAroundFrameCenter(90)
	c.rotateAroundFrameCenter(90)
	c.rotateAroundFrameCenter(90)
	turned360 := c.sample(src)
	if !bytesEqual(straight.Pix, turned360.Pix) {
		t.Fatal("旋转 360 度后图像应与原图完全一致")
	}
}

func TestSampleCompositesTransparencyOnWhite(t *testing.T) {
	cases := []struct {
		name string
		src  color.NRGBA
		want color.NRGBA
	}{
		{"透明黑", color.NRGBA{0, 0, 0, 0}, color.NRGBA{255, 255, 255, 255}},
		{"透明红", color.NRGBA{255, 0, 0, 0}, color.NRGBA{255, 255, 255, 255}},
		{"半透明白", color.NRGBA{255, 255, 255, 128}, color.NRGBA{255, 255, 255, 255}},
		{"半透明红", color.NRGBA{255, 0, 0, 128}, color.NRGBA{255, 127, 127, 255}},
		{"不透明色", color.NRGBA{42, 84, 126, 255}, color.NRGBA{42, 84, 126, 255}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := image.NewNRGBA(image.Rect(0, 0, 16, 16))
			draw.Draw(src, src.Bounds(), image.NewUniform(tc.src), image.Point{}, draw.Src)
			dst := newCrop(src.Bounds()).sample(src)
			if got := dst.NRGBAAt(panelWidth/2, panelHeight/2); got != tc.want {
				t.Fatalf("叠在白底后应为 %v，实际 %v", tc.want, got)
			}
		})
	}
}

func TestSampleOutsideIsWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	c := crop{ox: 0, oy: 0, scale: 1.0, angle: 0, bounds: src.Bounds()}
	dst := c.sample(src)
	// 原图只有 100×100，面板四个角必定为留白纯白色 (255, 255, 255)
	corner := dst.NRGBAAt(0, 0)
	if corner.R != 255 || corner.G != 255 || corner.B != 255 {
		t.Fatalf("留白处应为纯白，实际 %v", corner)
	}
}

func TestFrameRectOnScreen(t *testing.T) {
	view := image.Rect(0, 0, 1000, 800)
	rect := frameRectOnScreen(view, 50)
	// 可用区域 900×700。700 高度限制下，宽应为 700 * 3/4 = 525
	if rect.Dy() != 700 {
		t.Fatalf("高度应贴紧 700，实际 %d", rect.Dy())
	}
	if rect.Dx() != 525 {
		t.Fatalf("宽度应为 525，实际 %d", rect.Dx())
	}
	// 检查是否居中
	cx := rect.Min.X + rect.Dx()/2
	cy := rect.Min.Y + rect.Dy()/2
	if cx != 500 || cy != 400 {
		t.Fatalf("没有在 view 中居中：(%d, %d)", cx, cy)
	}

	// 测试 screenToPanel 转换
	p1 := f32.Pt(float32(rect.Min.X), float32(rect.Min.Y))
	px, py := screenToPanel(p1, rect)
	if math.Abs(px) > 1e-6 || math.Abs(py) > 1e-6 {
		t.Fatalf("左上角映射到面板应为 (0,0)，实际 (%.2f, %.2f)", px, py)
	}
	p2 := f32.Pt(float32(rect.Max.X), float32(rect.Max.Y))
	px2, py2 := screenToPanel(p2, rect)
	if math.Abs(px2-1200) > 1e-4 || math.Abs(py2-1600) > 1e-4 {
		t.Fatalf("右下角映射到面板应为 (1200, 1600)，实际 (%.2f, %.2f)", px2, py2)
	}
}
