package main

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/font/gofont"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"lumenfolio/panel"
)

func newTestUI() *ui {
	return newUI(text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection())), "")
}

func TestCropPointerGestures(t *testing.T) {
	cases := []struct {
		name   string
		button pointer.Buttons
		start  f32.Point
		moves  []f32.Point
		pan    bool
		rotate bool
	}{
		{"左键平移", pointer.ButtonPrimary, f32.Pt(400, 260), []f32.Point{f32.Pt(420, 280), f32.Pt(440, 300), f32.Pt(460, 320)}, true, false},
		{"右键旋转", pointer.ButtonSecondary, f32.Pt(400, 260), []f32.Point{f32.Pt(420, 280), f32.Pt(440, 300), f32.Pt(460, 320)}, false, true},
		{"旋转把手", pointer.ButtonPrimary, f32.Pt(300, 6), []f32.Point{f32.Pt(340, 24), f32.Pt(380, 60), f32.Pt(410, 100)}, false, true},
		{"离开画布继续旋转", pointer.ButtonSecondary, f32.Pt(400, 260), []f32.Point{f32.Pt(420, 280), f32.Pt(440, 300), f32.Pt(900, 900)}, false, true},
		{"中键不操作取景", pointer.ButtonTertiary, f32.Pt(400, 260), []f32.Point{f32.Pt(420, 280), f32.Pt(440, 300)}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newTestUI()
			u.rawImage = image.NewNRGBA(image.Rect(0, 0, 120, 160))
			u.crop = panel.NewCrop(u.rawImage.Bounds())
			t.Cleanup(func() {
				if u.ditherTimer != nil {
					u.ditherTimer.Stop()
				}
			})
			var r input.Router
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(600, 600))}
			frame := func() {
				ops.Reset()
				u.canvasArea(gtx, u.cropView)
				r.Frame(&ops)
			}
			frame()
			r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: tc.button, Position: tc.start})
			frame()
			for _, p := range tc.moves {
				r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: tc.button, Position: p})
				frame()
			}
			if panned := u.crop.OX != 0 || u.crop.OY != 0; panned != tc.pan {
				t.Errorf("平移状态应为 %v，实际偏移 (%.2f, %.2f)", tc.pan, u.crop.OX, u.crop.OY)
			}
			if rotated := u.crop.Angle != 0; rotated != tc.rotate {
				t.Errorf("旋转状态应为 %v，实际角度 %.3f", tc.rotate, u.crop.Angle)
			}
			last := tc.moves[len(tc.moves)-1]
			r.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: last})
			frame()
			before := u.crop
			r.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: tc.start})
			frame()
			if u.crop != before {
				t.Fatal("松开鼠标后移动指针不应继续改变取景")
			}
		})
	}
}

func TestCanvasHeaderTabsSwitchMode(t *testing.T) {
	u := newTestUI()
	u.rawImage = image.NewNRGBA(image.Rect(0, 0, 120, 160))
	u.crop = panel.NewCrop(u.rawImage.Bounds())
	u.preview = image.NewNRGBA(image.Rect(0, 0, panel.Width, panel.Height))
	var r input.Router
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(1180, 760))}
	frame := func() {
		ops.Reset()
		u.layout(gtx)
		r.Frame(&ops)
	}
	frame()
	for _, click := range []struct {
		at   f32.Point
		want viewMode
	}{{f32.Pt(420, 20), modePreview}, {f32.Pt(300, 20), modeCrop}} {
		r.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: click.at},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: click.at},
		)
		frame()
		if u.mode != click.want {
			t.Fatalf("完整界面中点击 %v 后应切换到模式 %d，实际 %d", click.at, click.want, u.mode)
		}
	}
}

func TestModeTabsRespondToPointer(t *testing.T) {
	for _, target := range []viewMode{modeCrop, modePreview} {
		name := "取景选取"
		if target == modePreview {
			name = "六色预览"
		}
		t.Run(name, func(t *testing.T) {
			u := newTestUI()
			u.mode = modePreview
			button := &u.modeCropBtn
			if target == modePreview {
				u.mode = modeCrop
				button = &u.modePrevBtn
			}
			var r input.Router
			var ops op.Ops
			gtx := layout.Context{Ops: &ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(200, 60))}
			tab := u.tabButton(name, button, false, func() { u.mode = target })
			tab(gtx)
			r.Frame(&ops)
			r.WakeupTime()
			r.Queue(
				pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(10, 10)},
				pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(10, 10)},
			)
			ops.Reset()
			tab(gtx)
			r.Frame(&ops)
			if u.mode != target {
				t.Fatal("鼠标点击模式标签后应切换到对应模式")
			}
			if _, redraw := r.WakeupTime(); !redraw {
				t.Fatal("切换模式后应安排重绘，及时更新标签的选中样式")
			}
		})
	}
}

func TestOpenButtonReceivesClickInPreview(t *testing.T) {
	u := newTestUI()
	u.rawImage = image.NewNRGBA(image.Rect(0, 0, 120, 160))
	u.mode = modePreview
	var opened int
	u.openResult = make(chan openResult, 1)
	var r input.Router
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(1180, 760))}
	frame := func() {
		ops.Reset()
		u.layout(gtx)
		r.Frame(&ops)
	}
	frame()
	r.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(124, 80)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(124, 80)},
	)
	frame()
	select {
	case <-u.openResult:
		opened++
	default:
	}
	if opened != 1 {
		t.Fatalf("预览界面中打开按钮应收到 1 次点击，实际 %d", opened)
	}
}
