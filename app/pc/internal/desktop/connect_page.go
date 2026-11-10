package desktop

import (
	"context"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"lumenfolio/frameclient"
	"lumenfolio/identity"
)

// entryMode 是开屏之后的工作区。没有进入工作区时停在连接页。
type entryMode uint8

const (
	entryGate entryMode = iota
	entryOffline
	entryWiFi
	entryBluetooth
)

type connectPage struct {
	wifiButton, bluetoothButton, offlineButton widget.Clickable
	connectButton, leaveButton                 widget.Clickable
	step                                       string // ""、wifi、bluetooth
	status                                     string
	ctx                                        context.Context
	cancel                                     context.CancelFunc
	pending                                    *frameclient.Frame
	results                                    chan connectResult
}

type connectResult struct {
	session *frameclient.Frame
	id      identity.ID
	err     error
}

func (u *ui) inWorkspace() bool { return u.entry != entryGate }

func (u *ui) connectLayout(gtx layout.Context) layout.Dimensions {
	p := &u.connect
	// 无系统标题栏。连接页先标出整页可拖，后面画的按钮仍然接收点击。
	moveArea(gtx, gtx.Constraints.Max)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(u.connectBrand),
					layout.Rigid(gap(20)),
					layout.Rigid(u.connectChoices),
					layout.Rigid(gap(12)),
					layout.Rigid(u.connectDetail),
					layout.Rigid(gap(8)),
					layout.Rigid(u.section(p.status)),
				)
			})
		})
	})
}

// connectBrand 用侧栏同一组标志做开屏标题：相框图标在左，艺术字在右。
func (u *ui) connectBrand(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(gtx.Dp(64), gtx.Dp(64))
			gtx.Constraints = layout.Exact(size)
			defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(14)).Push(gtx.Ops).Pop()
			return widget.Image{Src: brandIcon(), Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
		}),
		layout.Rigid(gap(16)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			size := gtx.Constraints.Constrain(image.Pt(gtx.Constraints.Max.X, gtx.Dp(52)))
			gtx.Constraints = layout.Exact(size)
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			return widget.Image{Src: u.wordmark, Fit: widget.Contain, Position: layout.W}.Layout(gtx)
		}),
	)
}

func (u *ui) connectChoices(gtx layout.Context) layout.Dimensions {
	if u.connect.cancel != nil {
		gtx = gtx.Disabled()
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.button("Wi-Fi", &u.connect.wifiButton, func() {
			u.connect.step = "wifi"
			u.connect.status = "填写相框地址后连接。"
		})),
		layout.Rigid(gap(8)),
		layout.Rigid(u.button("蓝牙", &u.connect.bluetoothButton, u.startBluetoothEntry)),
		layout.Rigid(gap(8)),
		layout.Rigid(u.button("离线", &u.connect.offlineButton, u.enterOffline)),
	)
}

func (u *ui) connectDetail(gtx layout.Context) layout.Dimensions {
	switch u.connect.step {
	case "wifi":
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(u.hostField),
			layout.Rigid(gap(8)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.connect.cancel != nil {
					gtx = gtx.Disabled()
				}
				return u.primaryButton("连接", &u.connect.connectButton, u.connectTypedWiFi)(gtx)
			}),
		)
	case "bluetooth":
		return u.section("正在查找附近的相框。只有一台时会直接连接。")(gtx)
	default:
		return layout.Dimensions{}
	}
}

func (u *ui) enterOffline() {
	u.cancelConnectAttempt()
	u.closeSession()
	u.entry = entryOffline
	u.status = "离线使用。可以取景、预览和导出，不能上传或访问相框。"
}

func (u *ui) connectTypedWiFi() {
	if u.connect.cancel != nil {
		return
	}
	address := strings.TrimSpace(u.hostEditor.Text())
	session, err := frameclient.WiFi(address)
	if err != nil {
		u.connect.status = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.connect.ctx, u.connect.cancel, u.connect.pending = ctx, cancel, session
	u.connect.status = "正在连接 " + address + "…"
	go func() {
		_, err := session.Status(ctx)
		var id identity.ID
		if err == nil {
			id, err = u.identifySession(ctx, session)
		}
		u.connect.results <- connectResult{session: session, id: id, err: err}
		if u.window != nil {
			u.window.Invalidate()
		}
	}()
}

func (u *ui) startBluetoothEntry() {
	if u.connect.cancel != nil {
		return
	}
	u.connect.step = "bluetooth"
	u.connect.status = "正在查找相框…"
	u.scanBluetooth()
}

func (u *ui) applyConnect(result connectResult) {
	p := &u.connect
	canceled := p.ctx != nil && p.ctx.Err() != nil
	if p.cancel != nil {
		p.cancel()
	}
	p.ctx, p.cancel, p.pending = nil, nil, nil
	if canceled {
		if result.session != nil {
			result.session.Close()
		}
		return
	}
	if result.err != nil {
		if result.session != nil {
			result.session.Close()
		}
		p.status = "没有连上：" + result.err.Error()
		return
	}
	u.wifiAddress = result.session.Address()
	u.setSession(result.session)
	u.rememberFrame(result.id, result.session.Address())
	u.entry = entryWiFi
	u.checkUploadStorage(0)
	u.status = "已通过 Wi-Fi 连接 " + result.session.Address()
}

// returnToGate 关闭当前会话并回到开屏。已记住的地址保留在输入框里。
func (u *ui) returnToGate() {
	u.cancelConnectAttempt()
	u.closeGallery()
	u.closeOTA()
	u.bluetooth.back()
	if u.bluetooth.pending != nil {
		u.bluetooth.pending.Close()
		u.bluetooth.pending = nil
	}
	u.closeSession()
	u.entry = entryGate
	u.connect.step = ""
	u.connect.status = ""
}

func (u *ui) cancelConnectAttempt() {
	if u.connect.cancel != nil {
		u.connect.cancel()
	}
	if u.connect.pending != nil {
		u.connect.pending.Close()
		u.connect.pending = nil
	}
}

func (u *ui) entryLabel() string {
	switch u.entry {
	case entryOffline:
		return "离线"
	case entryBluetooth:
		return "蓝牙 · " + u.deviceLabel()
	case entryWiFi:
		return "Wi-Fi · " + u.deviceLabel()
	default:
		return ""
	}
}

func (u *ui) entryBar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
		layout.Flexed(1, u.section(u.entryLabel())),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if u.fileBusy {
				gtx = gtx.Disabled()
			}
			return u.uploadChoice("更换连接", &u.connect.leaveButton, false, !u.fileBusy, u.returnToGate)(gtx)
		}),
	)
}
