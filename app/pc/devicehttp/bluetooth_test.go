package devicehttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeBluetooth struct {
	request   bytes.Buffer
	response  string
	offset    int
	failWrite bool
	wait      bool
}

func (*fakeBluetooth) MaxWrite() int { return 20 }
func (*fakeBluetooth) Close()        {}

func (link *fakeBluetooth) Write(_ context.Context, packet []byte) error {
	switch packet[0] {
	case openSocket:
		link.request.Reset()
		link.offset = 0
	case writeSocket:
		if len(packet) > link.MaxWrite() {
			return errors.New("ATT packet too large")
		}
		if link.failWrite {
			return io.ErrClosedPipe
		}
		link.request.Write(packet[1:])
	case closeSocket:
		if link.failWrite {
			return io.ErrClosedPipe
		}
	}
	return nil
}

func (link *fakeBluetooth) Read(ctx context.Context) ([]byte, error) {
	if link.wait {
		<-ctx.Done()
		link.wait = false
		return nil, ctx.Err()
	}
	if link.offset == len(link.response) {
		return []byte{socketEOF}, nil
	}
	end := min(link.offset+11, len(link.response))
	packet := append([]byte{socketData}, link.response[link.offset:end]...)
	link.offset = end
	return packet, nil
}

func TestBluetoothRoundTripStreamsHTTP(t *testing.T) {
	link := &fakeBluetooth{response: "HTTP/1.1 202 Accepted\r\nTransfer-Encoding: chunked\r\n\r\n4\r\ndone\r\n0\r\n\r\n"}
	host := UseBluetooth("01:02:03:04:05:06", func(context.Context) (BluetoothLink, error) { return link, nil })
	defer ForgetBluetooth(host)
	client := &http.Client{Transport: WithBluetooth(&http.Transport{})}
	data := strings.Repeat("frame", 41)
	response, err := client.Post("http://"+host+":8080/frame/transfer?source=sd", "application/octet-stream", strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted || string(reply) != "done" || err != nil {
		t.Fatalf("response = %d, %q, %v", response.StatusCode, reply, err)
	}
	request, err := http.ReadRequest(bufio.NewReader(&link.request))
	if err != nil {
		t.Fatal(err)
	}
	defer request.Body.Close()
	payload, err := io.ReadAll(request.Body)
	if request.RequestURI != "/frame/transfer?source=sd" || request.ContentLength != int64(len(data)) || string(payload) != data || err != nil {
		t.Fatalf("request = %s, %d bytes, %v", request.RequestURI, len(payload), err)
	}
}

func TestBluetoothDoesNotReplayAndCanReconnect(t *testing.T) {
	calls := 0
	host := UseBluetooth("01:02:03:04:05:07", func(context.Context) (BluetoothLink, error) {
		calls++
		return &fakeBluetooth{response: "HTTP/1.1 204 No Content\r\n\r\n", failWrite: calls == 1}, nil
	})
	defer ForgetBluetooth(host)
	client := &http.Client{Transport: WithBluetooth(&http.Transport{})}
	if _, err := client.Post("http://"+host+"/ota", "application/octet-stream", strings.NewReader("update")); err == nil || calls != 1 {
		t.Fatalf("failed upload was replayed: dials=%d, err=%v", calls, err)
	}
	response, err := client.Get("http://" + host + "/ota/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if calls != 2 || response.StatusCode != http.StatusNoContent {
		t.Fatalf("reconnect = %d dials, HTTP %d", calls, response.StatusCode)
	}
}

func TestBluetoothSerializesUntilBodyClose(t *testing.T) {
	link := &fakeBluetooth{response: "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\ndone"}
	host := UseBluetooth("01:02:03:04:05:08", func(context.Context) (BluetoothLink, error) { return link, nil })
	defer ForgetBluetooth(host)
	client := &http.Client{Transport: WithBluetooth(&http.Transport{})}
	url := "http://" + host + "/frame"
	first, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second request did not wait: %v", err)
	}
	first.Body.Close()
	next, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	next.Body.Close()
}

func TestBluetoothCancelKeepsLink(t *testing.T) {
	link := &fakeBluetooth{response: "HTTP/1.1 204 No Content\r\n\r\n", wait: true}
	dials := 0
	host := UseBluetooth("01:02:03:04:05:09", func(context.Context) (BluetoothLink, error) {
		dials++
		return link, nil
	})
	defer ForgetBluetooth(host)
	client := &http.Client{Transport: WithBluetooth(&http.Transport{})}
	url := "http://" + host + "/frame/thumb"
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read was not canceled: %v", err)
	}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if dials != 1 {
		t.Fatalf("cancellation closed the physical link, dials = %d", dials)
	}
}
