// Package picture 提供桌面界面和命令行共用的画面编码及传输。
package picture

import (
	"context"
	"fmt"
	"image"

	"lumenfolio/frame"
	"lumenfolio/frameclient"
	"lumenfolio/panel"
)

// Encode 按指定抖动算法把当前构图编成一帧无损编码。
//
// 上传和离线导出都用这一帧。alg 是 panel.Algorithms 的序号。
func Encode(alg int, src image.Image, crop panel.Crop) ([]byte, error) {
	algs := panel.Algorithms()
	if alg < 0 || alg >= len(algs) {
		return nil, fmt.Errorf("没有这个抖动算法：%d", alg)
	}
	if src == nil {
		return nil, fmt.Errorf("没有图片")
	}
	encoded := panel.Encode(algs[alg].Dither(crop.Sample(src)))
	if encoded == nil {
		return nil, fmt.Errorf("构图没有编成帧")
	}
	return encoded, nil
}

// Send 把当前构图编成一帧，经已打开的相框会话发到 Flash 上传槽。
//
// 会话由调用方打开并在用完后关闭；命令行传入 frameclient.WiFi 建立的会话。
func Send(ctx context.Context, session *frameclient.Frame, alg int, src image.Image, crop panel.Crop) (string, error) {
	if session == nil {
		return "", fmt.Errorf("缺少相框会话")
	}
	encoded, err := Encode(alg, src, crop)
	if err != nil {
		return "", err
	}
	return session.Send(ctx, encoded, frame.Flash, "", nil)
}

// SendTo 编码当前构图，保存到选定存储并等待相框完成 SPI 交付。
func SendTo(ctx context.Context, host string, port, alg int, src image.Image, crop panel.Crop, target frame.Target, name string, report func(frame.Progress)) (string, error) {
	if host == "" {
		return "", fmt.Errorf("缺少设备地址")
	}
	encoded, err := Encode(alg, src, crop)
	if err != nil {
		return "", err
	}
	return frame.SendTo(ctx, host, port, encoded, target, name, report)
}
