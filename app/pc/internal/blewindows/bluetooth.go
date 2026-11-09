// Package blewindows 在 Windows 上通过 WinRT 访问相框的 BLE GATT 服务。
package blewindows

// Device 是一次扫描找到的相框，地址用于当前进程内重新连接。
type Device struct {
	Address string
	Name    string
}
