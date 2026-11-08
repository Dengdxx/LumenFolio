// Package gallery 浏览相框 Flash 和 SD 卡中的面板帧，并请求显示选中的画面。
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

	"golang.org/x/image/draw"

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

// Usage 是一个存储区的占用字节数和总字节数。总量为零表示容量不可用。
type Usage struct {
	UsedBytes  uint64 `json:"used_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

// String 用十进制 MB 显示容量，百分比从未舍入的字节数计算。
func (u Usage) String() string {
	if u.TotalBytes == 0 {
		return "容量不可用"
	}
	const mb = 1_000_000
	return fmt.Sprintf("%d MB/%d MB（%.1f%%）", (u.UsedBytes+mb/2)/mb, (u.TotalBytes+mb/2)/mb,
		100*float64(u.UsedBytes)/float64(u.TotalBytes))
}

// StorageUsage 是 Flash artifacts 和 SD 卡的容量快照。两区可以分别不可用。
type StorageUsage struct {
	Flash Usage `json:"artifacts"`
	SD    Usage `json:"sd"`
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
	body, err := request(ctx, http.MethodGet, host, port, "/frames", url.Values{"source": {string(source)}}, http.StatusOK, 64*1024,
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
	body, err := request(ctx, http.MethodGet, host, port, "/frame", frameQuery(source, id), http.StatusOK, panel.FrameBytes,
		"相框上已找不到这张图片，请刷新列表")
	if err != nil {
		return nil, err
	}
	return panel.DecodeFrame(body)
}

// Thumbnail 读取现有面板帧，以面积加权的缩放生成 120×160 列表预览。
func Thumbnail(ctx context.Context, host string, port int, source Source, id string) (*image.NRGBA, error) {
	full, err := Preview(ctx, host, port, source, id)
	if err != nil {
		return nil, err
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, panel.Width/10, panel.Height/10))
	draw.BiLinear.Scale(thumb, thumb.Bounds(), full, full.Bounds(), draw.Src, nil)
	return thumb, nil
}

// Display 请求相框从指定存储读取并显示该帧。成功只表示已安排刷新。
func Display(ctx context.Context, host string, port int, source Source, id string) error {
	_, err := request(ctx, http.MethodPost, host, port, "/frame/display", frameQuery(source, id), http.StatusAccepted, 1024,
		"找不到画面，或固件尚未支持显示选中的画面。请刷新列表或更新固件")
	return err
}

// Storage 读取两种存储的容量，SD 未挂载时该项的总量为零。
func Storage(ctx context.Context, host string, port int) (StorageUsage, error) {
	body, err := request(ctx, http.MethodGet, host, port, "/frames/storage", nil, http.StatusOK, 1024,
		"相框固件暂不支持读取容量")
	if err != nil {
		return StorageUsage{}, err
	}
	var usage StorageUsage
	if err := json.Unmarshal(body, &usage); err != nil {
		return StorageUsage{}, fmt.Errorf("读取存储容量：%w", err)
	}
	return usage, nil
}

func frameQuery(source Source, id string) url.Values {
	return url.Values{"source": {string(source)}, "id": {id}}
}

// request 直连局域网相框，并按接口允许的字节数限制响应。
func request(ctx context.Context, method, host string, port int, path string, query url.Values, wantStatus, limit int, missing string) ([]byte, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("端口超出范围：%d", port)
	}
	// ESP-IDF 返回原始查询串。百分号编码空格，和文件名里的加号区分。
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: path,
		RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20")}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
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
	if response.StatusCode != wantStatus {
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
