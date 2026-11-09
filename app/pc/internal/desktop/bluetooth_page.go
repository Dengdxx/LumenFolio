package desktop

import (
	"context"
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"lumenfolio/devicehttp"
	"lumenfolio/internal/blewindows"
	"lumenfolio/ota"
)

// bluetoothPage 由窗口事件循环持有，扫描和连接只在后台交付结果。
type bluetoothPage struct {
	open        bool
	host        string
	selected    blewindows.Device
	pendingHost string
	ctx         context.Context
	cancel      context.CancelFunc
	status      string
	devices     []blewindows.Device
	results     chan bluetoothResult

	openButton, scanButton, disconnectButton, backButton widget.Clickable
	buttons                                              []widget.Clickable
	list                                                 layout.List
}

type bluetoothResult struct {
	devices []blewindows.Device
	device  blewindows.Device
	err     error
}

const bluetoothInstruction = "长按相框 KEY3 打开蓝牙，再选择找到的相框。"

func (u *ui) deviceHost() string {
	if u.bluetooth.host != "" {
		return u.bluetooth.host
	}
	return strings.TrimSpace(u.hostEditor.Text())
}

func (u *ui) deviceLabel(host string) string {
	if host != "" && host == u.bluetooth.host {
		return u.bluetooth.selected.Name + " · 蓝牙"
	}
	return host
}

func (u *ui) openBluetooth() {
	p := &u.bluetooth
	p.open = true
	if p.host != "" {
		p.status = "已连接 " + p.selected.Name
	}
	if p.host == "" && p.ctx == nil {
		u.scanBluetooth()
	}
}

func (u *ui) scanBluetooth() {
	p := &u.bluetooth
	if p.ctx != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.ctx, p.cancel = ctx, cancel
	p.status = "正在搜索相框…"
	p.devices, p.buttons = nil, nil
	p.list = layout.List{Axis: layout.Vertical}
	go func() {
		devices, err := blewindows.Scan(ctx)
		u.postBluetooth(bluetoothResult{devices: devices, err: err})
	}()
}

func (u *ui) connectBluetooth(device blewindows.Device) {
	p := &u.bluetooth
	if p.ctx != nil || device.Address == p.selected.Address {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.ctx, p.cancel = ctx, cancel
	host := devicehttp.UseBluetooth(device.Address, func(ctx context.Context) (devicehttp.BluetoothLink, error) {
		return blewindows.Dial(ctx, device)
	})
	p.pendingHost = host
	p.status = "正在连接 " + device.Name + "，若系统提示配对，请确认。"
	go func() {
		_, err := ota.DeviceStatus(ctx, host, 8080)
		u.postBluetooth(bluetoothResult{device: device, err: err})
	}()
}

func (u *ui) postBluetooth(result bluetoothResult) {
	u.bluetooth.results <- result
	if u.window != nil {
		u.window.Invalidate()
	}
}

func (p *bluetoothPage) apply(result bluetoothResult) {
	canceled := p.ctx.Err() != nil
	p.cancel()
	p.ctx, p.cancel = nil, nil
	pendingHost := p.pendingHost
	p.pendingHost = ""
	if canceled {
		devicehttp.ForgetBluetooth(pendingHost)
		return
	}
	if result.err != nil {
		devicehttp.ForgetBluetooth(pendingHost)
		if pendingHost != "" {
			p.status = "连接未完成：" + result.err.Error()
		} else {
			p.status = "搜索失败：" + result.err.Error()
		}
		return
	}
	if pendingHost != "" {
		devicehttp.ForgetBluetooth(p.host)
		p.host, p.selected = pendingHost, result.device
		p.status = "已连接 " + p.selected.Name
		return
	}
	p.devices = result.devices
	p.buttons = make([]widget.Clickable, len(p.devices))
	p.status = "选择要连接的相框。"
	if len(p.devices) == 0 {
		p.status = "未找到相框。请打开相框蓝牙后重新搜索。"
	}
}

func (p *bluetoothPage) disconnect() {
	devicehttp.ForgetBluetooth(p.host)
	p.host, p.selected = "", blewindows.Device{}
	p.status = "已断开蓝牙，返回取景后可使用 Wi-Fi 地址。"
}

func (p *bluetoothPage) back() {
	if p.cancel != nil {
		p.cancel()
	}
	p.open = false
}

func (p *bluetoothPage) close() {
	p.back()
	devicehttp.ForgetBluetooth(p.host)
	devicehttp.ForgetBluetooth(p.pendingHost)
}

func (u *ui) bluetoothLayout(gtx layout.Context) layout.Dimensions {
	p := &u.bluetooth
	moveArea(gtx, image.Pt(gtx.Dp(140), gtx.Dp(44)))
	return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(560))
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(material.Subtitle1(u.theme, "蓝牙连接").Layout),
					layout.Rigid(gap(12)),
					layout.Rigid(u.section(bluetoothInstruction)),
					layout.Rigid(gap(8)),
					layout.Rigid(u.section(p.status)),
					layout.Rigid(gap(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.ctx != nil {
							gtx = gtx.Disabled()
						}
						return u.button("搜索相框", &p.scanButton, u.scanBluetooth)(gtx)
					}),
					layout.Rigid(gap(8)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(230))
						macro := op.Record(gtx.Ops)
						dims := p.list.Layout(gtx, len(p.devices), func(gtx layout.Context, i int) layout.Dimensions {
							device := p.devices[i]
							if p.ctx != nil || device.Address == p.selected.Address {
								gtx = gtx.Disabled()
							}
							return layout.Inset{Bottom: 4}.Layout(gtx,
								u.button(device.Name+" · "+device.Address, &p.buttons[i], func() { u.connectBluetooth(device) }))
						})
						call := macro.Stop()
						defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
						call.Add(gtx.Ops)
						return dims
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.host == "" {
							return layout.Dimensions{}
						}
						if p.ctx != nil {
							gtx = gtx.Disabled()
						}
						return u.button("断开蓝牙并使用 Wi-Fi", &p.disconnectButton, func() {
							p.disconnect()
							gtx.Execute(op.InvalidateCmd{})
						})(gtx)
					}),
					layout.Rigid(gap(8)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return u.button("← 返回取景", &p.backButton, func() {
							p.back()
							gtx.Execute(op.InvalidateCmd{})
						})(gtx)
					}),
				)
			})
		})
	})
}
