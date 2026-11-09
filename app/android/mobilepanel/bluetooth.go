package mobilepanel

import (
	"context"
	"errors"
	"time"

	"lumenfolio/devicehttp"
)

// BluetoothConnection 由 Android GATT 客户端实现。每次读写等待本次操作的系统回调。
type BluetoothConnection interface {
	MaxWrite() int32
	WriteBluetooth(data []byte, timeoutMillis int64) error
	ReadBluetooth(timeoutMillis int64) ([]byte, error)
	CloseBluetooth()
}

// BluetoothDialer 为同一台相框建立新连接，由共享传输在掉线后的下一次请求时调用。
type BluetoothDialer interface {
	DialBluetooth(timeoutMillis int64) (BluetoothConnection, error)
}

// UseBluetooth 注册平台蓝牙拨号器，返回图库、传图和 OTA 共用的相框地址。
func UseBluetooth(address string, dialer BluetoothDialer) string {
	return devicehttp.UseBluetooth(address, func(ctx context.Context) (devicehttp.BluetoothLink, error) {
		timeout, err := bluetoothTimeout(ctx)
		if err != nil {
			return nil, err
		}
		connection, err := dialer.DialBluetooth(timeout)
		if err != nil {
			return nil, err
		}
		if connection == nil {
			return nil, errors.New("无法建立相框蓝牙连接")
		}
		if err := ctx.Err(); err != nil {
			connection.CloseBluetooth()
			return nil, err
		}
		return bluetoothLink{connection}, nil
	})
}

// ForgetBluetooth 关闭该地址的连接并释放平台拨号器。
func ForgetBluetooth(host string) {
	devicehttp.ForgetBluetooth(host)
}

type bluetoothLink struct {
	connection BluetoothConnection
}

func (link bluetoothLink) MaxWrite() int { return int(link.connection.MaxWrite()) }

func (link bluetoothLink) Write(ctx context.Context, data []byte) error {
	timeout, err := bluetoothTimeout(ctx)
	if err != nil {
		return err
	}
	return link.connection.WriteBluetooth(data, timeout)
}

func (link bluetoothLink) Read(ctx context.Context) ([]byte, error) {
	timeout, err := bluetoothTimeout(ctx)
	if err != nil {
		return nil, err
	}
	return link.connection.ReadBluetooth(timeout)
}

func (link bluetoothLink) Close() { link.connection.CloseBluetooth() }

// 单次 GATT 操作有独立时限，同时服从整个设备请求的剩余时间。
func bluetoothTimeout(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	wait := 20 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline))
	}
	return max(1, wait.Milliseconds()), nil
}
