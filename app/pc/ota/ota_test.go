package ota

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var otaTestID = strings.Repeat("ab", 32)

func otaFixtureImage(t *testing.T, project, version string) (string, []byte) {
	t.Helper()
	data := make([]byte, 16000)
	data[0] = 0xe9
	binary.LittleEndian.PutUint32(data[32:36], 0xabcd5432)
	copy(data[48:80], version)
	copy(data[80:112], project)
	for i := 176; i < 208; i++ {
		data[i] = 0xab
	}
	path := filepath.Join(t.TempDir(), "firmware.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func otaAddress(t *testing.T, server *httptest.Server) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestOTASuccessAndLostReplies(t *testing.T) {
	for _, loseReplies := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost replies"}[loseReplies], func(t *testing.T) {
			path, data := otaFixtureImage(t, "LumenFolio", "new-build")
			status := otaStatus{Phase: "idle", Ready: false, Project: "LumenFolio", Version: "old-build", ImageID: strings.Repeat("cd", 32),
				RunningPartition: "factory", BootPartition: "factory", UpdatePartition: "ota_0", Capacity: 2 * 1024 * 1024}
			var mu sync.Mutex
			uploads, confirmations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/ota/status":
					if r.Method != http.MethodGet {
						t.Errorf("status method %s", r.Method)
					}
					json.NewEncoder(w).Encode(status)
				case "/ota":
					uploads++
					if r.Method != http.MethodPost || r.ContentLength != int64(len(data)) || len(r.TransferEncoding) != 0 || r.Header.Get("Content-Type") != "application/octet-stream" {
						t.Errorf("wrong raw upload: method=%s length=%d encoding=%v", r.Method, r.ContentLength, r.TransferEncoding)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil || !bytes.Equal(body, data) {
						t.Errorf("uploaded image changed: %v", err)
					}
					accepted := status
					accepted.Phase = "reboot_pending"
					accepted.BootPartition = "ota_0"
					status.Phase = "pending_verify"
					status.Ready = true
					status.PendingVerify = true
					status.Version = "new-build"
					status.ImageID = otaTestID
					status.RunningPartition = "ota_0"
					status.BootPartition = "ota_0"
					status.UpdatePartition = "ota_1"
					if loseReplies {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						connection.Close()
						return
					}
					w.WriteHeader(http.StatusAccepted)
					json.NewEncoder(w).Encode(accepted)
				case "/ota/confirm":
					confirmations++
					if !status.PendingVerify || r.Method != http.MethodPost || r.Header.Get("X-OTA-Partition") != "ota_0" || r.Header.Get("X-OTA-Version") != "new-build" || r.Header.Get("X-OTA-Image") != otaTestID || r.ContentLength != 0 {
						t.Error("wrong confirmation")
					}
					status.PendingVerify = false
					status.Phase = "idle"
					if loseReplies {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						connection.Close()
						return
					}
					json.NewEncoder(w).Encode(status)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			host, port := otaAddress(t, server)
			var out, errOut bytes.Buffer
			// 走真实的命令行入口。界面不经过 run，命令行只分发 ota。
			code := Run(context.Background(), host, []string{"--http-port", port, "--wait-timeout", "2s", path}, &out, &errOut)
			if code != 0 || !strings.Contains(out.String(), "已在 ota_0 启动并确认") {
				t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
			}
			mu.Lock()
			defer mu.Unlock()
			if uploads != 1 || confirmations != 1 {
				t.Fatalf("uploads=%d confirmations=%d", uploads, confirmations)
			}
		})
	}
}

func TestOTARejectsInvalidInputAndServerErrors(t *testing.T) {
	path, _ := otaFixtureImage(t, "LumenFolio", "candidate")
	for _, test := range []struct {
		name      string
		capacity  int64
		phase     string
		httpError bool
	}{
		{"too large", 100, "idle", false}, {"busy", 2 << 20, "receiving", false}, {"validation failed", 2 << 20, "idle", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			uploads, confirms := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/ota/status" {
					json.NewEncoder(w).Encode(otaStatus{Phase: test.phase, Ready: true, Project: "LumenFolio", Version: "old", ImageID: otaTestID, RunningPartition: "factory", BootPartition: "factory", UpdatePartition: "ota_0", Capacity: test.capacity})
					return
				}
				if r.URL.Path == "/ota" {
					uploads++
					http.Error(w, "invalid image", 422)
					return
				}
				confirms++
				http.Error(w, "unexpected", 500)
			}))
			defer server.Close()
			host, port := otaAddress(t, server)
			var out, errOut bytes.Buffer
			if Run(context.Background(), host, []string{"--http-port", port, path}, &out, &errOut) == 0 {
				t.Fatal("unexpected success")
			}
			mu.Lock()
			defer mu.Unlock()
			want := 0
			if test.httpError {
				want = 1
			}
			if uploads != want || confirms != 0 {
				t.Fatalf("uploads=%d confirms=%d", uploads, confirms)
			}
		})
	}
	wrong, _ := otaFixtureImage(t, "another-project", "version")
	var out, errOut bytes.Buffer
	if Run(context.Background(), "127.0.0.1", []string{wrong}, &out, &errOut) == 0 || !strings.Contains(errOut.String(), "LumenFolio") {
		t.Fatal("wrong project accepted")
	}
	bad := filepath.Join(t.TempDir(), "bootloader.bin")
	os.WriteFile(bad, make([]byte, 1024), 0600)
	if Run(context.Background(), "127.0.0.1", []string{bad}, &out, &errOut) == 0 {
		t.Fatal("non-application accepted")
	}
}

func TestOTAConfirmationTimeoutAndRollback(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      otaStatus
		wantTimeout bool
	}{
		{"not ready", otaStatus{Phase: "pending_verify", PendingVerify: true, Version: "new", RunningPartition: "ota_0", BootPartition: "ota_0"}, true},
		{"wrong image", otaStatus{Phase: "pending_verify", PendingVerify: true, Ready: true, Version: "other", RunningPartition: "ota_0", BootPartition: "ota_0"}, true},
		{"same version different build", otaStatus{Phase: "pending_verify", PendingVerify: true, Ready: true, Version: "new", ImageID: strings.Repeat("cd", 32), RunningPartition: "ota_0", BootPartition: "ota_0"}, true},
		{"rolled back", otaStatus{Phase: "idle", Ready: true, Version: "old", RunningPartition: "factory", BootPartition: "factory"}, false},
		{"rollback failed", otaStatus{Phase: "rollback_failed", PendingVerify: true, Version: "new", RunningPartition: "ota_0", BootPartition: "ota_0", LastError: 123}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.status.Project = "LumenFolio"
			if test.status.ImageID == "" {
				test.status.ImageID = otaTestID
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("unready/mismatched image must not be confirmed")
				}
				json.NewEncoder(w).Encode(test.status)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			err := waitOTA(ctx, server.Client(), server.URL, otaImage{Project: "LumenFolio", Version: "new", ImageID: otaTestID}, "ota_0", time.Millisecond)
			if err == nil {
				t.Fatal("unexpected success")
			}
			if test.wantTimeout && !strings.Contains(err.Error(), "结果未确认") {
				t.Fatal(err)
			}
		})
	}
}

func TestOTAResumePendingAndStatus(t *testing.T) {
	path, _ := otaFixtureImage(t, "LumenFolio", "new")
	var mu sync.Mutex
	status := otaStatus{Phase: "pending_verify", Ready: true, PendingVerify: true, Project: "LumenFolio", Version: "new", ImageID: otaTestID, RunningPartition: "ota_1", BootPartition: "ota_1", UpdatePartition: "ota_0", Capacity: 2 << 20}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/ota" {
			t.Error("must not upload over a pending image")
		}
		if r.URL.Path == "/ota/confirm" {
			if r.Header.Get("X-OTA-Partition") != "ota_1" {
				t.Error("wrong pending partition")
			}
			status.PendingVerify = false
			status.Phase = "idle"
		}
		json.NewEncoder(w).Encode(status)
	}))
	defer server.Close()
	host, port := otaAddress(t, server)
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), host, []string{"--http-port", port, "--wait-timeout", "2s", path}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	out.Reset()
	if code := Run(context.Background(), host, []string{"--http-port", port, "status"}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, &errOut)
	}
	var current otaStatus
	if err := json.Unmarshal(out.Bytes(), &current); err != nil || current.PendingVerify || current.RunningPartition != "ota_1" {
		t.Fatalf("%v %s", err, &out)
	}
}

type otaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f otaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestOTAEarlyRejectionStopsProgress(t *testing.T) {
	path, _ := otaFixtureImage(t, "LumenFolio", "new")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	image, err := readOTAImage(file)
	if err != nil {
		t.Fatal(err)
	}
	var body io.ReadCloser
	client := &http.Client{Transport: otaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		body = request.Body
		return &http.Response{StatusCode: 409, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("busy")), Request: request}, nil
	})}
	var out bytes.Buffer
	_, uncertain, err := uploadOTA(context.Background(), client, "http://device", file, image, &out)
	if err == nil || uncertain {
		t.Fatalf("uncertain=%v error=%v", uncertain, err)
	}
	// RoundTripper may finish consuming/closing the request body asynchronously after Do returns.
	if _, err := io.Copy(io.Discard, body); err != nil {
		t.Fatal(err)
	}
	body.Close()
	if out.Len() != 0 {
		t.Fatalf("late progress after rejection: %s", &out)
	}
}
