package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"

	"lumenfolio/frame"
	"lumenfolio/panel"
)

// encodePicture 按指定抖动算法把当前构图编成一帧无损编码。
//
// 上传和离线导出都用这一帧。alg 是 panel.Algorithms 的序号。
func encodePicture(alg int, src image.Image, crop panel.Crop) ([]byte, error) {
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

// sendPicture 把当前构图编成一帧并发到设备。
//
// 界面和命令行都走这里：采样和编码不依赖窗口，发送也不解析命令行。
// host 为空时返回错误。
func sendPicture(ctx context.Context, host string, port, alg int, src image.Image, crop panel.Crop) (string, error) {
	if host == "" {
		return "", fmt.Errorf("缺少设备地址")
	}
	encoded, err := encodePicture(alg, src, crop)
	if err != nil {
		return "", err
	}
	return frame.Send(ctx, host, port, encoded)
}

// exportPicture 把当前构图编成无损帧，返回实际写入字节数。文件已存在时覆盖。
func exportPicture(path string, alg int, src image.Image, crop panel.Crop) (int, error) {
	encoded, err := encodePicture(alg, src, crop)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return 0, err
	}
	return len(encoded), nil
}

// runFrame 执行 frame 子命令：把一张图按当前取景规则抖动后发到设备。
//
// 位置参数是图片路径。--http-port 默认 8080，--alg 是抖动算法序号，默认 Floyd-Steinberg。
// 取景用 panel.NewCrop 的初始构图：整图等比放进面板、居中、不旋转。
func runFrame(ctx context.Context, host string, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("frame", flag.ContinueOnError)
	flags.SetOutput(errOut)
	port := flags.Int("http-port", 8080, "设备 HTTP 端口")
	alg := flags.Int("alg", 1, "抖动算法序号，与界面列表一致，0 起")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(errOut, "用法：LumenFolio --host IP frame [选项] 图片.png|jpg|jpeg")
		return 2
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer file.Close()
	src, _, err := image.Decode(file)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	reply, err := sendPicture(ctx, host, *port, *alg, src, panel.NewCrop(src.Bounds()))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, reply)
	return 0
}

// runExport 执行 export 子命令：把一张图编成无损帧写到本地，不连接设备。
//
// 位置参数是图片路径和输出 .bin 路径。--alg 与 frame 子命令相同，默认 Floyd-Steinberg。
// 取景用 panel.NewCrop 的初始构图：整图等比放进面板、居中、不旋转。
func runExport(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(errOut)
	alg := flags.Int("alg", 1, "抖动算法序号，与界面列表一致，0 起")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(errOut, "用法：LumenFolio export [选项] 图片.png|jpg|jpeg 输出.bin")
		return 2
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer file.Close()
	src, _, err := image.Decode(file)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	dst := flags.Arg(1)
	size, err := exportPicture(dst, *alg, src, panel.NewCrop(src.Bounds()))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "已写出 %s（%d 字节）\n", dst, size)
	return 0
}
