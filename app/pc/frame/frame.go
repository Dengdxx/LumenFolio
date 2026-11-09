// Package frame 把一张六色面板图送到相框。
// 命令行和界面都调用这里，不依赖界面库。
package frame

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lumenfolio/devicehttp"
	"lumenfolio/panel"
)

// Send 把一帧 EPD1／EPD2 按实际编码长度 POST 到设备的 /frame。
//
// 设备不接受分块传输，所以必须带明确的 Content-Length。
// 发送前校验帧头与总长度；压缩流由设备显示任务解压校验。
// 设备返回 202 表示已经写入上传槽并排上了刷屏；其余状态视为失败。
// 返回设备回执文本。ctx 取消时中止发送。
func Send(ctx context.Context, host string, port int, frame []byte) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("端口超出范围：%d", port)
	}
	if err := panel.ValidateFrame(frame); err != nil {
		return "", err
	}
	base := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/frame", bytes.NewReader(frame))
	if err != nil {
		return "", err
	}
	request.ContentLength = int64(len(frame))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Close = true

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // 局域网直连设备，不走环境变量里的代理。
	defer transport.CloseIdleConnections()
	timeout := 3 * time.Minute
	if devicehttp.IsBluetooth(host) {
		timeout = devicehttp.BluetoothTransferTimeout
	}
	client := &http.Client{Transport: devicehttp.WithBluetooth(transport), Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	text := strings.TrimSpace(string(body))
	if response.StatusCode != http.StatusAccepted {
		return text, fmt.Errorf("HTTP %d: %s", response.StatusCode, text)
	}
	return text, nil
}
