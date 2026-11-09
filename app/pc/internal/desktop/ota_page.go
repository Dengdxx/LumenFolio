package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"

	"lumenfolio/ota"
)

// otaPage 的界面状态由窗口事件循环维护，文件读取与升级在后台发送结果。
type otaPage struct {
	open          bool
	openButton    widget.Clickable
	backButton    widget.Clickable
	chooseButton  widget.Clickable
	confirmButton widget.Clickable
	firmware      otaFirmware
	progress      ota.Progress
	updates       chan otaUpdate
}

type otaFirmware struct {
	name    string
	version string
	data    []byte // 选择时的快照，确认时上传同一份数据。
}

type otaUpdate struct {
	firmware *otaFirmware
	progress ota.Progress
	finished bool
}

func (u *ui) openOTA() {
	u.ota.open = true
	u.ota.progress = ota.Progress{Message: "请选择已下载的 LumenFolio 应用镜像（.bin）。"}
	u.window.Invalidate()
}

func (u *ui) closeOTA() {
	u.ota.open = false
	u.ota.firmware = otaFirmware{}
	u.window.Invalidate()
}

func (u *ui) chooseOTAFirmware() {
	u.fileBusy = true
	u.ota.firmware = otaFirmware{}
	u.ota.progress = ota.Progress{Message: "正在选择固件…"}
	go func() {
		result := otaUpdate{finished: true}
		file, err := u.explorer.ChooseFile(".bin")
		if errors.Is(err, explorer.ErrUserDecline) {
			result.progress.Message = "已取消选择固件。"
			u.postOTA(result)
			return
		}
		if err == nil {
			var firmware otaFirmware
			firmware, err = readOTAFirmware(file)
			if err == nil {
				result.firmware = &firmware
				result.progress.Message = "请核对相框地址和固件版本，然后确认升级。"
			}
		}
		if err != nil {
			result.progress.Message = "没能读取固件：" + err.Error()
		}
		u.postOTA(result)
	}()
}

// readOTAFirmware 按当前应用槽的容量限制文件快照，设备仍在升级前检查实际槽容量。
func readOTAFirmware(file io.ReadCloser) (otaFirmware, error) {
	defer file.Close()
	const maxSize = 0x180000
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return otaFirmware{}, err
	}
	if len(data) > maxSize {
		return otaFirmware{}, errors.New("应用镜像超过 1.5 MiB")
	}
	version, err := ota.Inspect(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return otaFirmware{}, err
	}
	name := "应用镜像.bin"
	if named, ok := file.(interface{ Name() string }); ok && named.Name() != "" {
		name = filepath.Base(named.Name())
	}
	return otaFirmware{name: name, version: version, data: data}, nil
}

func (u *ui) startOTA() {
	host := strings.TrimSpace(u.hostEditor.Text())
	if host == "" {
		u.ota.progress = ota.Progress{Message: "先填写相框地址。"}
		return
	}
	firmware := u.ota.firmware
	u.fileBusy = true
	u.ota.progress = ota.Progress{Message: "正在连接相框并核对固件…"}
	go func() {
		result := otaUpdate{finished: true}
		err := ota.Upgrade(context.Background(), host, 8080, bytes.NewReader(firmware.data), int64(len(firmware.data)), func(progress ota.Progress) {
			u.postOTA(otaUpdate{progress: progress})
		})
		if err != nil {
			result.progress.Message = "升级未完成：" + err.Error()
		} else {
			firmware.data = nil
			result.firmware = &firmware
			result.progress.Message = "升级成功，固件 " + firmware.version + " 已启动并确认。"
		}
		u.postOTA(result)
	}()
}

// postOTA 由当前文件操作串行发布，保留最新进度，窗口未绘制时上传也能继续。
// 完成状态发送后，窗口消费它并解除 fileBusy 才能开始下一项操作。
func (u *ui) postOTA(update otaUpdate) {
	select {
	case <-u.ota.updates:
	default:
	}
	u.ota.updates <- update
	if u.window != nil {
		u.window.Invalidate()
	}
}

func (u *ui) otaLayout(gtx layout.Context) layout.Dimensions {
	p := &u.ota
	moveArea(gtx, image.Pt(gtx.Dp(140), gtx.Dp(44)))
	return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(560)))
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(material.Subtitle1(u.theme, "固件升级").Layout),
					layout.Rigid(gap(16)),
					layout.Rigid(u.section("相框地址")),
					layout.Rigid(gap(6)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.fileBusy {
							gtx = gtx.Disabled()
						}
						return u.hostField(gtx)
					}),
					layout.Rigid(gap(12)),
					layout.Rigid(u.button("选择固件文件（.bin）", &p.chooseButton, u.chooseOTAFirmware)),
					layout.Rigid(gap(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.firmware.name == "" {
							return layout.Dimensions{}
						}
						return material.Body2(u.theme, p.firmware.name+" · 版本 "+p.firmware.version).Layout(gtx)
					}),
					layout.Rigid(gap(8)),
					layout.Rigid(u.section("升级期间请保持应用开启、连接和供电，暂勿操作相框按键或刷新屏幕。")),
					layout.Rigid(gap(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.firmware.data == nil {
							return layout.Dimensions{}
						}
						return u.primaryButton("确认升级", &p.confirmButton, u.startOTA)(gtx)
					}),
					layout.Rigid(gap(12)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return u.section(p.progress.Message)(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if p.progress.Total == 0 {
							return layout.Dimensions{}
						}
						progress := material.ProgressBar(u.theme, float32(p.progress.Sent)/float32(p.progress.Total))
						progress.TrackColor = u.colors.selected
						return progress.Layout(gtx)
					}),
					layout.Rigid(gap(16)),
					layout.Rigid(u.button("← 返回取景", &p.backButton, u.closeOTA)),
				)
			})
		})
	})
}
