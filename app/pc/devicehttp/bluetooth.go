// Package devicehttp 通过 Wi-Fi 或 BLE 访问同一套相框接口。
package devicehttp

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	bluetoothSuffix = ".lumenfolio-ble.invalid"
	openSocket      = 1
	writeSocket     = 2
	closeSocket     = 3

	socketWaiting = 0
	socketData    = 1
	socketEOF     = 2
	socketError   = 3

	// BluetoothTransferTimeout 为 MTU 较小的 BLE 链路预留传输时间。
	BluetoothTransferTimeout = 15 * time.Minute
	// BluetoothConnectTimeout 给首次连接及系统蓝牙配对留出时间。
	BluetoothConnectTimeout = 25 * time.Second
)

// BluetoothLink 是已建立的 GATT 连接。同一时间只执行一个操作。
// MaxWrite 包括隧道命令字节，不包括 ATT 头。
type BluetoothLink interface {
	MaxWrite() int
	Write(context.Context, []byte) error
	Read(context.Context) ([]byte, error)
	Close()
}

// bluetoothTarget 持有 available 令牌时才操作链路。关闭回环失败后释放链路，
// 让下次请求可以重新连接，包括 OTA 重启后的状态确认。
type bluetoothTarget struct {
	dial      func(context.Context) (BluetoothLink, error)
	available chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	link      BluetoothLink
}

var bluetoothTargets sync.Map // synthetic host -> *bluetoothTarget

// UseBluetooth 把平台 GATT 连接器登记为现有相框接口使用的 host。
// 首次请求和掉线后的请求按需调用 dial，连接同一台相框。
func UseBluetooth(address string, dial func(context.Context) (BluetoothLink, error)) string {
	host := hex.EncodeToString([]byte(address)) + bluetoothSuffix
	ctx, cancel := context.WithCancel(context.Background())
	target := &bluetoothTarget{dial: dial, available: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	target.available <- struct{}{}
	if previous, ok := bluetoothTargets.Swap(host, target); ok {
		previous.(*bluetoothTarget).stop()
	}
	return host
}

// ForgetBluetooth 取消正在执行或排队的请求，并异步关闭蓝牙连接。
func ForgetBluetooth(host string) {
	if target, ok := bluetoothTargets.LoadAndDelete(host); ok {
		target.(*bluetoothTarget).stop()
	}
}

// IsBluetooth 判断 host 是否选择 BLE 传输。
func IsBluetooth(host string) bool { return strings.HasSuffix(host, bluetoothSuffix) }

func (target *bluetoothTarget) stop() {
	target.cancel()
	go func() {
		<-target.available
		target.dropLink()
	}()
}

func (target *bluetoothTarget) dropLink() {
	if target.link != nil {
		target.link.Close()
		target.link = nil
	}
}

type transport struct{ wifi *http.Transport }

// WithBluetooth 添加 BLE 隧道，沿用调用方的 Wi-Fi 配置和空闲连接生命周期。
func WithBluetooth(wifi *http.Transport) http.RoundTripper { return transport{wifi: wifi} }

func (t transport) CloseIdleConnections() { t.wifi.CloseIdleConnections() }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if !IsBluetooth(host) {
		return t.wifi.RoundTrip(req)
	}
	value, ok := bluetoothTargets.Load(host)
	if !ok {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, errors.New("蓝牙相框已断开，请重新连接")
	}
	return value.(*bluetoothTarget).roundTrip(req)
}

// roundTrip 在响应体关闭前独占相框。同一链路每次只处理一个 HTTP 请求，
// 由固件现有接口管理应答、状态及存储。
func (target *bluetoothTarget) roundTrip(req *http.Request) (*http.Response, error) {
	body := req.Body
	defer func() {
		if body != nil {
			body.Close()
		}
	}()
	ctx, cancel := context.WithCancel(req.Context())
	stopCancel := context.AfterFunc(target.ctx, cancel)
	release := func() {
		stopCancel()
		cancel()
	}
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-target.available:
	}

	finish := func() {
		if target.link != nil {
			// 切换图库或取消缩略图时只结束这次 HTTP 请求。配对窗口关闭后，
			// 一旦释放 GATT 链路，用户就要再按一次相框按键。
			cleanup, cancel := context.WithTimeout(target.ctx, 2*time.Second)
			err := target.link.Write(cleanup, []byte{closeSocket})
			cancel()
			if err != nil {
				target.dropLink()
			}
		}
		release()
		target.available <- struct{}{}
	}
	failed := func(err error) (*http.Response, error) {
		finish()
		return nil, fmt.Errorf("蓝牙通信失败：%w", err)
	}
	if target.ctx.Err() != nil || ctx.Err() != nil {
		release()
		target.available <- struct{}{}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, target.ctx.Err()
	}
	if target.link == nil {
		link, err := target.dial(ctx)
		if err != nil {
			return failed(err)
		}
		target.link = link
	}
	link := target.link
	maxWrite := min(link.MaxWrite(), 512)
	if maxWrite < 2 {
		return failed(errors.New("蓝牙写入容量不足"))
	}
	if err := link.Write(ctx, []byte{openSocket}); err != nil {
		return failed(err)
	}
	tunnel := &bluetoothStream{ctx: ctx, link: link, packet: make([]byte, maxWrite)}
	wire := req.Clone(ctx)
	wire.Close = true
	body = nil // Request.Write 接管并关闭请求体。
	if err := wire.Write(tunnel); err != nil {
		return failed(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(tunnel), wire)
	if err != nil {
		return failed(err)
	}
	response.Request = req
	response.Body = &bluetoothBody{ReadCloser: response.Body, finish: finish}
	return response, nil
}

type bluetoothStream struct {
	ctx     context.Context
	link    BluetoothLink
	packet  []byte
	pending []byte
}

func (s *bluetoothStream) Write(data []byte) (int, error) {
	written := 0
	s.packet[0] = writeSocket
	for written < len(data) {
		if err := s.ctx.Err(); err != nil {
			return written, err
		}
		size := copy(s.packet[1:], data[written:])
		if err := s.link.Write(s.ctx, s.packet[:size+1]); err != nil {
			return written, err
		}
		written += size
	}
	return written, nil
}

func (s *bluetoothStream) Read(dst []byte) (int, error) {
	for len(s.pending) == 0 {
		err := s.readPacket()
		if err != nil {
			return 0, err
		}
	}
	size := copy(dst, s.pending)
	s.pending = s.pending[size:]
	return size, nil
}

func (s *bluetoothStream) readPacket() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	packet, err := s.link.Read(s.ctx)
	if err != nil {
		return fmt.Errorf("读取蓝牙响应：%w", err)
	}
	if len(packet) != 0 {
		switch packet[0] {
		case socketData:
			s.pending = packet[1:]
			if len(s.pending) > 0 {
				return nil
			}
		case socketEOF:
			return io.EOF
		case socketError:
			return errors.New("相框内部 HTTP 连接已中断")
		case socketWaiting:
			select {
			case <-s.ctx.Done():
				return s.ctx.Err()
			case <-time.After(15 * time.Millisecond):
				return nil
			}
		}
	}
	return errors.New("相框蓝牙响应格式无效")
}

type bluetoothBody struct {
	io.ReadCloser
	once   sync.Once
	finish func()
}

// Close 通知相框关闭回环套接字，尚未读取的响应体随套接字一同丢弃。
func (b *bluetoothBody) Close() error {
	b.once.Do(b.finish)
	return nil
}
