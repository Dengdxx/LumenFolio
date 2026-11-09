package frame

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSendToSDTracksSPI(t *testing.T) {
	data := rawFrame()
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/frame/transfer" {
			t.Errorf("请求路径错误：%s", r.URL)
		}
		query := r.URL.Query()
		switch r.Method {
		case http.MethodPost:
			name := query.Get("id")
			if query.Get("source") != "sd" || !strings.HasPrefix(name, "照片_") || !strings.HasSuffix(name, ".bin") || len(name) >= 96 {
				t.Errorf("SD 目标错误：%s", r.URL)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(body, data) || r.ContentLength != int64(len(data)) {
				t.Errorf("传图载荷不一致：%d 字节，%v", len(body), err)
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id":12}`)
		case http.MethodGet:
			if query.Get("id") != "12" {
				t.Errorf("轮询了错误的任务：%s", r.URL)
			}
			if polls.Add(1) == 1 {
				fmt.Fprint(w, `{"stage":"spi"}`)
			} else {
				fmt.Fprint(w, `{"stage":"done"}`)
			}
		}
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	var stages []string
	lastPercent := 0
	message, err := SendTo(t.Context(), host, port, data, SD, `C:\Pictures\照片?.png`, func(p Progress) {
		if p.Percent < lastPercent || (p.Percent == 100 && p.Stage != "done") {
			t.Errorf("进度错误：%+v", p)
		}
		lastPercent = p.Percent
		if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
			stages = append(stages, p.Stage)
		}
	})
	if err != nil || !strings.Contains(message, "SPI 写屏完成") || strings.Join(stages, ",") != "upload,storage,queued,spi,done" || lastPercent != 100 {
		t.Fatalf("没有按阶段交付：%v，%d%%，%s，%v", stages, lastPercent, message, err)
	}
}

func TestSendToLegacyNeverWritesFlashForSD(t *testing.T) {
	var legacy atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/frame" {
			legacy.Add(1)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, "已写入")
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	if _, err := SendTo(t.Context(), host, port, rawFrame(), SD, "photo.png", nil); err == nil || legacy.Load() != 0 {
		t.Fatalf("旧固件上的 SD 请求必须停止：%v，Flash 写入 %d 次", err, legacy.Load())
	}
	if message, err := SendTo(t.Context(), host, port, rawFrame(), Flash, "", nil); err != nil || legacy.Load() != 1 || !strings.Contains(message, "未提供 SPI 回执") {
		t.Fatalf("旧 Flash 接口回退失败：%s，%v", message, err)
	}
}
