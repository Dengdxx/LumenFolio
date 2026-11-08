package mobilepanel

import (
	"bytes"
	"context"

	"lumenfolio/ota"
)

// OTAListener 接收固件升级进度。回调可能从后台线程发出，Total 大于 0 时为上传字节数。
type OTAListener interface {
	OnProgress(message string, sent, total int64)
}

// InspectFirmware 读取本地应用镜像版本，供用户在升级前核对。
func InspectFirmware(data []byte) (string, error) {
	return ota.Inspect(bytes.NewReader(data), int64(len(data)))
}

// UpgradeFirmware 执行局域网 OTA，等待目标镜像启动并确认后返回。Android 须在后台调用。
func UpgradeFirmware(host string, data []byte, listener OTAListener) error {
	return ota.Upgrade(context.Background(), host, devicePort, bytes.NewReader(data), int64(len(data)), func(progress ota.Progress) {
		listener.OnProgress(progress.Message, progress.Sent, progress.Total)
	})
}
