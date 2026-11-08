package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/opentype"
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
	"gioui.org/x/explorer"

	"lumenfolio/panel"
)

// 界面用的颜色。墨水的预览色另见 inks，不从这里取。
var (
	bgColor     = color.NRGBA{24, 24, 27, 255}
	panelColor  = color.NRGBA{38, 38, 43, 255}
	textColor   = color.NRGBA{232, 232, 235, 255}
	mutedColor  = color.NRGBA{150, 150, 158, 255}
	accentColor = color.NRGBA{96, 165, 250, 255}
	frameColor  = color.NRGBA{210, 210, 205, 255}
)

type viewMode int

const (
	modeCrop viewMode = iota
	modePreview
)

type dragMode int

const (
	dragNone dragMode = iota
	dragPan
	dragRotate
)

// runGUI 在主 goroutine 启动界面。只能由进程入口调用；命令行路径不得进入 Gio 事件循环。
//
// 提供取景、六色预览、上传和离线导出；设备通信在后台执行，不阻塞窗口事件循环。
func runGUI(initialFile string) int {
	shaper := loadShaper()
	ui := newUI(shaper, initialFile)
	go func() {
		w := new(app.Window)
		w.Option(app.Title("LumenFolio"), app.Size(unit.Dp(1180), unit.Dp(760)), app.MinSize(unit.Dp(900), unit.Dp(600)))
		if err := ui.run(w); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
	return 0
}

// loadShaper 用系统的微软雅黑。找不到时退回 Gio 默认字体，中文会显示成方框。
func loadShaper() *text.Shaper {
	data, err := os.ReadFile(`C:\Windows\Fonts\msyh.ttc`)
	if err != nil {
		log.Println("没找到微软雅黑，中文无法显示：", err)
		return text.NewShaper()
	}
	faces, err := opentype.ParseCollection(data)
	if err != nil || len(faces) == 0 {
		log.Println("微软雅黑解析失败：", err)
		return text.NewShaper()
	}
	return text.NewShaper(text.WithCollection([]font.FontFace{faces[0]}))
}

// windowInvalidator 是后台任务跨 goroutine 请求重绘的窗口边界。
// Invalidate 必须可并发调用，并唤醒窗口的 Event 等待。
type windowInvalidator interface {
	Invalidate()
}

type ui struct {
	theme       *material.Theme
	explorer    *explorer.Explorer
	window      windowInvalidator
	initialFile string

	// 按钮
	openButton   widget.Clickable
	uploadButton widget.Clickable
	exportButton widget.Clickable
	hostEditor   widget.Editor
	modeCropBtn  widget.Clickable
	modePrevBtn  widget.Clickable
	rotLeftBtn   widget.Clickable
	rotRightBtn  widget.Clickable
	resetCropBtn widget.Clickable
	toPreviewBtn widget.Clickable
	toCropBtn    widget.Clickable
	algButtons   []widget.Clickable
	algList      layout.List

	mode     viewMode
	crop     panel.Crop
	alg      int
	rawImage image.Image  // 打开的原始图片
	source   *image.NRGBA // 按取景框采样出的 1200×1600 图
	preview  *image.NRGBA // 抖动结果
	name     string
	status   string
	busy     bool

	// 交互状态
	canvasTag bool
	gesture   dragGesture
	moved     bool

	cropVersion int
	ditherTimer *time.Timer

	rawImageOp    *paint.ImageOp
	rawImageOpSrc image.Image

	openResult chan openResult
	done       chan ditherResult
	ditherReq  chan struct{}
	jobResult  chan string
}

type openResult struct {
	name string
	img  image.Image
	err  error
}

type ditherResult struct {
	version int
	alg     int
	src     *image.NRGBA
	img     *image.NRGBA
}

func newUI(shaper *text.Shaper, initialFile string) *ui {
	theme := material.NewTheme()
	theme.Shaper = shaper
	theme.Palette = material.Palette{
		Bg:         panelColor,
		Fg:         textColor,
		ContrastBg: accentColor,
		ContrastFg: color.NRGBA{15, 15, 18, 255},
	}
	ui := &ui{
		theme:       theme,
		initialFile: initialFile,
		algButtons:  make([]widget.Clickable, len(panel.Algorithms())),
		algList:     layout.List{Axis: layout.Vertical},
		mode:        modeCrop,
		status:      "打开一张图片，通过取景框旋转、平移和滚轮缩放选取构图。",
		openResult:  make(chan openResult, 1),
		done:        make(chan ditherResult, 1),
		ditherReq:   make(chan struct{}, 1),
		jobResult:   make(chan string, 1),
	}
	ui.hostEditor.SingleLine = true
	return ui
}

func (u *ui) run(w *app.Window) error {
	u.window = w
	u.explorer = explorer.NewExplorer(w)
	if u.initialFile != "" {
		go func(path string) {
			f, err := os.Open(path)
			if err != nil {
				u.postOpenResult(openResult{err: err})
				return
			}
			u.postOpenResult(u.readImage(f))
		}(u.initialFile)
	}
	var ops op.Ops
	for {
		select {
		case result := <-u.openResult:
			u.busy = false
			switch {
			case result.err != nil:
				u.status = "没打开图片：" + result.err.Error()
			case result.img == nil:
				u.status = "已取消选择"
			default:
				u.rawImage = result.img
				u.name = result.name
				u.crop = panel.NewCrop(result.img.Bounds())
				u.mode = modeCrop
				u.rawImageOp = nil
				u.rawImageOpSrc = nil
				u.triggerDither()
			}
			w.Invalidate()
		case <-u.ditherReq:
			u.triggerDither()
		case result := <-u.done:
			if result.version == u.cropVersion && result.alg == u.alg {
				u.busy = false
				u.source = result.src
				u.preview = result.img
				u.status = u.name + " · " + panel.Algorithms()[u.alg].Name()
				w.Invalidate()
			}
		case text := <-u.jobResult:
			u.busy = false
			u.status = text
			w.Invalidate()
		default:
		}

		evt, ok := w.Event().(event.Event)
		if !ok {
			return nil
		}
		u.explorer.ListenEvents(evt)
		switch evt := evt.(type) {
		case app.DestroyEvent:
			return evt.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, evt)
			u.layout(gtx)
			evt.Frame(gtx.Ops)
		}
	}
}

// scheduleDither 在拖拽和滚轮操作时做防抖，停顿 120ms 后触发重采样与抖动。
func (u *ui) scheduleDither() {
	if u.ditherTimer != nil {
		u.ditherTimer.Stop()
	}
	u.ditherTimer = time.AfterFunc(120*time.Millisecond, func() {
		select {
		case u.ditherReq <- struct{}{}:
		default:
		}
		if u.window != nil {
			u.window.Invalidate()
		}
	})
}

// triggerDither 立即按当前取景框在后台采样并进行抖动计算。
func (u *ui) triggerDither() {
	if u.rawImage == nil {
		return
	}
	if u.ditherTimer != nil {
		u.ditherTimer.Stop()
	}
	u.cropVersion++
	version := u.cropVersion
	alg := u.alg
	c := u.crop
	img := u.rawImage
	u.preview = nil
	u.busy = true
	u.status = "正在用「" + panel.Algorithms()[alg].Name() + "」采样并抖动 " + u.name
	go func() {
		src := c.Sample(img)
		res := panel.Algorithms()[alg].Dither(src)
		// 由 UI goroutine 按版本丢弃过期结果；队列满时等待，不能把最新结果直接丢掉。
		u.done <- ditherResult{version: version, alg: alg, src: src, img: res}
		if u.window != nil {
			u.window.Invalidate()
		}
	}()
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	paint.Fill(gtx.Ops, bgColor)
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(u.sidebar),
		layout.Flexed(1, u.canvas),
	)
}

func (u *ui) sidebar(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(248))
	fill(gtx, panelColor)
	return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
			layout.Rigid(u.section("相框地址")),
			layout.Rigid(gap(6)),
			layout.Rigid(u.hostField),
			layout.Rigid(gap(8)),
			layout.Rigid(u.button("上传到相框", &u.uploadButton, u.onUpload)),
			layout.Rigid(gap(8)),
			layout.Rigid(u.button("导出帧文件", &u.exportButton, u.onExport)),
		)
	})
}

func (u *ui) heading(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := material.H6(u.theme, "相框预览")
			label.Color = textColor
			return label.Layout(gtx)
		}),
		layout.Rigid(gap(2)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := material.Body2(u.theme, "1200×1600 · 六色")
			label.Color = mutedColor
			return label.Layout(gtx)
		}),
	)
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
					b.CornerRadius = unit.Dp(4)
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return b.Layout(gtx)
				}),
				layout.Rigid(gap(6)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.rotRightBtn.Clicked(gtx) {
						u.crop.RotateAroundFrameCenter(90)
						u.triggerDither()
					}
					b := material.Button(u.theme, &u.rotRightBtn, "↻ +90°")
					b.CornerRadius = unit.Dp(4)
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return b.Layout(gtx)
				}),
				layout.Rigid(gap(6)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if u.resetCropBtn.Clicked(gtx) {
						u.crop = panel.NewCrop(u.rawImage.Bounds())
						u.triggerDither()
					}
					b := material.Button(u.theme, &u.resetCropBtn, "复位")
					b.CornerRadius = unit.Dp(4)
					b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}
					return b.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(gap(14)),
	)
}

func (u *ui) section(title string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := material.Body2(u.theme, title)
		label.Color = mutedColor
		return label.Layout(gtx)
	}
}

func (u *ui) button(label string, clickable *widget.Clickable, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if clickable.Clicked(gtx) {
			action()
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		button := material.Button(u.theme, clickable, label)
		button.CornerRadius = unit.Dp(6)
		button.Inset = layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(10), Right: unit.Dp(10)}
		return button.Layout(gtx)
	}
}

func (u *ui) algorithmList(gtx layout.Context) layout.Dimensions {
	algs := panel.Algorithms()
	return u.algList.Layout(gtx, len(algs), func(gtx layout.Context, index int) layout.Dimensions {
		return u.algorithm(index, algs[index].Name())(gtx)
	})
}

func (u *ui) algorithm(index int, name string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if u.algButtons[index].Clicked(gtx) && index != u.alg && !u.busy {
			u.alg = index
			u.triggerDither()
		}
		return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return u.choice(gtx, &u.algButtons[index], name, index == u.alg)
		})
	}
}

// choice 是一个可选中的条目。宽度撑满侧边栏，选中时左侧加一条强调色。
func (u *ui) choice(gtx layout.Context, clickable *widget.Clickable, label string, selected bool) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return clickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg := panelColor
		switch {
		case selected:
			bg = color.NRGBA{52, 64, 84, 255}
		case clickable.Hovered():
			bg = color.NRGBA{48, 48, 54, 255}
		}
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(12), Right: unit.Dp(12)}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				l := material.Body1(u.theme, label)
				l.Color = textColor
				return l.Layout(gtx)
			})
		call := macro.Stop()

		size := image.Point{X: gtx.Constraints.Max.X, Y: dims.Size.Y}
		rect := clip.RRect{Rect: image.Rectangle{Max: size}, SE: 6, SW: 6, NW: 6, NE: 6}
		paint.FillShape(gtx.Ops, bg, rect.Op(gtx.Ops))
		if selected {
			bar := clip.Rect{Max: image.Point{X: gtx.Dp(unit.Dp(3)), Y: size.Y}}.Push(gtx.Ops)
			paint.ColorOp{Color: accentColor}.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			bar.Pop()
		}
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

func (u *ui) swatches(gtx layout.Context) layout.Dimensions {
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
		size := gtx.Dp(unit.Dp(26))
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{X: size, Y: size}
				gtx.Constraints.Max = gtx.Constraints.Min
				rect := clip.RRect{Rect: image.Rectangle{Max: gtx.Constraints.Min}, SE: 4, SW: 4, NW: 4, NE: 4}
				paint.FillShape(gtx.Ops, ink.Preview, rect.Op(gtx.Ops))
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(gap(3)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.Caption(u.theme, ink.Name)
				label.Color = mutedColor
				return label.Layout(gtx)
			}),
		)
	}
}

func (u *ui) canvas(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.canvasHeader),
		layout.Flexed(1, u.canvasBody),
		layout.Rigid(u.statusBar),
	)
}

// canvasHeader 显示取景/预览模式切换与快捷操作。
func (u *ui) canvasHeader(gtx layout.Context) layout.Dimensions {
	if u.rawImage == nil {
		return layout.Dimensions{}
	}
	fill(gtx, panelColor)
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(u.tabButton(" ✂️ 取景选取 ", &u.modeCropBtn, u.mode == modeCrop, func() {
					u.mode = modeCrop
				})),
				layout.Rigid(gap(6)),
				layout.Rigid(u.tabButton(" 🖼️ 六色预览 ", &u.modePrevBtn, u.mode == modePreview, func() {
					u.mode = modePreview
					if u.preview == nil && !u.busy {
						u.triggerDither()
					}
				})),
				layout.Rigid(gap(16)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					hint := "🖱️ 鼠标左键平移 · 滚轮缩放 · 顶部把手或右键旋转"
					if u.mode == modePreview {
						hint = "当前显示 1200×1600 面板的六色墨水效果"
					}
					lbl := material.Body2(u.theme, hint)
					lbl.Color = mutedColor
					return lbl.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.mode == modeCrop {
						if u.toPreviewBtn.Clicked(gtx) {
							u.mode = modePreview
							if u.preview == nil && !u.busy {
								u.triggerDither()
							}
						}
						btn := material.Button(u.theme, &u.toPreviewBtn, "查看效果 →")
						btn.CornerRadius = unit.Dp(4)
						btn.Inset = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}
						return btn.Layout(gtx)
					}
					if u.toCropBtn.Clicked(gtx) {
						u.mode = modeCrop
					}
					btn := material.Button(u.theme, &u.toCropBtn, "← 调整取景")
					btn.CornerRadius = unit.Dp(4)
					btn.Inset = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}
					return btn.Layout(gtx)
				}),
			)
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
			bg := color.NRGBA{48, 48, 54, 255}
			fg := mutedColor
			if active {
				bg = color.NRGBA{52, 64, 84, 255}
				fg = textColor
			}
			macro := op.Record(gtx.Ops)
			dims := layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(10), Right: unit.Dp(10)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(u.theme, label)
					l.Color = fg
					return l.Layout(gtx)
				})
			call := macro.Stop()

			rect := clip.RRect{Rect: image.Rectangle{Max: dims.Size}, SE: 4, SW: 4, NW: 4, NE: 4}
			paint.FillShape(gtx.Ops, bg, rect.Op(gtx.Ops))
			if active {
				line := clip.Rect{Min: image.Pt(0, dims.Size.Y-gtx.Dp(unit.Dp(2))), Max: dims.Size}.Push(gtx.Ops)
				paint.ColorOp{Color: accentColor}.Add(gtx.Ops)
				paint.PaintOp{}.Add(gtx.Ops)
				line.Pop()
			}
			call.Add(gtx.Ops)
			return dims
		})
	}
}

func (u *ui) canvasBody(gtx layout.Context) layout.Dimensions {
	if u.rawImage == nil {
		message := "还没有图片，点击左侧「打开图片」"
		label := material.H6(u.theme, message)
		label.Color = mutedColor
		label.Alignment = text.Middle
		return layout.Center.Layout(gtx, label.Layout)
	}
	if u.mode == modeCrop {
		return u.cropView(gtx)
	}
	return u.picture(gtx)
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

	// 顶部固定旋转把手位置（在固定取景框顶部上方 24dp）
	topCenter := f32.Pt(float32(fc.X), float32(frameRect.Min.Y))
	handlePos := topCenter.Sub(f32.Pt(0, float32(gtx.Dp(unit.Dp(24)))))

	// 2. 注册交互区域与指针事件处理
	defer clip.Rect(viewRect).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &u.canvasTag)

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
		drawLine(f32.Pt(ox, oy), f32.Pt(ox+dx*l, oy), accentColor, w)
		drawLine(f32.Pt(ox, oy), f32.Pt(ox, oy+dy*l), accentColor, w)
	}
	cornerBracket(float32(frameRect.Min.X), float32(frameRect.Min.Y), 1, 1)
	cornerBracket(float32(frameRect.Max.X), float32(frameRect.Min.Y), -1, 1)
	cornerBracket(float32(frameRect.Max.X), float32(frameRect.Max.Y), -1, -1)
	cornerBracket(float32(frameRect.Min.X), float32(frameRect.Max.Y), 1, -1)

	// 9. 绘制顶部固定旋转把手与连线
	drawLine(topCenter, handlePos, accentColor, float32(gtx.Dp(unit.Dp(1.5))))
	hr := gtx.Dp(unit.Dp(7))
	hrect := image.Rect(int(handlePos.X)-hr, int(handlePos.Y)-hr, int(handlePos.X)+hr, int(handlePos.Y)+hr)
	paint.FillShape(gtx.Ops, accentColor, clip.Ellipse(hrect).Op(gtx.Ops))
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
			if u.busy {
				message = "正在采样并计算抖动…"
			}
			label := material.H6(u.theme, message)
			label.Color = mutedColor
			label.Alignment = text.Middle
			return layout.Center.Layout(gtx, label.Layout)
		}
		max := gtx.Constraints.Max
		width := max.X
		height := width * panel.Height / panel.Width
		if height > max.Y {
			height = max.Y
			width = height * panel.Width / panel.Height
		}
		imageOp := paint.NewImageOp(u.preview)
		imageOp.Filter = paint.FilterNearest
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{X: width, Y: height}
			gtx.Constraints.Max = gtx.Constraints.Min
			frame := clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops)
			paint.ColorOp{Color: frameColor}.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			widget.Image{Src: imageOp, Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
			frame.Pop()
			return layout.Dimensions{Size: gtx.Constraints.Min}
		})
	})
}

func (u *ui) statusBar(gtx layout.Context) layout.Dimensions {
	fill(gtx, panelColor)
	return layout.Inset{Top: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					label := material.Body2(u.theme, u.status)
					label.Color = mutedColor
					return label.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if u.rawImage == nil {
						return layout.Dimensions{}
					}
					b := u.rawImage.Bounds()
					info := fmt.Sprintf("原图: %d×%d", b.Dx(), b.Dy())
					label := material.Caption(u.theme, info)
					label.Color = mutedColor
					return label.Layout(gtx)
				}),
			)
		})
}

func (u *ui) onOpen() {
	if u.busy {
		return
	}
	go func() {
		file, err := u.explorer.ChooseFile(".png", ".jpg", ".jpeg")
		switch {
		case err == explorer.ErrUserDecline:
			u.postOpenResult(openResult{})
		case err != nil:
			u.postOpenResult(openResult{err: err})
		default:
			u.postOpenResult(u.readImage(file))
		}
	}()
}

// postOpenResult 在结果入队后唤醒窗口，成功、取消和错误都不能依赖下一次鼠标事件才被处理。
// UI 状态仍只由事件循环修改；后台 goroutine 只负责交付结果和请求重绘。
func (u *ui) postOpenResult(result openResult) {
	u.openResult <- result
	if u.window != nil {
		u.window.Invalidate()
	}
}

// readImage 解码选中的文件。文件名取不到时给一个占位名。
func (u *ui) readImage(file io.ReadCloser) openResult {
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return openResult{err: err}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return openResult{err: err}
	}
	name := "未命名图片"
	if named, ok := file.(interface{ Name() string }); ok && named.Name() != "" {
		name = named.Name()
	}
	return openResult{name: name, img: img}
}

func (u *ui) hostField(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	editor := material.Editor(u.theme, &u.hostEditor, "192.168.137.42")
	editor.Color = textColor
	editor.HintColor = mutedColor
	editor.TextSize = unit.Sp(14)
	return editor.Layout(gtx)
}

func (u *ui) postJob(text string) {
	u.jobResult <- text
	if u.window != nil {
		u.window.Invalidate()
	}
}

func (u *ui) onUpload() {
	if u.rawImage == nil {
		u.status = "先打开一张图片"
		return
	}
	host := u.hostEditor.Text()
	if host == "" {
		u.status = "先填写相框地址"
		return
	}
	if u.busy {
		return
	}
	u.busy = true
	u.status = "正在把当前构图发到 " + host
	alg, crop, src := u.alg, u.crop, u.rawImage
	go func() {
		reply, err := sendPicture(context.Background(), host, 8080, alg, src, crop)
		if err != nil {
			u.postJob("没发出去：" + err.Error())
			return
		}
		u.postJob(reply)
	}()
}

func (u *ui) onExport() {
	if u.rawImage == nil {
		u.status = "先打开一张图片"
		return
	}
	if u.explorer == nil || u.busy {
		return
	}
	u.busy = true
	u.status = "选择帧文件的保存位置"
	alg, crop, src := u.alg, u.crop, u.rawImage
	go func() {
		file, err := u.explorer.CreateFile("frame.bin")
		switch {
		case err == explorer.ErrUserDecline:
			u.postJob("已取消导出")
		case err != nil:
			u.postJob("没能保存：" + err.Error())
		default:
			u.postJob(writeExport(file, alg, src, crop))
		}
	}()
}

func writeExport(file io.WriteCloser, alg int, src image.Image, crop panel.Crop) string {
	defer file.Close()
	encoded, err := encodePicture(alg, src, crop)
	if err != nil {
		return "没能导出：" + err.Error()
	}
	if _, err := file.Write(encoded); err != nil {
		return "没能导出：" + err.Error()
	}
	return fmt.Sprintf("已导出 %d 字节的帧文件", len(encoded))
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

// 保证 jpeg、png 的解码器被注册。
var (
	_ = jpeg.DefaultQuality
	_ = png.Encoder{}
)
