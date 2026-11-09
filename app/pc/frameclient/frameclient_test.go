package frameclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lumenfolio/devicehttp"
	"lumenfolio/frame"
	"lumenfolio/gallery"
)

func TestWiFiSessionUsesTheDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/frames/storage" {
			t.Errorf("请求路径错误：%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"artifacts":{"used_bytes":1,"total_bytes":2},"sd":{"used_bytes":0,"total_bytes":0}}`))
	}))
	defer server.Close()

	session, err := WiFi(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	usage, err := session.Storage(context.Background())
	if err != nil || usage.Flash.TotalBytes != 2 {
		t.Fatalf("容量错误：%+v，%v", usage, err)
	}
	session.Close()
	if _, err := session.Storage(context.Background()); err == nil {
		t.Fatal("关闭后的会话不应再访问相框")
	}
}

func TestBluetoothSessionClosesItsLink(t *testing.T) {
	dials := 0
	session := OpenBluetooth("LumenFolio", "01:02:03:04:05:06", func(context.Context) (devicehttp.BluetoothLink, error) {
		dials++
		return nil, context.Canceled
	})
	if session.Kind() != LinkBluetooth || session.Name() != "LumenFolio" {
		t.Fatalf("会话信息不对：%s %s", session.Kind(), session.Name())
	}
	if _, err := session.Send(context.Background(), []byte{1}, frame.Flash, "", nil); err == nil {
		t.Fatal("无效帧不应发到相框")
	}
	session.Close()
	session.Close()
	if _, err := session.Pictures(context.Background(), gallery.SD, ""); err == nil || dials != 0 {
		t.Fatalf("关闭后仍在使用蓝牙：%v，拨号 %d 次", err, dials)
	}
}

func TestWiFiRejectsABadPort(t *testing.T) {
	if _, err := WiFi("192.168.1.2:99999"); err == nil {
		t.Fatal("越界端口应拒绝")
	}
	session, err := WiFi("frame.local")
	if err != nil || session.port != 8080 {
		t.Fatalf("缺省端口错误：%v，%d", err, session.port)
	}
}
