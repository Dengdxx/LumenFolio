package main

import (
	"context"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/gallery"
	"lumenfolio/ota"
)

// galleryBrowser 的视图状态由窗口事件循环独占。后台请求只发送不可变的结果。
type galleryBrowser struct {
	openButton    widget.Clickable
	closeButton   widget.Clickable
	flashButton   widget.Clickable
	sdButton      widget.Clickable
	refreshButton widget.Clickable
	displayButton widget.Clickable
	buttons       []widget.Clickable
	list          layout.List

	host          string
	source        gallery.Source
	pictures      []gallery.Picture
	thumbnails    map[string]paint.ImageOp
	selected      string
	preview       paint.ImageOp
	err           error
	displayStatus string
	displayError  bool
	request       context.Context
	kind          galleryRequestKind
	cancel        context.CancelFunc
	results       chan galleryResult
	storage       gallery.StorageUsage
	deviceStatus  string
	infoRequest   context.Context
	cancelInfo    context.CancelFunc
}

type galleryRequestKind uint8

const (
	galleryRead galleryRequestKind = iota
	galleryThumbnail
	galleryDisplay
)

type galleryResult struct {
	request  context.Context
	pictures []gallery.Picture
	preview  paint.ImageOp
	id       string
	err      error
	storage  gallery.StorageUsage
	status   string
}

func (u *ui) openGallery() {
	host := strings.TrimSpace(u.hostEditor.Text())
	if host == "" {
		u.status = "先填写相框地址"
		return
	}
	u.gallery.host = host
	u.loadGallery(gallery.Flash)
	u.loadGalleryInfo()
	u.window.Invalidate()
}

func (u *ui) closeGallery() {
	g := &u.gallery
	g.cancelRequest()
	g.cancelInfoRequest()
	g.host = ""
	g.pictures = nil
	g.buttons = nil
	g.thumbnails = nil
	g.preview = paint.ImageOp{}
	g.storage, g.deviceStatus = gallery.StorageUsage{}, ""
}

func (u *ui) loadGallery(source gallery.Source) {
	g := &u.gallery
	g.source = source
	g.pictures = nil
	g.buttons = nil
	g.thumbnails = nil
	g.selected = ""
	g.preview, g.err = paint.ImageOp{}, nil
	g.displayStatus, g.displayError = "", false
	g.list = layout.List{Axis: layout.Vertical}
	host := g.host
	u.startGalleryRequest(func(ctx context.Context) galleryResult {
		pictures, err := gallery.List(ctx, host, 8080, source)
		return galleryResult{pictures: pictures, err: err}
	})
}

func (u *ui) loadGalleryPreview(id string) {
	g := &u.gallery
	g.selected = id
	g.preview, g.err = paint.ImageOp{}, nil
	g.displayStatus, g.displayError = "", false
	host, source := g.host, g.source
	u.startGalleryRequest(func(ctx context.Context) galleryResult {
		img, err := gallery.Preview(ctx, host, 8080, source, id)
		if err != nil {
			return galleryResult{err: err}
		}
		return galleryResult{preview: paint.NewImageOp(img)}
	})
}

// loadVisibleThumbnail 在列表完成布局后按可见范围串行读取，给主动打开的预览和刷屏让路。
func (u *ui) loadVisibleThumbnail() {
	g := &u.gallery
	if g.request != nil {
		return
	}
	first, count := g.list.Position.First, g.list.Position.Count
	for _, entry := range g.pictures[first : first+count] {
		if _, loaded := g.thumbnails[entry.ID]; loaded {
			continue
		}
		host, source, id := g.host, g.source, entry.ID
		u.startGalleryRequest(func(ctx context.Context) galleryResult {
			pixels, err := gallery.Thumbnail(ctx, host, 8080, source, id)
			if err != nil {
				return galleryResult{id: id, err: err}
			}
			return galleryResult{id: id, preview: paint.NewImageOp(pixels)}
		})
		g.kind = galleryThumbnail
		return
	}
}

func (u *ui) displayGalleryPicture() {
	g := &u.gallery
	if g.selected == "" || g.preview.Size().X == 0 || g.kind == galleryDisplay {
		return
	}
	host, source, id := g.host, g.source, g.selected
	g.displayStatus, g.displayError = "正在安排显示…", false
	u.startGalleryRequest(func(ctx context.Context) galleryResult {
		return galleryResult{err: gallery.Display(ctx, host, 8080, source, id)}
	})
	g.kind = galleryDisplay
	u.window.Invalidate()
}

// loadGalleryInfo 读取容量和设备状态。两项各自处理错误，并独立于图片列表更新。
func (u *ui) loadGalleryInfo() {
	g := &u.gallery
	g.cancelInfoRequest()
	ctx, cancel := context.WithCancel(context.Background())
	g.infoRequest, g.cancelInfo = ctx, cancel
	host := g.host
	u.runGalleryRequest(ctx, func(ctx context.Context) galleryResult {
		usage, _ := gallery.Storage(ctx, host, 8080)
		status, _ := ota.DeviceStatus(ctx, host, 8080)
		return galleryResult{storage: usage, status: status}
	})
}

// startGalleryRequest 替换正在进行的请求，后台发布结果后唤醒窗口。
func (u *ui) startGalleryRequest(run func(context.Context) galleryResult) {
	g := &u.gallery
	g.cancelRequest()
	ctx, cancel := context.WithCancel(context.Background())
	g.request, g.cancel = ctx, cancel
	u.runGalleryRequest(ctx, run)
}

func (u *ui) runGalleryRequest(ctx context.Context, run func(context.Context) galleryResult) {
	results, window := u.gallery.results, u.window
	go func() {
		result := run(ctx)
		result.request = ctx
		select {
		case results <- result:
			window.Invalidate()
		case <-ctx.Done():
		}
	}()
}

func (g *galleryBrowser) cancelRequest() {
	if g.cancel != nil {
		g.cancel()
		g.request, g.cancel = nil, nil
	}
	g.kind = galleryRead
}

func (g *galleryBrowser) cancelInfoRequest() {
	if g.cancelInfo != nil {
		g.cancelInfo()
		g.infoRequest, g.cancelInfo = nil, nil
	}
}

// applyResult 只接收当前请求的结果，避免切换来源后展示先前的列表或图片。
func (g *galleryBrowser) applyResult(result galleryResult) {
	if g.infoRequest != nil && g.infoRequest == result.request {
		g.cancelInfoRequest()
		g.storage, g.deviceStatus = result.storage, result.status
		return
	}
	if g.request != result.request {
		return
	}
	kind := g.kind
	g.cancelRequest()
	switch kind {
	case galleryThumbnail:
		// 失败的条目保留占位，等刷新时重试，避免每一帧都重新发请求。
		g.thumbnails[result.id] = result.preview
		return
	case galleryDisplay:
		g.displayError = result.err != nil
		if g.displayError {
			g.displayStatus = "安排显示失败：" + result.err.Error()
		} else {
			g.displayStatus = "已安排显示，屏幕刷新约需 35 秒"
		}
		return
	}
	g.err = result.err
	if g.selected != "" {
		g.preview = result.preview
		return
	}
	g.pictures = result.pictures
	g.buttons = make([]widget.Clickable, len(g.pictures))
	g.thumbnails = make(map[string]paint.ImageOp, len(g.pictures))
}

func (u *ui) galleryLayout(gtx layout.Context) layout.Dimensions {
	// 保留正在提交的目标，等相框返回后再允许切换来源或图片。
	if u.gallery.kind == galleryDisplay {
		gtx = gtx.Disabled()
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.card(gtx, u.galleryHeader)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(u.galleryList),
				layout.Flexed(1, u.galleryPreview),
			)
		}),
	)
}

func (u *ui) galleryHeader(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	title := material.Subtitle1(u.theme, "相框图片 · "+g.host)
	title.MaxLines = 1
	flashUsage, sdUsage := g.storage.Flash.String(), g.storage.SD.String()
	status := g.deviceStatus
	if g.infoRequest != nil {
		flashUsage, sdUsage = "正在读取容量…", "正在读取容量…"
		status = "正在读取设备状态…"
	} else if status == "" {
		status = "设备状态不可用"
	}
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.tabButton("← 返回", &g.closeButton, false, u.closeGallery)),
			layout.Rigid(gap(12)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(title.Layout),
					layout.Rigid(gap(4)),
					layout.Rigid(u.section(status)),
				)
			}),
			layout.Rigid(u.gallerySourceButton("Flash artifacts", flashUsage, &g.flashButton, gallery.Flash)),
			layout.Rigid(gap(8)),
			layout.Rigid(u.gallerySourceButton("SD 卡", sdUsage, &g.sdButton, gallery.SD)),
			layout.Rigid(gap(12)),
			layout.Rigid(u.tabButton("刷新", &g.refreshButton, false, func() {
				u.loadGallery(g.source)
				u.loadGalleryInfo()
			})),
		)
	})
}

func (u *ui) gallerySourceButton(label, usage string, button *widget.Clickable, source gallery.Source) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		capacity := material.Caption(u.theme, usage)
		capacity.Color = u.colors.muted
		capacity.MaxLines = 1
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.tabButton(label, button, u.gallery.source == source, func() {
				if u.gallery.source != source {
					u.loadGallery(source)
				}
			})),
			layout.Rigid(gap(4)),
			layout.Rigid(capacity.Layout),
		)
	}
}

func (u *ui) galleryList(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(248))
	gtx.Constraints.Min = gtx.Constraints.Max
	return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			dims := g.list.Layout(gtx, len(g.pictures), func(gtx layout.Context, index int) layout.Dimensions {
				entry, button := g.pictures[index], &g.buttons[index]
				if button.Clicked(gtx) && (g.selected != entry.ID || g.err != nil) {
					u.loadGalleryPreview(entry.ID)
				}
				thumbnail, loaded := g.thumbnails[entry.ID]
				return u.choiceLayout(gtx, button, g.selected == entry.ID, func(gtx layout.Context, foreground color.NRGBA) layout.Dimensions {
					label := material.Body1(u.theme, entry.Name)
					label.Color = foreground
					label.MaxLines = 2
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.galleryThumbnail(gtx, thumbnail, loaded) }),
						layout.Rigid(gap(12)),
						layout.Flexed(1, label.Layout),
					)
				})
			})
			u.loadVisibleThumbnail()
			return dims
		})
	})
}

func (u *ui) galleryThumbnail(gtx layout.Context, thumbnail paint.ImageOp, loaded bool) layout.Dimensions {
	size := image.Pt(gtx.Dp(54), gtx.Dp(72))
	gtx.Constraints = layout.Exact(size)
	defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(4)).Push(gtx.Ops).Pop()
	if thumbnail.Size().X > 0 {
		return widget.Image{Src: thumbnail, Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
	}
	fill(gtx, u.colors.field)
	message := "等待读取"
	if loaded {
		message = "读取失败"
	}
	label := material.Caption(u.theme, message)
	label.Color = u.colors.muted
	return layout.Center.Layout(gtx, label.Layout)
}

func (u *ui) galleryPreview(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if g.preview.Size().X > 0 {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return framePicture(gtx, g.preview) }),
				layout.Rigid(gap(12)),
				layout.Rigid(u.galleryDisplayControls),
			)
		}
		message := "选择一张图片查看预览"
		switch {
		case g.err != nil:
			message = g.err.Error()
		case g.request != nil && g.kind == galleryRead && g.selected == "":
			message = "正在读取图片列表…"
		case g.request != nil:
			message = "正在读取图片…"
		case len(g.pictures) == 0:
			message = "此来源暂无可预览图片"
		}
		label := material.Body1(u.theme, message)
		label.Color = u.colors.muted
		if g.err != nil {
			label.Color = u.colors.error
		}
		label.Alignment = text.Middle
		return layout.Center.Layout(gtx, label.Layout)
	})
}

func (u *ui) galleryDisplayControls(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(320))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.primaryButton("在相框上显示", &g.displayButton, u.displayGalleryPicture)),
			layout.Rigid(gap(6)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				label := material.Body2(u.theme, g.displayStatus)
				label.Color = u.colors.muted
				if g.displayError {
					label.Color = u.colors.error
				}
				label.Alignment = text.Middle
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return label.Layout(gtx)
			}),
		)
	})
}
