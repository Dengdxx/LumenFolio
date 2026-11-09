package frame

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"lumenfolio/panel"
)

func rawFrame() []byte {
	frame := bytes.Repeat([]byte{0x11}, panel.FrameBytes)
	copy(frame, "EPD1")
	clear(frame[4:8])
	binary.LittleEndian.PutUint16(frame[8:], panel.Width)
	binary.LittleEndian.PutUint16(frame[10:], panel.Height)
	return frame
}

func serverAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestSendPostsRawAndCompressedFrames(t *testing.T) {
	frames := map[string][]byte{
		"EPD1": rawFrame(),
		"EPD2": panel.Encode(image.NewNRGBA(image.Rect(0, 0, panel.Width, panel.Height))),
	}
	for name, want := range frames {
		t.Run(name, func(t *testing.T) {
			var got []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/frame" || r.Method != http.MethodPost || r.ContentLength != int64(len(want)) ||
					len(r.TransferEncoding) != 0 || r.Header.Get("Content-Type") != "application/octet-stream" {
					t.Errorf("请求不对：%s %s 长度 %d 编码 %v 类型 %s",
						r.Method, r.URL.Path, r.ContentLength, r.TransferEncoding, r.Header.Get("Content-Type"))
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				got = body
				w.WriteHeader(http.StatusAccepted)
				w.Write([]byte("已写入，正在刷上屏\n"))
			}))
			defer server.Close()
			host, port := serverAddress(t, server)
			reply, err := Send(context.Background(), host, port, want)
			if err != nil {
				t.Fatal(err)
			}
			if reply != "已写入，正在刷上屏" || !bytes.Equal(got, want) {
				t.Fatal("帧内容或回执不一致")
			}
		})
	}
}

func TestSendReportsRejectionAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 收完请求体再拒绝，Windows 才能收到错误响应而非未读请求体导致的 TCP 重置。
		io.Copy(io.Discard, r.Body)
		http.Error(w, "帧格式不支持", http.StatusUnprocessableEntity)
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	if _, err := Send(context.Background(), host, port, rawFrame()); err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("拒绝应带回状态码，实际 %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Send(ctx, host, port, rawFrame()); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应中止发送，实际 %v", err)
	}
}

func TestSendRejectsInvalidFrameBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	host, port := serverAddress(t, server)
	badMagic := rawFrame()
	copy(badMagic, "NOPE")
	for _, frame := range [][]byte{nil, []byte("short"), badMagic, rawFrame()[:100]} {
		if _, err := Send(context.Background(), host, port, frame); err == nil {
			t.Fatal("非法帧不应发出")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("非法帧触发了网络请求")
	}
}
