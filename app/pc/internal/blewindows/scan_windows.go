package blewindows

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	winble "github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"golang.org/x/sys/windows"
	"tinygo.org/x/bluetooth"
)

const serviceID = "9fa16700-7e6b-4d75-89c4-5c79b25eac01"

var (
	combase        = windows.NewLazySystemDLL("combase.dll")
	roUninitialize = combase.NewProc("RoUninitialize")
)

// lockWinRT 让 WinRT 调用与初始化、清理留在同一个 MTA 线程上。
func lockWinRT() (func(), error) {
	runtime.LockOSThread()
	if err := ole.RoInitialize(1); err != nil && err.(*ole.OleError).Code() != 1 { // S_FALSE 也表示成功。
		runtime.UnlockOSThread()
		return nil, err
	}
	return func() {
		roUninitialize.Call()
		runtime.UnlockOSThread()
	}, nil
}

// Scan 主动扫描四秒，将广告和扫描响应按地址合并，只返回广播相框服务的设备。
func Scan(ctx context.Context) ([]Device, error) {
	release, err := lockWinRT()
	if err != nil {
		return nil, err
	}
	defer release()
	watcher, err := advertisement.NewBluetoothLEAdvertisementWatcher()
	if err != nil {
		return nil, err
	}
	defer watcher.Release()
	if err := watcher.SetScanningMode(advertisement.BluetoothLEScanningModeActive); err != nil {
		return nil, err
	}

	type result struct {
		name  string
		match bool
	}
	var mu sync.Mutex
	seen := make(map[uint64]result)
	receivedGUID := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher, advertisement.SignatureBluetoothLEAdvertisementReceivedEventArgs)
	received := foundation.NewTypedEventHandler(ole.NewGUID(receivedGUID), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		event := (*advertisement.BluetoothLEAdvertisementReceivedEventArgs)(arg)
		address, err := event.GetBluetoothAddress()
		if err != nil {
			return
		}
		packet, err := event.GetAdvertisement()
		if err != nil {
			return
		}
		defer packet.Release()
		name, _ := packet.GetLocalName()
		match := advertisesService(packet)
		mu.Lock()
		defer mu.Unlock()
		item := seen[address]
		if name != "" {
			item.name = name
		}
		item.match = item.match || match
		seen[address] = item
	})
	defer received.Release()
	receivedToken, err := watcher.AddReceived(received)
	if err != nil {
		return nil, err
	}
	defer watcher.RemoveReceived(receivedToken)

	stopped := make(chan error, 1)
	stoppedGUID := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher, advertisement.SignatureBluetoothLEAdvertisementWatcherStoppedEventArgs)
	handler := foundation.NewTypedEventHandler(ole.NewGUID(stoppedGUID), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		event := (*advertisement.BluetoothLEAdvertisementWatcherStoppedEventArgs)(arg)
		code, err := event.GetError()
		if err == nil && (code == winble.BluetoothErrorRadioNotAvailable || code == winble.BluetoothErrorDisabledByUser) {
			err = errors.New("请在 Windows 设置中打开蓝牙")
		} else if err == nil {
			err = fmt.Errorf("Windows 停止了蓝牙扫描（%d）", code)
		}
		stopped <- err
	})
	defer handler.Release()
	stoppedToken, err := watcher.AddStopped(handler)
	if err != nil {
		return nil, err
	}
	defer watcher.RemoveStopped(stoppedToken)
	if err := watcher.Start(); err != nil {
		var nativeErr *ole.OleError
		if errors.As(err, &nativeErr) {
			switch uint32(nativeErr.Code()) {
			case 0x80070000 | uint32(windows.ERROR_NOT_READY), 0x80070000 | uint32(windows.ERROR_DEVICE_NOT_AVAILABLE):
				return nil, errors.New("请在 Windows 设置中打开蓝牙")
			}
		}
		return nil, err
	}
	defer watcher.Stop()

	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-stopped:
		return nil, err
	case <-timer.C:
	}
	mu.Lock()
	defer mu.Unlock()
	devices := make([]Device, 0)
	for address, item := range seen {
		if !item.match {
			continue
		}
		var mac bluetooth.MAC
		for i := range mac {
			mac[i] = byte(address >> (8 * i))
		}
		if item.name == "" {
			item.name = fmt.Sprintf("LumenFolio-%04X", uint16(address))
		}
		devices = append(devices, Device{Address: mac.String(), Name: item.name})
	}
	slices.SortFunc(devices, func(a, b Device) int { return strings.Compare(a.Address, b.Address) })
	return devices, nil
}

func advertisesService(packet *advertisement.BluetoothLEAdvertisement) bool {
	services, err := packet.GetServiceUuids()
	if err != nil {
		return false
	}
	defer services.Release()
	count, _ := services.GetSize()
	for i := uint32(0); i < count; i++ {
		var id syscall.GUID
		// IVector<GUID> 的 GetAt 输出 16 字节，通用绑定只为指针大小的输出分配空间。
		hr, _, _ := syscall.SyscallN(services.VTable().GetAt, uintptr(unsafe.Pointer(services)), uintptr(i), uintptr(unsafe.Pointer(&id)))
		if hr == 0 && bluetooth.GUIDToUUID(id) == serviceUUID {
			return true
		}
	}
	return false
}
