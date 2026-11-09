package frame

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"lumenfolio/devicehttp"
	"lumenfolio/panel"
)

// Target 是一张新画面的保存位置。
type Target string

const (
	Flash Target = "artifacts" // 覆盖 Flash 上传槽。
	SD    Target = "sd"        // 向 SD 卡添加独立文件。
)

// Progress 描述一次传图的已知阶段和总进度估计。只有 SPI 交付完成才到 100。
type Progress struct {
	Stage   string
	Percent int
	Message string
}

// SendTo 写入选定存储并等待 SPI 交付回执。report 在本次调用内串行执行，可省略。
// 旧固件仅允许 Flash 回退。写入成功后若回执中断，返回的错误会说明图片已保存。
func SendTo(ctx context.Context, host string, port int, data []byte, target Target, name string, report func(Progress)) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("端口超出范围：%d", port)
	}
	if target != Flash && target != SD {
		return "", fmt.Errorf("图片存储位置无效")
	}
	if err := panel.ValidateFrame(data); err != nil {
		return "", err
	}
	if report == nil {
		report = func(Progress) {}
	}
	query := url.Values{"source": {string(target)}}
	if target == SD {
		query.Set("id", sdFrameName(name))
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/frame/transfer",
		RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20")}
	storage := "Flash"
	if target == SD {
		storage = "SD 卡"
	}
	timeout, uploading := 3*time.Minute, "正在无线上传…"
	if devicehttp.IsBluetooth(host) {
		timeout, uploading = devicehttp.BluetoothTransferTimeout, "正在通过蓝牙上传…"
	}
	reader := &transferReader{reader: bytes.NewReader(data), total: len(data), storage: storage, uploading: uploading, report: report}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), reader)
	if err != nil {
		return "", err
	}
	request.ContentLength = int64(len(data))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Close = true
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: devicehttp.WithBluetooth(transport), Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	report(Progress{"upload", 0, uploading})
	response, err := client.Do(request)
	reader.stopReporting()
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		if target == SD {
			return "", fmt.Errorf("相框固件暂不支持上传到 SD 卡，请更新固件")
		}
		report(Progress{"upload", 0, "正在通过旧固件接口传图…"})
		reply, err := Send(ctx, host, port, data)
		if err != nil {
			return reply, err
		}
		return reply + "。旧固件未提供 SPI 回执", nil
	}
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("HTTP %d：%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var receipt struct {
		ID uint32 `json:"id"`
	}
	if err := json.Unmarshal(body, &receipt); err != nil || receipt.ID == 0 {
		return "", fmt.Errorf("相框已接收画面，写屏回执无效")
	}
	report(Progress{"queued", 85, "已保存到" + storage + "，等待写屏…"})
	endpoint.RawQuery = url.Values{"id": {strconv.FormatUint(uint64(receipt.ID), 10)}}.Encode()
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return waitTransfer(wait, client, endpoint.String(), storage, report)
}

// transferReader 只估计请求体发送量，存储完成以相框的 HTTP 回执为准。
type transferReader struct {
	reader    *bytes.Reader
	total     int
	storage   string
	uploading string
	mu        sync.Mutex
	report    func(Progress)
}

func (r *transferReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > 0 && r.report != nil {
		if left := r.reader.Len(); left == 0 {
			r.report(Progress{"storage", 70, "正在完成" + r.storage + "写入…"})
		} else {
			r.report(Progress{"upload", (r.total - left) * 65 / r.total, r.uploading})
		}
	}
	return n, err
}

// stopReporting 在 HTTP 提前返回时隔离仍在退出的请求体写入 goroutine。
func (r *transferReader) stopReporting() {
	r.mu.Lock()
	r.report = nil
	r.mu.Unlock()
}

func waitTransfer(ctx context.Context, client *http.Client, endpoint, storage string, report func(Progress)) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := "queued"
	for {
		var state struct {
			Stage   string `json:"stage"`
			Message string `json:"message"`
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return "", err
		}
		request.Close = true
		response, err := client.Do(request)
		if err != nil {
			return "", fmt.Errorf("图片已保存，无法获取写屏回执：%w", err)
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&state)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("图片已保存，写屏回执不可用")
		}
		switch state.Stage {
		case "queued":
		case "spi":
			if last != state.Stage {
				report(Progress{"spi", 92, "正在通过 SPI 写入屏幕…"})
			}
		case "done":
			message := "已保存到" + storage + "，SPI 写屏完成"
			report(Progress{"done", 100, message})
			return message, nil
		case "failed":
			if state.Message == "" {
				state.Message = "相框未能写屏"
			}
			return "", fmt.Errorf("图片已保存，%s", strings.TrimPrefix(state.Message, "已保存，"))
		default:
			return "", fmt.Errorf("图片已保存，写屏阶段未知")
		}
		last = state.Stage
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("图片已保存，等待写屏回执：%w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func sdFrameName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(strings.Map(func(ch rune) rune {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == ' ' || ch == '-' || ch == '_' {
			return ch
		}
		return '_'
	}, strings.TrimSuffix(name, path.Ext(name))))
	if len(name) > 60 {
		name = name[:60]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	if name == "" || name == "_" {
		name = "photo"
	}
	return fmt.Sprintf("%s-%d.bin", name, time.Now().UnixNano())
}
