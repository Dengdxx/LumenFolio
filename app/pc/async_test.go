package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"testing/synctest"

	"gioui.org/layout"
	"gioui.org/op/paint"

	"lumenfolio/gallery"
	"lumenfolio/panel"
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

func TestGalleryDiscardsQueuedResultOnSourceSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		u := newTestUI()
		window := &waitingWindow{wake: make(chan struct{}, 1)}
		u.window = window
		u.startGalleryRequest(func(context.Context) galleryResult {
			return galleryResult{pictures: []gallery.Picture{{ID: "boot"}}}
		})
		synctest.Wait()
		select {
		case <-window.wake:
		default:
			t.Fatal("Flash 列表入队后应唤醒窗口")
		}

		// 切换来源时，Flash 的结果已入队但尚未被窗口取走。
		u.startGalleryRequest(func(context.Context) galleryResult {
			return galleryResult{pictures: []gallery.Picture{{ID: "1.bin"}}}
		})
		u.gallery.applyResult(<-u.gallery.results)
		if len(u.gallery.pictures) != 0 {
			t.Fatal("切换到 SD 卡后不应显示旧的 Flash 列表")
		}
		synctest.Wait()
		select {
		case <-window.wake:
		default:
			t.Fatal("SD 卡列表入队后应再次唤醒窗口")
		}
		u.gallery.applyResult(<-u.gallery.results)
		if got := u.gallery.pictures; len(got) != 1 || got[0].ID != "1.bin" {
			t.Fatalf("应显示当前来源的 SD 卡列表，实际为 %v", got)
		}
	})
}

func TestGalleryPageAppendKeepsPreviewAndExistingButton(t *testing.T) {
	u := newTestUI()
	g := &u.gallery
	first, cancelFirst := context.WithCancel(context.Background())
	g.request, g.cancel = first, cancelFirst
	g.applyResult(galleryResult{request: first, pictures: []gallery.Picture{{ID: "1.bin"}}, next: "1.bin"})
	firstButton := g.buttons[0]
	g.selected = "1.bin"
	preview := paint.NewImageOp(image.NewNRGBA(image.Rect(0, 0, 3, 4)))
	g.preview, g.thumbnails["1.bin"] = preview, preview

	second, cancelSecond := context.WithCancel(context.Background())
	g.request, g.cancel, g.kind = second, cancelSecond, galleryPage
	g.applyResult(galleryResult{request: first, pictures: []gallery.Picture{{ID: "old.bin"}}})
	g.applyResult(galleryResult{request: second, pictures: []gallery.Picture{{ID: "1.bin"}, {ID: "2.bin"}}})
	if len(g.pictures) != 2 || g.pictures[1].ID != "2.bin" || g.nextPage != "" {
		t.Fatalf("追加应跳过重复和过期条目：%v", g.pictures)
	}
	if g.buttons[0] != firstButton || g.selected != "1.bin" || g.preview != preview || g.thumbnails["1.bin"] != preview {
		t.Fatal("追加下一页应保留已有的点击器、预览和缩略图")
	}
}

func TestGalleryRetainsThumbnailsOnlyAroundViewport(t *testing.T) {
	u := newTestUI()
	g := &u.gallery
	g.pictureIndex = make(map[string]int)
	g.thumbnails = make(map[string]paint.ImageOp)
	for _, id := range []string{"1.bin", "2.bin", "3.bin", "4.bin", "5.bin", "6.bin", "7.bin"} {
		g.pictureIndex[id] = len(g.pictures)
		g.pictures = append(g.pictures, gallery.Picture{ID: id})
		g.thumbnails[id] = paint.ImageOp{}
	}
	g.list.Position = layout.Position{First: 3, Count: 1}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.request, g.cancel = request, cancel
	u.loadVisibleGalleryRows()
	if len(g.thumbnails) != 5 {
		t.Fatalf("视口一行及前后两行应保留五张缩略图：%v", g.thumbnails)
	}
	for _, id := range []string{"1.bin", "7.bin"} {
		if _, exists := g.thumbnails[id]; exists {
			t.Fatalf("应清除远离视口的缩略图 %s", id)
		}
	}
}

func TestPreviewAndFileResultsStayIndependent(t *testing.T) {
	u := newTestUI()
	u.fileBusy, u.rendering = true, true
	u.status = "选择要打开的图片"
	preview := image.NewNRGBA(image.Rect(0, 0, 12, 16))
	u.done <- ditherResult{img: preview}
	if !u.applyPendingResults() || u.rendering || u.preview != preview {
		t.Fatal("打开文件时也应显示已经算好的预览")
	}
	if !u.fileBusy || u.status != "选择要打开的图片" {
		t.Fatal("预览完成不能结束文件操作或清掉选择提示")
	}

	// Gio 可能把读图与预览的唤醒合并为一帧，两项结果都必须在这一轮处理。
	u.rendering = true
	u.openResult <- openResult{}
	u.done <- ditherResult{img: preview}
	if !u.applyPendingResults() || u.fileBusy || u.rendering {
		t.Fatal("一次唤醒应同时处理取消选择和预览完成")
	}
}
