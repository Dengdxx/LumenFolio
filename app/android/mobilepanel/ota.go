package mobilepanel

import (
	"lumenfolio/frameclient"
)

// OTAListener 接收固件升级进度。回调可能从后台线程发出，Total 大于 0 时为上传字节数。
type OTAListener interface {
	OnProgress(message string, sent, total int64)
}

// InspectFirmware 读取本地应用镜像版本，供用户在升级前核对，不连接相框。
func InspectFirmware(data []byte) (string, error) {
	return frameclient.InspectFirmware(data)
}
