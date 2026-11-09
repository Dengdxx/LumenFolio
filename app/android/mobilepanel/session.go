package mobilepanel

import (
	"context"

	"lumenfolio/frame"
	"lumenfolio/frameclient"
	"lumenfolio/gallery"
	"lumenfolio/ota"
)

// DeviceSession 是一台相框的会话句柄。
//
// 图库、传图和 OTA 都从它发起，Android 不再拼相框 host，也不区分 Wi-Fi 与蓝牙。
// 会话关闭后所有操作返回错误，Close 可重复调用。
type DeviceSession struct {
	frame *frameclient.Frame
}

// ConnectWiFi 用手填地址打开一次会话，此时不访问网络。address 可带端口，缺省 8080。
func ConnectWiFi(address string) (*DeviceSession, error) {
	frame, err := frameclient.WiFi(address)
	if err != nil {
		return nil, err
	}
	return &DeviceSession{frame: frame}, nil
}

// ConnectBluetooth 把 Android 的 GATT 拨号器登记为会话链路，此时不拨号。
// 同一蓝牙地址再次打开会替换旧链路；掉线后的下一次请求会再次拨号。
func ConnectBluetooth(name, address string, dialer BluetoothDialer) *DeviceSession {
	return &DeviceSession{frame: frameclient.OpenBluetooth(name, address, bluetoothDialer(dialer))}
}

// Close 释放这条会话。蓝牙会断开连接；可重复调用。
func (s *DeviceSession) Close() {
	s.frame.Close()
}

// ID 读取相框的固定身份。旧固件返回空串。
func (s *DeviceSession) ID() (string, error) {
	id, err := s.frame.ID(context.Background())
	if err != nil {
		return "", err
	}
	return string(id), nil
}

// Status 读取电量、充电和温湿度的展示文本。
func (s *DeviceSession) Status() (string, error) {
	return s.frame.Status(context.Background())
}

// Storage 读取 Flash artifacts 和 SD 卡的容量。
func (s *DeviceSession) Storage() (*StorageInfo, error) {
	usage, err := s.frame.Storage(context.Background())
	if err != nil {
		return nil, err
	}
	return &StorageInfo{usage: usage}, nil
}

// Pictures 读取一页图库。首次读取传空游标，空串返回的 Next 表示末页。
func (s *DeviceSession) Pictures(source, after string) (*DevicePictureList, error) {
	page, err := s.frame.Pictures(context.Background(), gallery.Source(source), after)
	if err != nil {
		return nil, err
	}
	return &DevicePictureList{pictures: page.Pictures, next: page.Next}, nil
}

// Preview 将指定板端帧还原为供 Android 解码的 PNG。
func (s *DeviceSession) Preview(source, id string) ([]byte, error) {
	pixels, err := s.frame.Preview(context.Background(), gallery.Source(source), id)
	if err != nil {
		return nil, err
	}
	return encodePreview(pixels)
}

// Thumbnail 优先复用指定版本的小图缓存，返回供 Android 解码的 PNG。
func (s *DeviceSession) Thumbnail(source, id, revision string) ([]byte, error) {
	picture := gallery.Picture{ID: id, Revision: revision}
	pixels, err := s.frame.Thumbnail(context.Background(), gallery.Source(source), picture)
	if err != nil {
		return nil, err
	}
	return encodePreview(pixels)
}

// Display 请求显示选中的画面，成功表示设备已安排刷新。
func (s *DeviceSession) Display(source, id string) error {
	return s.frame.Display(context.Background(), gallery.Source(source), id)
}

// Delete 由 Android 在确认后调用，成功时设备已删除目标画面。
func (s *DeviceSession) Delete(source, id string) error {
	return s.frame.Delete(context.Background(), gallery.Source(source), id)
}

// Send 发送已编码的帧并等待 SPI 回执，由 Android 在后台调用。
func (s *DeviceSession) Send(data []byte, source, name string, listener FrameTransferListener) (string, error) {
	return s.frame.Send(context.Background(), data, frame.Target(source), name, func(progress frame.Progress) {
		if listener != nil {
			listener.OnFrameProgress(progress.Stage, int32(progress.Percent), progress.Message)
		}
	})
}

// Upgrade 通过当前连接执行 OTA，等待目标镜像启动并确认后返回。Android 须在后台调用。
func (s *DeviceSession) Upgrade(data []byte, listener OTAListener) error {
	return s.frame.Upgrade(context.Background(), data, func(progress ota.Progress) {
		if listener != nil {
			listener.OnProgress(progress.Message, progress.Sent, progress.Total)
		}
	})
}
