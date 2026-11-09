package blewindows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"lumenfolio/devicehttp"
	"tinygo.org/x/bluetooth"
)

const maxAttributeValue = 512

var (
	serviceUUID, _ = bluetooth.ParseUUID(serviceID)
	writeUUID, _   = bluetooth.ParseUUID("9fa16701-7e6b-4d75-89c4-5c79b25eac01")
	readUUID, _    = bluetooth.ParseUUID("9fa16702-7e6b-4d75-89c4-5c79b25eac01")
)

type link struct {
	maxWrite int
	requests chan linkRequest
	closed   chan struct{}
	close    sync.Once
}

type linkRequest struct {
	ctx    context.Context
	write  []byte
	result chan linkResult
}

type linkResult struct {
	data []byte
	err  error
}

// Dial 通过扫描得到的地址建立 WinRT GATT 会话。调用方可取消连接等待，之后原生线程负责释放会话。
func Dial(ctx context.Context, selected Device) (devicehttp.BluetoothLink, error) {
	mac, err := bluetooth.ParseMAC(selected.Address)
	if err != nil {
		return nil, err
	}
	l := &link{requests: make(chan linkRequest), closed: make(chan struct{})}
	ready := make(chan error, 1)
	go l.run(ctx, bluetooth.Address{MACAddress: bluetooth.MACAddress{MAC: mac}}, ready)
	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
		return l, nil
	case <-ctx.Done():
		l.Close()
		return nil, ctx.Err()
	}
}

// run 在同一原生线程串行执行 GATT 操作，避免取消请求时释放系统仍在使用的会话。
func (l *link) run(ctx context.Context, address bluetooth.Address, ready chan<- error) {
	release, err := lockWinRT()
	if err != nil {
		ready <- err
		return
	}
	defer release()

	adapter := &bluetooth.Adapter{}
	device, err := adapter.Connect(address, bluetooth.ConnectionParams{
		Priority: bluetooth.ConnectionPriorityThroughput,
	})
	if err != nil {
		ready <- fmt.Errorf("没能连接相框：%w", err)
		return
	}
	defer device.Disconnect()
	if ctx.Err() != nil {
		ready <- ctx.Err()
		return
	}
	services, err := device.DiscoverServices([]bluetooth.UUID{serviceUUID})
	if err != nil {
		ready <- fmt.Errorf("没找到相框蓝牙服务：%w", err)
		return
	}
	characteristics, err := services[0].DiscoverCharacteristics([]bluetooth.UUID{writeUUID, readUUID})
	if err != nil {
		ready <- fmt.Errorf("没找到相框蓝牙通信端点：%w", err)
		return
	}
	writer, reader := characteristics[0], characteristics[1]
	if err := waitEncryption(ctx, reader); err != nil {
		ready <- fmt.Errorf("无法建立加密连接，请确认系统的配对提示：%w", err)
		return
	}
	mtu, err := writer.GetMTU()
	if err != nil {
		ready <- fmt.Errorf("没能读取蓝牙传输大小：%w", err)
		return
	}
	l.maxWrite = min(int(mtu)-3, maxAttributeValue)
	ready <- nil

	for {
		select {
		case <-l.closed:
			return
		case req := <-l.requests:
			result := linkResult{err: req.ctx.Err()}
			if result.err == nil {
				result = exchange(req, writer, reader)
			}
			req.result <- result
		}
	}
}

// waitEncryption 只在第一笔请求前读取空闲端点，让用户有时间接受 Windows 配对提示。
func waitEncryption(ctx context.Context, reader bluetooth.DeviceCharacteristic) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var buffer [maxAttributeValue]byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := reader.Read(buffer[:])
		if !errors.Is(err, bluetooth.ErrAttInsufficientEncryption) && !errors.Is(err, bluetooth.ErrAttInsufficientAuthentication) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func exchange(req linkRequest, writer, reader bluetooth.DeviceCharacteristic) linkResult {
	if req.write != nil {
		n, err := writer.Write(req.write)
		if err == nil && n != len(req.write) {
			err = io.ErrShortWrite
		}
		return linkResult{err: err}
	}
	data := make([]byte, maxAttributeValue)
	n, err := reader.Read(data)
	return linkResult{data: data[:n], err: err}
}

func (l *link) MaxWrite() int { return l.maxWrite }

func (l *link) Write(ctx context.Context, value []byte) error {
	return l.request(ctx, slices.Clone(value)).err
}

func (l *link) Read(ctx context.Context) ([]byte, error) {
	result := l.request(ctx, nil)
	return result.data, result.err
}

func (l *link) Close() { l.close.Do(func() { close(l.closed) }) }

func (l *link) request(ctx context.Context, value []byte) linkResult {
	req := linkRequest{ctx: ctx, write: value, result: make(chan linkResult, 1)}
	select {
	case l.requests <- req:
	case <-l.closed:
		return linkResult{err: io.ErrClosedPipe}
	case <-ctx.Done():
		return linkResult{err: ctx.Err()}
	}
	select {
	case result := <-req.result:
		return result
	case <-l.closed:
		return linkResult{err: io.ErrClosedPipe}
	case <-ctx.Done():
		return linkResult{err: ctx.Err()}
	}
}
