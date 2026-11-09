// Package desktop 实现 LumenFolio 的 Gio 窗口和页面。
package desktop

import (
	"image"
	"log"
	"os"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"

	"lumenfolio/panel"
)

// Run 在主 goroutine 启动界面。只能由进程入口调用；命令行路径不得进入 Gio 事件循环。
//
// 提供取景、六色预览、上传和离线导出；设备通信在后台执行，不阻塞窗口事件循环。
func Run(initialFile string) int {
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
	colors      appColors
	wordmark    paint.ImageOp
	explorer    *explorer.Explorer
	window      windowInvalidator
	initialFile string

	// 按钮
	openButton   widget.Clickable
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

	mode      viewMode
	crop      panel.Crop
	alg       int
	rawImage  image.Image  // 打开的原始图片
	source    *image.NRGBA // 按取景框采样出的 1200×1600 图
	preview   *image.NRGBA // 抖动结果
	name      string
	status    string
	rendering bool // 当前构图和算法的六色预览正在计算
	fileBusy  bool // 文件操作与升级互斥，由 UI 事件循环维护
	upload    uploadPanel
	gallery   galleryBrowser
	ota       otaPage

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

func newUI(shaper *text.Shaper, initialFile string) *ui {
	theme := material.NewTheme()
	theme.Shaper = shaper
	colors := phoneColors(systemPrefersDark())
	theme.Palette = material.Palette{
		Bg:         colors.surface,
		Fg:         colors.text,
		ContrastBg: colors.primary,
		ContrastFg: colors.onPrimary,
	}
	ui := &ui{
		theme:       theme,
		colors:      colors,
		wordmark:    tintBrandWordmark(colors.text),
		initialFile: initialFile,
		algButtons:  make([]widget.Clickable, len(panel.Algorithms())),
		algList:     layout.List{Axis: layout.Vertical},
		mode:        modeCrop,
		status:      "打开一张图片，通过取景框旋转、平移和滚轮缩放选取构图。",
		openResult:  make(chan openResult, 1),
		done:        make(chan ditherResult, 1),
		ditherReq:   make(chan struct{}, 1),
		jobResult:   make(chan string, 1),
		upload: uploadPanel{
			storageResults: make(chan uploadStorageResult, 1),
			updates:        make(chan uploadUpdate, 1),
		},
		gallery: galleryBrowser{results: make(chan galleryResult, 1)},
		ota:     otaPage{updates: make(chan otaUpdate, 1)},
	}
	ui.hostEditor.SingleLine = true
	return ui
}

func (u *ui) run(w *app.Window) error {
	defer u.closeGallery()
	defer u.upload.close()
	u.window = w
	u.explorer = explorer.NewExplorer(w)
	if u.initialFile != "" {
		u.fileBusy = true
		u.status = "正在打开图片"
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
		if u.applyPendingResults() {
			w.Invalidate()
		}
		evt := w.Event()
		u.explorer.ListenEvents(evt)
		switch evt := evt.(type) {
		case app.DestroyEvent:
			return evt.Err
		case app.FrameEvent:
			// 文件选择可能在等待这个鼠标帧时结束，先应用结果再派发点击。
			u.applyPendingResults()
			gtx := app.NewContext(&ops, evt)
			u.layout(gtx)
			evt.Frame(gtx.Ops)
		}
	}
}

// applyPendingResults 由窗口事件循环消费后台结果。多次唤醒可能合并为一帧，等待窗口事件前取完就绪结果。
func (u *ui) applyPendingResults() (changed bool) {
	for {
		select {
		case result := <-u.openResult:
			u.fileBusy = false
			changed = true
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
		case <-u.ditherReq:
			u.triggerDither()
			changed = true
		case result := <-u.done:
			if result.version == u.cropVersion && result.alg == u.alg {
				u.rendering = false
				u.source = result.src
				u.preview = result.img
				if !u.fileBusy {
					u.status = u.name + " · " + panel.Algorithms()[u.alg].Name()
				}
				changed = true
			}
		case text := <-u.jobResult:
			u.fileBusy = false
			u.status = text
			changed = true
		case result := <-u.upload.storageResults:
			changed = u.upload.applyStorage(result) || changed
		case update := <-u.upload.updates:
			u.upload.progress = update.progress
			if update.finished {
				u.upload.cancelSend()
				u.upload.cancelSend = nil
				u.fileBusy = false
				u.status = update.progress.Message
			}
			changed = true
		case result := <-u.gallery.results:
			if u.gallery.applyResult(result) {
				u.loadGallery(u.gallery.source)
				u.loadGalleryInfo()
				u.gallery.actionStatus = "已删除图片"
			}
			changed = true
		case update := <-u.ota.updates:
			u.ota.progress = update.progress
			if update.firmware != nil {
				u.ota.firmware = *update.firmware
			}
			if update.finished {
				u.fileBusy = false
			}
			changed = true
		default:
			return changed
		}
	}
}
