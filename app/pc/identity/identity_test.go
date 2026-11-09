package identity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityFromStatusAndBluetooth(t *testing.T) {
	base := ID("7c:df:a1:e2:3f:28")
	if got := Parse("7C:DF:A1:E2:3F:28"); got != base {
		t.Fatalf("大写地址应归一为 %s，实际 %s", base, got)
	}
	if got := FromBluetooth("7C:DF:A1:E2:3F:2A"); got != base {
		t.Fatalf("蓝牙地址应推出基 MAC %s，实际 %s", base, got)
	}
	if FromBluetooth("7c:df:a1:e2:3f:01") != "" || Parse("不是地址") != "" {
		t.Fatal("借位或格式不对的地址不能当成身份")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ota/status" {
			t.Errorf("请求路径错误：%s", r.URL.Path)
		}
		switch r.URL.Query().Get("id") {
		case "missing":
			w.Write([]byte(`{"project":"LumenFolio"}`))
		case "bad":
			w.Write([]byte(`{"id":"not-a-mac"}`))
		default:
			w.Write([]byte(`{"id":"7C:DF:A1:E2:3F:28","project":"LumenFolio"}`))
		}
	}))
	defer server.Close()

	got, err := Read(t.Context(), server.Client(), server.URL+"/ota/status")
	if err != nil || got != base {
		t.Fatalf("状态身份错误：%q，%v", got, err)
	}
	got, err = Read(t.Context(), server.Client(), server.URL+"/ota/status?id=missing")
	if err != nil || got != "" {
		t.Fatalf("旧固件应返回空身份：%q，%v", got, err)
	}
	if _, err = Read(t.Context(), server.Client(), server.URL+"/ota/status?id=bad"); err == nil || !strings.Contains(err.Error(), "无法识别") {
		t.Fatalf("坏掉的身份应报错：%v", err)
	}
}
