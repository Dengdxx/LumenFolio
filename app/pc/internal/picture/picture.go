// Package picture 提供桌面界面和命令行共用的画面编码及传输。
package picture

import (
	"context"
	"fmt"
	"image"

	"lumenfolio/frame"
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

// Send 把当前构图编成一帧并发到设备。host 为空时返回错误。
func Send(ctx context.Context, host string, port, alg int, src image.Image, crop panel.Crop) (string, error) {
	if host == "" {
		return "", fmt.Errorf("缺少设备地址")
	}
	encoded, err := Encode(alg, src, crop)
	if err != nil {
		return "", err
	}
	return frame.Send(ctx, host, port, encoded)
}
