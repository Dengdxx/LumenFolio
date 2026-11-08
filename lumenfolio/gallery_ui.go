package main

import (
	"context"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/gallery"
)

// galleryBrowser 的视图状态由窗口事件循环独占。后台请求只发送不可变的结果。
type galleryBrowser struct {
	openButton    widget.Clickable
	closeButton   widget.Clickable
	flashButton   widget.Clickable
	sdButton      widget.Clickable
	refreshButton widget.Clickable
	buttons       []widget.Clickable
	list          layout.List

	host     string
	source   gallery.Source
	pictures []gallery.Picture
	selected string
	preview  paint.ImageOp
	err      error
	request  context.Context
	cancel   context.CancelFunc
	results  chan galleryResult
}

type galleryResult struct {
	request  context.Context
	pictures []gallery.Picture
	preview  paint.ImageOp
	err      error
}

func (u *ui) openGallery() {
	host := strings.TrimSpace(u.hostEditor.Text())
	if host == "" {
		u.status = "先填写相框地址"
		return
	}
	u.gallery.host = host
	u.loadGallery(gallery.Flash)
	u.window.Invalidate()
}

func (u *ui) closeGallery() {
	g := &u.gallery
	g.cancelRequest()
	g.host = ""
	g.pictures = nil
	g.buttons = nil
	g.preview = paint.ImageOp{}
}

func (u *ui) loadGallery(source gallery.Source) {
	g := &u.gallery
	g.source = source
	g.pictures = nil
	g.buttons = nil
	g.selected = ""
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
	host, source := g.host, g.source
	u.startGalleryRequest(func(ctx context.Context) galleryResult {
		img, err := gallery.Preview(ctx, host, 8080, source, id)
		if err != nil {
			return galleryResult{err: err}
		}
		return galleryResult{preview: paint.NewImageOp(img)}
	})
}

// startGalleryRequest 替换正在进行的读取，后台发布结果后唤醒窗口。
func (u *ui) startGalleryRequest(run func(context.Context) galleryResult) {
	g := &u.gallery
	g.cancelRequest()
	g.err = nil
	g.preview = paint.ImageOp{}
	ctx, cancel := context.WithCancel(context.Background())
	g.request, g.cancel = ctx, cancel
	results, window := g.results, u.window
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
}

// applyResult 只接收当前请求的结果，避免切换来源后展示先前的列表或图片。
func (g *galleryBrowser) applyResult(result galleryResult) {
	if g.request != result.request {
		return
	}
	g.cancelRequest()
	g.err = result.err
	if g.selected != "" {
		g.preview = result.preview
		return
	}
	g.pictures = result.pictures
	g.buttons = make([]widget.Clickable, len(g.pictures))
}

func (u *ui) galleryLayout(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.galleryHeader),
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
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.tabButton("← 返回", &g.closeButton, false, u.closeGallery)),
			layout.Rigid(gap(12)),
			layout.Flexed(1, u.section("相框图片 · "+g.host)),
			layout.Rigid(u.tabButton("Flash artifacts", &g.flashButton, g.source == gallery.Flash, func() {
				if g.source != gallery.Flash {
					u.loadGallery(gallery.Flash)
				}
			})),
			layout.Rigid(gap(8)),
			layout.Rigid(u.tabButton("SD 卡", &g.sdButton, g.source == gallery.SD, func() {
				if g.source != gallery.SD {
					u.loadGallery(gallery.SD)
				}
			})),
			layout.Rigid(gap(12)),
			layout.Rigid(u.tabButton("刷新", &g.refreshButton, false, func() { u.loadGallery(g.source) })),
		)
	})
}

func (u *ui) galleryList(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(248))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	fill(gtx, panelColor)
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return g.list.Layout(gtx, len(g.pictures), func(gtx layout.Context, index int) layout.Dimensions {
			entry, button := g.pictures[index], &g.buttons[index]
			if button.Clicked(gtx) && (g.selected != entry.ID || g.err != nil) {
				u.loadGalleryPreview(entry.ID)
			}
			return u.choice(gtx, button, entry.Name, g.selected == entry.ID)
		})
	})
}

func (u *ui) galleryPreview(gtx layout.Context) layout.Dimensions {
	g := &u.gallery
	return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if g.preview.Size().X > 0 {
			return framePicture(gtx, g.preview)
		}
		message := "选择一张图片查看预览"
		switch {
		case g.err != nil:
			message = g.err.Error()
		case g.request != nil && g.selected == "":
			message = "正在读取图片列表…"
		case g.request != nil:
			message = "正在读取图片…"
		case len(g.pictures) == 0:
			message = "此来源暂无可预览图片"
		}
		label := material.Body1(u.theme, message)
		label.Color = mutedColor
		label.Alignment = text.Middle
		return layout.Center.Layout(gtx, label.Layout)
	})
}
