package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/frame"
	"lumenfolio/frameclient"
	"lumenfolio/identity"
	"lumenfolio/internal/picture"
)

// storageProbeDelay 让手填地址先稳定下来，避免输入过程中反复探测。
const storageProbeDelay = 350 * time.Millisecond

type uploadPanel struct {
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
	address     string // 探测时的 Wi-Fi 地址；蓝牙会话为空，不覆盖已记地址
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
		u.rememberFrame(result.id, result.address)
	}
	return true
}

// syncWiFiSession 让当前会话跟随手填地址。蓝牙已连接时地址框不生效。
//
// 新地址建立新会话，旧 Wi-Fi 会话先关闭。地址解析失败时保留原因供界面展示。
func (u *ui) syncWiFiSession() {
	if u.bluetoothConnected() {
		return
	}
	address := strings.TrimSpace(u.hostEditor.Text())
	if address == u.wifiAddress {
		if u.session != nil || address == "" || u.wifiError != "" {
			return
		}
	}
	u.wifiAddress = address
	if address == "" {
		u.wifiError = ""
		u.setSession(nil)
		return
	}
	session, err := frameclient.WiFi(address)
	if err != nil {
		u.wifiError = err.Error()
		u.setSession(nil)
		return
	}
	u.wifiError = ""
	u.setSession(session)
	u.checkUploadStorage(storageProbeDelay)
}

// checkUploadStorage 在地址稳定后读取容量，切换会话时取消原请求。
func (u *ui) checkUploadStorage(delay time.Duration) {
	p := &u.upload
	p.stopProbe()
	session := u.session
	if session == nil {
		return
	}
	address := u.rememberAddress()
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
		storage, err := session.Storage(ctx)
		var id identity.ID
		if err == nil {
			id, err = u.identifySession(ctx, session)
		}
		result := uploadStorageResult{probe: ctx, address: address, sdAvailable: storage.SD.TotalBytes != 0, id: id, err: err}
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
	u.syncWiFiSession()
	p := &u.upload
	session := u.session
	if session == nil {
		u.status = "先填写相框地址"
		if u.wifiError != "" {
			u.status = "相框地址无效：" + u.wifiError
		}
		return
	}
	if p.probe != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelSend = cancel
	u.fileBusy = true
	p.progress = frame.Progress{Message: "正在编码画面…"}
	target, name := p.target(), u.name
	alg, crop, src := u.alg, u.crop, u.rawImage
	go func() {
		var last frame.Progress
		data, err := picture.Encode(alg, src, crop)
		var reply string
		if err == nil {
			reply, err = session.Send(ctx, data, target, name, func(progress frame.Progress) {
				last = progress
				u.postUpload(ctx, uploadUpdate{progress: progress})
			})
		}
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
	p := &u.upload
	status, detect := "上传位置", "检测"
	switch {
	case p.probe != nil:
		status = "正在检测 SD 卡…"
	case p.probeFailed:
		status, detect = "无法检测 SD 卡", "重试"
	case u.session != nil && !p.sdAvailable:
		status = "未检测到 SD 卡"
	case u.session == nil && u.wifiError != "":
		status = "相框地址无效"
	}
	available := !u.fileBusy && u.session != nil && p.probe == nil
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
