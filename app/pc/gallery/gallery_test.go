package gallery

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
