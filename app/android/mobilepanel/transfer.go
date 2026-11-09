package mobilepanel

import (
	"context"

	"lumenfolio/frame"
)

// FrameTransferListener 接收传图阶段和 0 至 100 的总进度估计，由 Android 切回主线程更新界面。
type FrameTransferListener interface {
	OnFrameProgress(stage string, percent int32, message string)
}

// UploadDeviceFrame 发送已编码的帧并等待 SPI 回执，由 Android 在后台调用。
func UploadDeviceFrame(host string, data []byte, source, name string, listener FrameTransferListener) (string, error) {
	return frame.SendTo(context.Background(), host, devicePort, data, frame.Target(source), name, func(progress frame.Progress) {
		if listener != nil {
			listener.OnFrameProgress(progress.Stage, int32(progress.Percent), progress.Message)
		}
	})
}
