// Package frameclient 把一台相框收成一次会话。
//
// 界面和命令行都从 Frame 出发。Wi-Fi 和蓝牙只是到达同一套 HTTP 接口的两条路，
// 调用方不再拼 host，也不判断当前走的是哪条链路。
package frameclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumenfolio/devicehttp"
	"lumenfolio/frame"
	"lumenfolio/gallery"
	"lumenfolio/identity"
	"lumenfolio/ota"
)

// LinkKind 是当前会话走的链路，只用于展示。
type LinkKind string

const (
	LinkWiFi      LinkKind = "wifi"
	LinkBluetooth LinkKind = "bluetooth"
)

// BluetoothDialer 为这一台相框拨号。掉线后的下一次请求会再次调用。
type BluetoothDialer func(context.Context) (devicehttp.BluetoothLink, error)

// Frame 是一台相框的一次会话。关闭后所有操作返回错误。
type Frame struct {
	kind    LinkKind
	name    string
	address string
	host    string
	port    int

	mu     sync.Mutex
	closed bool
}

// WiFi 用手填地址打开一次会话，此时不访问网络。address 可带端口，缺省 8080。
func WiFi(address string) (*Frame, error) {
	host, port, err := splitAddress(address)
	if err != nil {
		return nil, err
	}
	return &Frame{kind: LinkWiFi, name: host, address: host, host: host, port: port}, nil
}

// OpenBluetooth 把一条蓝牙链路登记为这次会话，此时不拨号。
//
// name 是扫描到的设备名，address 是蓝牙公开地址。同一地址再次打开会替换旧链路。
func OpenBluetooth(name, address string, dial BluetoothDialer) *Frame {
	host := devicehttp.UseBluetooth(address, dial)
	return &Frame{kind: LinkBluetooth, name: name, address: address, host: host, port: 8080}
}

func splitAddress(address string) (string, int, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, errors.New("缺少相框地址")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return address, 8080, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("相框端口无效：%s", portText)
	}
	return host, port, nil
}

// Kind 返回这条会话走的链路。
func (f *Frame) Kind() LinkKind { return f.kind }

// Name 返回展示用的名字。Wi-Fi 是地址，蓝牙是扫描到的设备名。
func (f *Frame) Name() string { return f.name }

// Address 返回用户能认出的地址。Wi-Fi 是主机名或 IP，蓝牙是公开地址。
func (f *Frame) Address() string { return f.address }

// Close 释放这条会话。蓝牙会断开连接；可重复调用。
func (f *Frame) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	if f.kind == LinkBluetooth {
		devicehttp.ForgetBluetooth(f.host)
	}
}

func (f *Frame) endpoint() (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return "", 0, errors.New("相框会话已关闭")
	}
	return f.host, f.port, nil
}

// ID 读取相框的固定身份。旧固件返回空串。
func (f *Frame) ID(ctx context.Context) (identity.ID, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return "", err
	}
	return ota.DeviceID(ctx, host, port)
}

// Status 读取电量、充电和温湿度的展示文本。
func (f *Frame) Status(ctx context.Context) (string, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return "", err
	}
	return ota.DeviceStatus(ctx, host, port)
}

// Storage 读取 Flash artifacts 和 SD 卡的容量。
func (f *Frame) Storage(ctx context.Context) (gallery.StorageUsage, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return gallery.StorageUsage{}, err
	}
	return gallery.Storage(ctx, host, port)
}

// Pictures 读取一页图库。after 为空表示第一页。
func (f *Frame) Pictures(ctx context.Context, source gallery.Source, after string) (gallery.Page, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return gallery.Page{}, err
	}
	return gallery.ListPage(ctx, host, port, source, after)
}

// Preview 读取一张图的完整六色预览。
func (f *Frame) Preview(ctx context.Context, source gallery.Source, id string) (*image.NRGBA, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return nil, err
	}
	return gallery.Preview(ctx, host, port, source, id)
}

// Thumbnail 读取一张图的缩略图，命中缓存时不再访问相框。
func (f *Frame) Thumbnail(ctx context.Context, source gallery.Source, picture gallery.Picture) (*image.NRGBA, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return nil, err
	}
	return gallery.ThumbnailFor(ctx, host, port, source, picture)
}

// Display 让相框显示已有的一张图。成功只表示已安排刷新。
func (f *Frame) Display(ctx context.Context, source gallery.Source, id string) error {
	host, port, err := f.endpoint()
	if err != nil {
		return err
	}
	return gallery.Display(ctx, host, port, source, id)
}

// Delete 删除 SD 卡上的一帧或 Flash 上传槽。开机画面不可删除。
func (f *Frame) Delete(ctx context.Context, source gallery.Source, id string) error {
	host, port, err := f.endpoint()
	if err != nil {
		return err
	}
	return gallery.Delete(ctx, host, port, source, id)
}

// CanDelete 判断这张图能不能删。开机槽由相框保护。
func CanDelete(source gallery.Source, id string) bool { return gallery.CanDelete(source, id) }

// Send 把已编码的帧存到选定位置，并等待 SPI 写屏回执。
func (f *Frame) Send(ctx context.Context, data []byte, target frame.Target, name string, report func(frame.Progress)) (string, error) {
	host, port, err := f.endpoint()
	if err != nil {
		return "", err
	}
	return frame.SendTo(ctx, host, port, data, target, name, report)
}

// InspectFirmware 读取本地应用镜像的版本，不连接相框。
func InspectFirmware(data []byte) (string, error) {
	return ota.Inspect(bytes.NewReader(data), int64(len(data)))
}

// Upgrade 上传固件并等待相框重启确认。report 可省略。
func (f *Frame) Upgrade(ctx context.Context, data []byte, report func(ota.Progress)) error {
	host, port, err := f.endpoint()
	if err != nil {
		return err
	}
	return ota.Upgrade(ctx, host, port, bytes.NewReader(data), int64(len(data)), report)
}

// UpgradeReader 给命令行用，镜像按传入的大小读取，不整份进内存。
// uploadTimeout 和 waitTimeout 分别限制上传和等待重启确认，与命令行的两个选项对应。
func (f *Frame) UpgradeReader(ctx context.Context, source io.ReaderAt, size int64, uploadTimeout, waitTimeout time.Duration, report func(ota.Progress)) error {
	host, port, err := f.endpoint()
	if err != nil {
		return err
	}
	return ota.UpgradeWithin(ctx, host, port, source, size, uploadTimeout, waitTimeout, report)
}
