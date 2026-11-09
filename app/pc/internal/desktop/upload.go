package desktop

import (
	"context"
	"fmt"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/frame"
	"lumenfolio/gallery"
	"lumenfolio/identity"
	"lumenfolio/internal/picture"
)

type uploadPanel struct {
	host        string
	sdAvailable bool
	preferFlash bool
	probeFailed bool
	probe       context.Context
	cancelProbe context.CancelFunc

	progress   frame.Progress
	cancelSend context.CancelFunc

	storageResults chan uploadStorageResult
	updates        chan uploadUpdate

	flashButton, sdButton, probeButton, sendButton widget.Clickable
}

type uploadStorageResult struct {
	probe       context.Context
	sdAvailable bool
	id          identity.ID
	err         error
}

type uploadUpdate struct {
	progress frame.Progress
	finished bool
}

func (p *uploadPanel) target() frame.Target {
	if p.sdAvailable && !p.preferFlash {
		return frame.SD
	}
	return frame.Flash
}

func (p *uploadPanel) stopProbe() {
	if p.cancelProbe != nil {
		p.cancelProbe()
		p.probe, p.cancelProbe = nil, nil
	}
}

func (p *uploadPanel) close() {
	p.stopProbe()
	if p.cancelSend != nil {
		p.cancelSend()
	}
}

func (u *ui) applyStorage(result uploadStorageResult) bool {
	p := &u.upload
	if result.probe != p.probe {
		return false
	}
	p.stopProbe()
	p.probeFailed = result.err != nil
	p.sdAvailable = !p.probeFailed && result.sdAvailable
	if result.err == nil {
		u.rememberFrame(result.id, p.host)
	}
	return true
}

func (u *ui) syncUploadHost() {
	host := u.deviceHost()
	p := &u.upload
	if host != p.host {
		p.host = host
		p.preferFlash = false
		p.sdAvailable = false
		p.probeFailed = false
		u.checkUploadStorage(350 * time.Millisecond)
	}
}

// checkUploadStorage 在地址稳定后读取容量，切换地址时取消原请求。
func (u *ui) checkUploadStorage(delay time.Duration) {
	p := &u.upload
	p.stopProbe()
	if p.host == "" {
		return
	}
	host := p.host
	ctx, cancel := context.WithCancel(context.Background())
	p.probe, p.cancelProbe = ctx, cancel
	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return
			}
		}
		storage, err := gallery.Storage(ctx, host, 8080)
		var id identity.ID
		if err == nil {
			id, err = u.identifyWiFi(ctx, host)
		}
		result := uploadStorageResult{probe: ctx, sdAvailable: storage.SD.TotalBytes != 0, id: id, err: err}
		select {
		case p.storageResults <- result:
			if u.window != nil {
				u.window.Invalidate()
			}
		case <-ctx.Done():
		}
	}()
}

func (u *ui) onUpload() {
	if u.fileBusy {
		return
	}
	if u.rawImage == nil {
		u.status = "先打开一张图片"
		return
	}
	u.syncUploadHost()
	p := &u.upload
	if p.host == "" {
		u.status = "先填写相框地址"
		return
	}
	if p.probe != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelSend = cancel
	u.fileBusy = true
	p.progress = frame.Progress{Message: "正在编码画面…"}
	host, target, name := p.host, p.target(), u.name
	alg, crop, src := u.alg, u.crop, u.rawImage
	go func() {
		var last frame.Progress
		reply, err := picture.SendTo(ctx, host, 8080, alg, src, crop, target, name, func(progress frame.Progress) {
			last = progress
			u.postUpload(ctx, uploadUpdate{progress: progress})
		})
		if err != nil {
			last.Message = "上传未完成：" + err.Error()
		} else {
			last.Message = reply + "。屏幕刷新约需 35 秒。"
			if last.Percent == 100 {
				last.Message = "100% · " + last.Message
			}
		}
		u.postUpload(ctx, uploadUpdate{progress: last, finished: true})
	}()
}

// postUpload 由单个上传任务顺序发布，窗口忙时保留最新进度和最终结果。
func (u *ui) postUpload(ctx context.Context, update uploadUpdate) {
	select {
	case <-u.upload.updates:
	default:
	}
	select {
	case u.upload.updates <- update:
		if u.window != nil {
			u.window.Invalidate()
		}
	case <-ctx.Done():
	}
}

func (u *ui) uploadLayout(gtx layout.Context) layout.Dimensions {
	u.syncUploadHost()
	p := &u.upload
	status, detect := "上传位置", "检测"
	switch {
	case p.probe != nil:
		status = "正在检测 SD 卡…"
	case p.probeFailed:
		status, detect = "无法检测 SD 卡", "重试"
	case p.host != "" && !p.sdAvailable:
		status = "未检测到 SD 卡"
	}
	available := !u.fileBusy && p.host != "" && p.probe == nil
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(u.section(status)),
				layout.Rigid(u.uploadChoice(detect, &p.probeButton, false, available, func() {
					u.checkUploadStorage(0)
				})),
			)
		}),
		layout.Rigid(gap(4)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(u.uploadChoice("Flash", &p.flashButton, p.target() == frame.Flash, available, func() {
					p.preferFlash = true
				})),
				layout.Rigid(gap(6)),
				layout.Rigid(u.uploadChoice("SD 卡", &p.sdButton, p.target() == frame.SD, available && p.sdAvailable, func() {
					p.preferFlash = false
				})),
			)
		}),
		layout.Rigid(gap(6)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.probe != nil {
				gtx = gtx.Disabled()
			}
			return u.primaryButton("上传到相框", &p.sendButton, u.onUpload)(gtx)
		}),
	)
}

func (u *ui) uploadChoice(label string, button *widget.Clickable, selected, available bool, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !available {
			gtx = gtx.Disabled()
		}
		return u.tabButton(label, button, selected, func() {
			if available {
				action()
			}
		})(gtx)
	}
}

func (u *ui) uploadProgress(gtx layout.Context) layout.Dimensions {
	progress := u.upload.progress
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.section(fmt.Sprintf("%s · %d%%", progress.Message, progress.Percent))),
		layout.Rigid(gap(4)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			bar := material.ProgressBar(u.theme, float32(progress.Percent)/100)
			bar.TrackColor = u.colors.selected
			return bar.Layout(gtx)
		}),
	)
}
