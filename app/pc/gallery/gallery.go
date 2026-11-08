// Package gallery 只读浏览相框 Flash 和 SD 卡中的面板帧，由 Windows 和 Android 共用。
package gallery

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumenfolio/panel"
)

// Source 指定相框上的存储来源。
type Source string

const (
	Flash Source = "artifacts" // Flash 中的开机槽和上传槽。
	SD    Source = "sd"        // SD 卡根目录的帧文件。
)

// Picture 是列表中的一张板端图片，ID 用于读取，Name 用于显示。
type Picture struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var client = &http.Client{
	Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
	Timeout:   15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// List 读取指定存储的图片清单。SD 未挂载和已挂载但没有图片分别返回错误和空列表。
func List(ctx context.Context, host string, port int, source Source) ([]Picture, error) {
	// SD 根目录最多 32 个帧文件，JSON 同时包含 ID 与名称。
	body, err := get(ctx, host, port, "/frames", url.Values{"source": {string(source)}}, 64*1024,
		"相框固件暂不支持浏览图片，请更新固件")
	if err != nil {
		return nil, err
	}
	var list struct {
		Available bool      `json:"available"`
		Frames    []Picture `json:"frames"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("读取相框图片列表：%w", err)
	}
	if !list.Available {
		if source == SD {
			return nil, fmt.Errorf("SD 卡未挂载")
		}
		return nil, fmt.Errorf("Flash artifacts 区不可用")
	}
	return list.Frames, nil
}

// Preview 读取指定存储中的一帧并解码为完整的六色预览，不修改相框画面。
func Preview(ctx context.Context, host string, port int, source Source, id string) (*image.NRGBA, error) {
	query := url.Values{"source": {string(source)}, "id": {id}}
	// ESP-IDF 返回原始查询串，由固件解码。空格按百分号编码，和文件名里的加号区分。
	body, err := get(ctx, host, port, "/frame", query, panel.FrameBytes,
		"相框上已找不到这张图片，请刷新列表")
	if err != nil {
		return nil, err
	}
	return panel.DecodeFrame(body)
}

// get 直连局域网相框。响应超过列表或帧格式允许的上限时停止读取。
func get(ctx context.Context, host string, port int, path string, query url.Values, limit int, missing string) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("端口超出范围：%d", port)
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: path,
		RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20")}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Close = true
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s", missing)
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, fmt.Errorf("HTTP %d：%s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("相框响应超过 %d 字节", limit)
	}
	return body, nil
}
