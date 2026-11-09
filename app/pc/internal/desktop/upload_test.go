package desktop

import (
	"context"
	"errors"
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"lumenfolio/devicehttp"
	"lumenfolio/frame"
	"lumenfolio/frameclient"
	"lumenfolio/panel"
)

func TestUploadStorageChoosesAvailableTarget(t *testing.T) {
	u := newTestUI()
	p := &u.upload
	old, cancelOld := context.WithCancel(context.Background())
	cancelOld()
	p.probe, p.cancelProbe = context.WithCancel(context.Background())
	defer p.close()

	p.storageResults <- uploadStorageResult{probe: old, sdAvailable: true}
	if u.applyPendingResults() || p.target() != frame.Flash || p.probe == nil {
		t.Fatal("过期容量结果不能选择 SD 卡或结束当前探测")
	}
	p.storageResults <- uploadStorageResult{probe: p.probe, sdAvailable: true}
	if !u.applyPendingResults() || p.target() != frame.SD || p.probe != nil {
		t.Fatal("已挂载 SD 卡时应默认选择 SD 卡")
	}

	u.setSession(testWiFiSession(t, "frame"))
	u.wifiAddress = "frame"
	u.hostEditor.SetText("frame")
	p.flashButton.Click()
	var ops op.Ops
	u.uploadLayout(layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(216, 116))})
	if p.target() != frame.Flash {
		t.Fatal("应允许从 SD 卡切换到 Flash")
	}
	p.probe, p.cancelProbe = context.WithCancel(context.Background())
	p.storageResults <- uploadStorageResult{probe: p.probe, sdAvailable: true}
	u.applyPendingResults()
	if p.target() != frame.Flash {
		t.Fatal("重检同一相框应保留手动选择")
	}

	p.preferFlash = false
	p.probe, p.cancelProbe = context.WithCancel(context.Background())
	p.storageResults <- uploadStorageResult{probe: p.probe}
	u.applyPendingResults()
	if p.target() != frame.Flash || p.probeFailed {
		t.Fatal("没有挂载 SD 卡时应默认选择 Flash")
	}
	p.probe, p.cancelProbe = context.WithCancel(context.Background())
	p.storageResults <- uploadStorageResult{probe: p.probe, err: errors.New("设备离线")}
	u.applyPendingResults()
	if p.target() != frame.Flash || !p.probeFailed {
		t.Fatal("探测失败时应提供重试和 Flash")
	}
}

func TestDisconnectingBluetoothRestoresTheTypedAddress(t *testing.T) {
	u := newTestUI()
	u.hostEditor.SetText("192.168.137.42")
	bluetooth := frameclient.OpenBluetooth("LumenFolio", "01:02:03:04:05:06", func(context.Context) (devicehttp.BluetoothLink, error) {
		t.Fatal("断开后不应再拨号")
		return nil, context.Canceled
	})
	u.setSession(bluetooth)
	u.uploadLayout(layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(216, 116))})
	if u.session != bluetooth {
		t.Fatal("蓝牙连接期间手填地址不应替换会话")
	}

	u.disconnectBluetooth()
	u.uploadLayout(layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(216, 116))})
	if u.session == nil || u.session.Kind() != frameclient.LinkWiFi || u.session.Address() != "192.168.137.42" {
		t.Fatal("断开蓝牙后应恢复手填的 Wi-Fi 地址")
	}
}

func TestUploadProgressKeepsFileOperationBusy(t *testing.T) {
	u := newTestUI()
	u.window = &waitingWindow{wake: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	u.upload.cancelSend = cancel
	u.fileBusy = true
	u.postUpload(ctx, uploadUpdate{progress: frame.Progress{Stage: "upload", Percent: 40}})
	u.postUpload(ctx, uploadUpdate{progress: frame.Progress{Stage: "spi", Percent: 92}})
	if !u.applyPendingResults() || !u.fileBusy || u.upload.progress.Stage != "spi" {
		t.Fatal("应显示最新进度，写屏期间仍保持上传锁")
	}
	u.postUpload(ctx, uploadUpdate{progress: frame.Progress{Stage: "done", Percent: 100, Message: "SPI 写屏完成"}, finished: true})
	if !u.applyPendingResults() || u.fileBusy || u.upload.cancelSend != nil || u.status != "SPI 写屏完成" {
		t.Fatal("SPI 完成后应解除上传锁并显示结果")
	}
}

func TestUploadControlsFitMinimumWindow(t *testing.T) {
	u := newTestUI()
	u.rawImage = image.NewNRGBA(image.Rect(0, 0, 120, 160))
	u.crop = panel.NewCrop(u.rawImage.Bounds())
	u.setSession(testWiFiSession(t, "frame"))
	u.wifiAddress = "frame"
	u.hostEditor.SetText("frame")
	u.upload.sdAvailable = true

	var r input.Router
	var ops op.Ops
	size := image.Pt(900, 600)
	gtx := layout.Context{Ops: &ops, Source: r.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(size)}
	draw := func() {
		ops.Reset()
		u.layout(gtx)
		r.Frame(&ops)
	}
	draw()
	buttons := map[string]*widget.Clickable{
		"Flash": &u.upload.flashButton, "SD 卡": &u.upload.sdButton,
		"上传到相框": &u.upload.sendButton, "导出帧文件": &u.exportButton,
	}
	points := make(map[string]image.Point)
	var visit func([]input.SemanticNode)
	visit = func(nodes []input.SemanticNode) {
		for _, node := range nodes {
			point := node.Desc.Bounds.Min.Add(image.Pt(2, 2))
			if buttons[node.Desc.Label] != nil && point.In(image.Rectangle{Max: size}) {
				points[node.Desc.Label] = point
			}
			visit(node.Children)
		}
	}
	visit(r.AppendSemantics(nil))
	for label, button := range buttons {
		point, ok := points[label]
		if !ok {
			t.Errorf("最小窗口中缺少可见控件 %q", label)
			continue
		}
		at := f32.Pt(float32(point.X), float32(point.Y))
		r.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at},
		)
		if !button.Clicked(gtx) {
			t.Errorf("最小窗口中控件 %q 未收到点击", label)
		}
		draw()
	}
}
