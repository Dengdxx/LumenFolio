package desktop

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"time"

	"gioui.org/x/explorer"

	"lumenfolio/internal/picture"
	"lumenfolio/panel"
)

type openResult struct {
	name string
	img  image.Image
	err  error
}

type ditherResult struct {
	version int
	alg     int
	src     *image.NRGBA
	img     *image.NRGBA
}

// scheduleDither 在拖拽和滚轮操作时做防抖，停顿 120ms 后触发重采样与抖动。
func (u *ui) scheduleDither() {
	if u.ditherTimer != nil {
		u.ditherTimer.Stop()
	}
	u.ditherTimer = time.AfterFunc(120*time.Millisecond, func() {
		select {
		case u.ditherReq <- struct{}{}:
		default:
		}
		if u.window != nil {
			u.window.Invalidate()
		}
	})
}

// triggerDither 立即按当前取景框在后台采样并进行抖动计算。
func (u *ui) triggerDither() {
	if u.rawImage == nil {
		return
	}
	if u.ditherTimer != nil {
		u.ditherTimer.Stop()
	}
	u.cropVersion++
	version := u.cropVersion
	alg := u.alg
	c := u.crop
	img := u.rawImage
	u.preview = nil
	u.rendering = true
	if !u.fileBusy {
		u.status = "正在用「" + panel.Algorithms()[alg].Name() + "」采样并抖动 " + u.name
	}
	go func() {
		src := c.Sample(img)
		res := panel.Algorithms()[alg].Dither(src)
		// 由 UI goroutine 按版本丢弃过期结果；队列满时等待，不能把最新结果直接丢掉。
		u.done <- ditherResult{version: version, alg: alg, src: src, img: res}
		if u.window != nil {
			u.window.Invalidate()
		}
	}()
}

func (u *ui) onOpen() {
	if u.fileBusy {
		return
	}
	u.fileBusy = true
	u.status = "选择要打开的图片"
	go func() {
		file, err := u.explorer.ChooseFile(".png", ".jpg", ".jpeg")
		switch {
		case err == explorer.ErrUserDecline:
			u.postOpenResult(openResult{})
		case err != nil:
			u.postOpenResult(openResult{err: err})
		default:
			u.postOpenResult(u.readImage(file))
		}
	}()
}

// postOpenResult 在结果入队后唤醒窗口，成功、取消和错误都不能依赖下一次鼠标事件才被处理。
// UI 状态仍只由事件循环修改；后台 goroutine 只负责交付结果和请求重绘。
func (u *ui) postOpenResult(result openResult) {
	u.openResult <- result
	if u.window != nil {
		u.window.Invalidate()
	}
}

// readImage 解码选中的文件。文件名取不到时给一个占位名。
func (u *ui) readImage(file io.ReadCloser) openResult {
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return openResult{err: err}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return openResult{err: err}
	}
	name := "未命名图片"
	if named, ok := file.(interface{ Name() string }); ok && named.Name() != "" {
		name = named.Name()
	}
	return openResult{name: name, img: img}
}

func (u *ui) postJob(text string) {
	u.jobResult <- text
	if u.window != nil {
		u.window.Invalidate()
	}
}

func (u *ui) onExport() {
	if u.rawImage == nil {
		u.status = "先打开一张图片"
		return
	}
	if u.explorer == nil || u.fileBusy {
		return
	}
	u.fileBusy = true
	u.status = "选择帧文件的保存位置"
	alg, crop, src := u.alg, u.crop, u.rawImage
	go func() {
		file, err := u.explorer.CreateFile("frame.bin")
		switch {
		case err == explorer.ErrUserDecline:
			u.postJob("已取消导出")
		case err != nil:
			u.postJob("没能保存：" + err.Error())
		default:
			u.postJob(writeExport(file, alg, src, crop))
		}
	}()
}

func writeExport(file io.WriteCloser, alg int, src image.Image, crop panel.Crop) string {
	defer file.Close()
	encoded, err := picture.Encode(alg, src, crop)
	if err != nil {
		return "没能导出：" + err.Error()
	}
	if _, err := file.Write(encoded); err != nil {
		return "没能导出：" + err.Error()
	}
	return fmt.Sprintf("已导出 %d 字节的帧文件", len(encoded))
}
