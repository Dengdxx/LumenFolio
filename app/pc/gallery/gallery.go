// Package gallery 浏览相框 Flash 和 SD 卡中的面板帧，并请求显示选中的画面。
package gallery

import (
	"context"
	"encoding/binary"
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
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision string `json:"revision"` // 空值表示该固件未提供可跨列表复用的版本。
}

// Page 是按文件名排列的一页图片，Next 是下一页的游标，空串表示末页。
type Page struct {
	Pictures []Picture `json:"frames"`
	Next     string    `json:"next"`
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

const (
	thumbnailWidth  = 120
	thumbnailHeight = 160
	thumbnailBytes  = thumbnailWidth * thumbnailHeight * 2
)

type responseError struct {
	status  int
	message string
}

func (e responseError) Error() string { return e.message }

var client = &http.Client{
	Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
	Timeout:   15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// List 读取指定存储的图片清单。SD 未挂载和已挂载但没有图片分别返回错误和空列表。
func List(ctx context.Context, host string, port int, source Source) ([]Picture, error) {
	page, err := readPage(ctx, host, port, source, url.Values{"source": {string(source)}})
	return page.Pictures, err
}

// ListPage 读取一页图库，首次读取时 after 传空串。旧固件返回的原列表作为末页。
func ListPage(ctx context.Context, host string, port int, source Source, after string) (Page, error) {
	query := url.Values{"source": {string(source)}}
	if source == SD {
		query.Set("limit", "16")
		if after != "" {
			query.Set("after", after)
		}
	}
	page, err := readPage(ctx, host, port, source, query)
	if err == nil && after != "" && page.Next == after {
		return Page{}, fmt.Errorf("相框返回了重复的分页游标")
	}
	return page, err
}

func readPage(ctx context.Context, host string, port int, source Source, query url.Values) (Page, error) {
	body, err := request(ctx, http.MethodGet, host, port, "/frames", query, http.StatusOK, 64*1024,
		"相框固件暂不支持浏览图片，请更新固件")
	if err != nil {
		return Page{}, err
	}
	var list struct {
		Available bool `json:"available"`
		Page
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return Page{}, fmt.Errorf("读取相框图片列表：%w", err)
	}
	if !list.Available {
		if source == SD {
			return Page{}, fmt.Errorf("SD 卡未挂载")
		}
		return Page{}, fmt.Errorf("Flash artifacts 区不可用")
	}
	return list.Page, nil
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

// Thumbnail 优先读取板端的 120×160 小图，旧固件或板端暂无法生成小图时按整帧缩放。
func Thumbnail(ctx context.Context, host string, port int, source Source, id string) (*image.NRGBA, error) {
	query := frameQuery(source, id)
	for attempt := 0; ; attempt++ {
		body, err := request(ctx, http.MethodGet, host, port, "/frame/thumb", query, http.StatusOK, thumbnailBytes,
			"相框上已找不到这张图片，请刷新列表")
		if err == nil {
			return decodeThumbnail(body)
		}
		status, ok := err.(responseError)
		if !ok {
			return nil, err
		}
		if status.status == http.StatusTooManyRequests && attempt < 4 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		if status.status != http.StatusNotFound && status.status != http.StatusServiceUnavailable {
			return nil, err
		}
		break
	}

	full, err := Preview(ctx, host, port, source, id)
	if err != nil {
		return nil, err
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, thumbnailWidth, thumbnailHeight))
	draw.BiLinear.Scale(thumb, thumb.Bounds(), full, full.Bounds(), draw.Src, nil)
	return thumb, nil
}

// decodeThumbnail 还原行优先的 RGB565 小端像素，完整载荷才可显示。
func decodeThumbnail(data []byte) (*image.NRGBA, error) {
	if len(data) != thumbnailBytes {
		return nil, fmt.Errorf("缩略图长度错误：收到 %d 字节", len(data))
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, thumbnailWidth, thumbnailHeight))
	for i := range thumbnailWidth * thumbnailHeight {
		pixel := binary.LittleEndian.Uint16(data[2*i:])
		r, g, b := uint8(pixel>>11), uint8(pixel>>5&0x3f), uint8(pixel&0x1f)
		copy(thumb.Pix[4*i:], []byte{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2, 255})
	}
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
		return nil, responseError{status: response.StatusCode, message: missing}
	}
	if response.StatusCode != wantStatus {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, responseError{status: response.StatusCode,
			message: fmt.Sprintf("HTTP %d：%s", response.StatusCode, strings.TrimSpace(string(body)))}
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
