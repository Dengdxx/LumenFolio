package gallery

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lumenfolio/panel"
)

func serverAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	addr := server.Listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestBrowsePictureFromSD(t *testing.T) {
	const name = "相片 + 100%.bin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("source") != "sd" {
			t.Errorf("浏览请求错误：%s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/frames":
			fmt.Fprintf(w, `{"available":true,"frames":[{"id":%q,"name":%q}]}`, name, name)
		case "/frame/thumb":
			http.NotFound(w, r)
		case "/frame":
			if got := r.URL.Query().Get("id"); got != name || strings.Contains(r.URL.RawQuery, "+") {
				t.Errorf("文件名编码错误：%q，解码后为 %q", r.URL.RawQuery, got)
			}
			frame := make([]byte, panel.FrameBytes)
			copy(frame, "EPD1")
			binary.LittleEndian.PutUint16(frame[8:], panel.Width)
			binary.LittleEndian.PutUint16(frame[10:], panel.Height)
			frame[12] = 0x56
			w.Write(frame)
		default:
			t.Errorf("未知路由：%s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	list, err := List(t.Context(), host, port, SD)
	if err != nil || len(list) != 1 || list[0] != (Picture{ID: name, Name: name}) {
		t.Fatalf("没有列出 SD 帧：%+v，%v", list, err)
	}
	preview, err := Preview(t.Context(), host, port, SD, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.NRGBAAt(1, 0); got != panel.Inks()[5].Preview {
		t.Fatalf("预览第二个像素应为绿色，实际 %v", got)
	}
	thumb, err := Thumbnail(t.Context(), host, port, SD, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if thumb.Bounds().Dx() != 120 || thumb.Bounds().Dy() != 160 || thumb.NRGBAAt(119, 159) != panel.Inks()[0].Preview {
		t.Fatalf("缩略图的比例或颜色错误：%v", thumb.Bounds())
	}
}

func TestThumbnailUsesBoardPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/frame/thumb" ||
			r.URL.Query().Get("source") != "artifacts" || r.URL.Query().Get("id") != "boot" {
			t.Errorf("缩略图请求错误：%s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		data := make([]byte, 120*160*2)
		binary.LittleEndian.PutUint16(data, 0xf800)
		binary.LittleEndian.PutUint16(data[len(data)-2:], 0x001f)
		w.Write(data)
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	thumb, err := Thumbnail(t.Context(), host, port, Flash, "boot")
	if err != nil {
		t.Fatal(err)
	}
	if thumb.Bounds().Dx() != 120 || thumb.Bounds().Dy() != 160 ||
		thumb.NRGBAAt(0, 0) != (color.NRGBA{R: 255, A: 255}) ||
		thumb.NRGBAAt(119, 159) != (color.NRGBA{B: 255, A: 255}) {
		t.Fatal("RGB565 缩略图尺寸、色码或行顺序错误")
	}
}

func TestThumbnailRetriesBusyQueueWithoutDownloadingFrame(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/frame/thumb" {
			t.Errorf("队列满时不应下载整帧：%s", r.URL)
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(make([]byte, thumbnailBytes))
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	if _, err := Thumbnail(t.Context(), host, port, SD, "one.bin"); err != nil || requests.Load() != 2 {
		t.Fatalf("应在队列释放后成功读取小图，实际请求 %d 次：%v", requests.Load(), err)
	}
}

func TestDisplayAndStorage(t *testing.T) {
	const name = "相片 + 100%.bin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/frame/display":
			if r.Method != http.MethodPost || r.ContentLength != 0 || r.URL.Query().Get("source") != "sd" || r.URL.Query().Get("id") != name {
				t.Errorf("显示请求错误：%s %s", r.Method, r.URL)
			}
			w.WriteHeader(http.StatusAccepted)
		case "/frames/storage":
			if r.Method != http.MethodGet {
				t.Errorf("容量请求错误：%s", r.Method)
			}
			fmt.Fprint(w, `{"artifacts":{"used_bytes":1450000,"total_bytes":12000000},"sd":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	if err := Display(t.Context(), host, port, SD, name); err != nil {
		t.Fatal(err)
	}
	storage, err := Storage(t.Context(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if storage.Flash.String() != "1 MB/12 MB（12.1%）" || storage.SD.String() != "容量不可用" {
		t.Fatalf("容量格式错误：%s / %s", storage.Flash, storage.SD)
	}
}

func TestListDistinguishesEmptyMissingSDAndOldFirmware(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		status           int
	}{
		{"empty", `{"available":true,"frames":[]}`, "", http.StatusOK},
		{"missing SD", `{"available":false,"frames":[]}`, "SD 卡未挂载", http.StatusOK},
		{"old firmware", ``, "更新固件", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			host, port := serverAddress(t, server)
			list, err := List(t.Context(), host, port, SD)
			if test.want == "" {
				if err != nil || len(list) != 0 {
					t.Fatalf("应返回空列表：%+v，%v", list, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("应返回 %q，实际 %v", test.want, err)
			}
		})
	}
}

func TestPreviewDistinguishesRemovedPictureAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	host, port := serverAddress(t, server)
	if _, err := Preview(t.Context(), host, port, Flash, "inbox"); err == nil || !strings.Contains(err.Error(), "找不到这张图片") {
		t.Fatalf("应提示图片已不在列表中，实际 %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := List(ctx, host, port, Flash); !errors.Is(err, context.Canceled) {
		t.Fatalf("应支持取消请求，实际 %v", err)
	}
}
