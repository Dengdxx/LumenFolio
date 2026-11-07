package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"testing/synctest"

	"framefolio/panel"
)

// waitingWindow 模拟空闲的操作系统窗口，只能由 Invalidate 唤醒。
type waitingWindow struct {
	wake chan struct{}
}

func (w *waitingWindow) Invalidate() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func TestOpenResultWakesWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		u := newTestUI()
		window := &waitingWindow{wake: make(chan struct{}, 1)}
		u.window = window
		// 未配置系统文件选择器时，实际 explorer API 返回不可用错误。
		u.onOpen()
		synctest.Wait()
		select {
		case <-window.wake:
			select {
			case result := <-u.openResult:
				if result.err == nil {
					t.Fatal("文件选择器错误应交给 UI 显示")
				}
			default:
				t.Fatal("应先发布读图结果，再唤醒窗口")
			}
		default:
			t.Fatal("后台文件选择结束后未唤醒空闲窗口")
		}
	})
}

func TestReadImageResultDelivery(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 128})
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		data    []byte
		cancel  bool
		wantErr bool
	}{
		{"成功解码 PNG", encoded.Bytes(), false, false},
		{"解码错误", []byte("not an image"), false, true},
		{"取消选择", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				u := newTestUI()
				window := &waitingWindow{wake: make(chan struct{}, 1)}
				u.window = window
				go func() {
					result := openResult{}
					if !tc.cancel {
						result = u.readImage(io.NopCloser(bytes.NewReader(tc.data)))
					}
					u.postOpenResult(result)
				}()
				synctest.Wait()
				select {
				case <-window.wake:
				default:
					t.Fatal("文件结果未唤醒空闲窗口")
				}
				result := <-u.openResult
				if (result.err != nil) != tc.wantErr {
					t.Fatalf("解码错误状态应为 %v，实际 %v", tc.wantErr, result.err)
				}
				wantImage := !tc.cancel && !tc.wantErr
				if (result.img != nil) != wantImage {
					t.Fatalf("图片存在状态应为 %v", wantImage)
				}
				if wantImage && color.NRGBAModel.Convert(result.img.At(0, 0)) != (color.NRGBA{255, 0, 0, 128}) {
					t.Fatal("解码结果应保留 PNG 的颜色与透明度")
				}
			})
		})
	}
}

func TestDitherDeliversNewestResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		u := newTestUI()
		u.rawImage = image.NewNRGBA(image.Rect(0, 0, 12, 16))
		u.crop = panel.NewCrop(u.rawImage.Bounds())
		window := &waitingWindow{wake: make(chan struct{}, 1)}
		u.window = window
		u.triggerDither()
		synctest.Wait()
		select {
		case <-window.wake:
		default:
			t.Fatal("首个抖动结果未唤醒窗口")
		}

		// UI 尚未取走旧结果时再次调整构图，新任务也必须能够交付。
		u.crop.RotateAroundFrameCenter(90)
		u.triggerDither()
		wantVersion := u.cropVersion
		synctest.Wait()
		old := <-u.done
		if old.version == wantVersion {
			t.Fatal("测试应先收到先前构图的结果")
		}
		synctest.Wait()
		select {
		case current := <-u.done:
			if current.version != wantVersion || current.img == nil || current.src == nil {
				t.Fatalf("最新构图的完整结果未交付：版本 %d，期望 %d", current.version, wantVersion)
			}
		default:
			t.Fatal("旧结果占据队列时，最新抖动结果被丢弃")
		}
		select {
		case <-window.wake:
		default:
			t.Fatal("最新结果交付后应再次唤醒窗口")
		}
	})
}
