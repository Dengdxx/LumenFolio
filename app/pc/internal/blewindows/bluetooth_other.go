//go:build !windows

package blewindows

import (
	"context"
	"errors"

	"lumenfolio/devicehttp"
)

var errUnsupported = errors.New("此平台不支持相框蓝牙连接")

func Scan(context.Context) ([]Device, error) {
	return nil, errUnsupported
}

func Dial(context.Context, Device) (devicehttp.BluetoothLink, error) {
	return nil, errUnsupported
}
