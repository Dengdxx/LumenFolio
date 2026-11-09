package cli

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lumenfolio/panel"
)

func solid(c color.NRGBA, w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestExportWritesOneFrame(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "white.png")
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, solid(color.NRGBA{245, 245, 240, 255}, 30, 40)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.bin")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--alg", "0", srcPath, dst}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) >= panel.FrameBytes || !strings.Contains(out.String(), fmt.Sprintf("（%d 字节）", len(got))) {
		t.Fatalf("应报告实际压缩长度，实际 %d 字节、输出 %s", len(got), &out)
	}
	for _, b := range exportPixels(t, got) {
		if b != 0x11 {
			t.Fatalf("白图最近色应全是白墨水 0x11，出现 %02x", b)
		}
	}
}

func TestFrameCommandPostsCurrentPicture(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "red.png")
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, solid(color.NRGBA{190, 30, 40, 255}, panel.Width, panel.Height)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcPath, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/frame/transfer" {
			t.Errorf("请求路径错误：%s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"stage":"done"}`))
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		got = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"--host", host, "frame", "--http-port", portText, "--alg", "0", srcPath}, &out, &errOut)
	if code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	if len(got) >= panel.FrameBytes {
		t.Fatalf("红图应压缩，实际 %d 字节", len(got))
	}
	for _, b := range exportPixels(t, got) {
		if b != 0x33 {
			t.Fatalf("红图解压后应为红墨水，出现 %02x", b)
		}
	}
}

func exportPixels(t *testing.T, frame []byte) []byte {
	t.Helper()
	if err := panel.ValidateFrame(frame); err != nil {
		t.Fatal(err)
	}
	if string(frame[:4]) != "EPD2" {
		t.Fatal("纯色图片应输出 EPD2")
	}
	reader, err := zlib.NewReader(bytes.NewReader(frame[16:]))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	pixels, err := io.ReadAll(reader)
	if err != nil || len(pixels) != panel.Width*panel.Height/2 {
		t.Fatalf("解压错误：%v，得到 %d 字节", err, len(pixels))
	}
	return pixels
}
