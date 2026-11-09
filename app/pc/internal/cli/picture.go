package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"os"
	"strconv"

	"lumenfolio/frameclient"
	"lumenfolio/internal/picture"
	"lumenfolio/panel"
)

// runFrame 执行 frame 子命令：把一张图按当前取景规则抖动后发到设备。
//
// 位置参数是图片路径。--http-port 默认 8080，--alg 是抖动算法序号，默认 Floyd-Steinberg。
// 取景用 panel.NewCrop 的初始构图：整图等比放进面板、居中、不旋转。
// 整次上传只打开一次 Wi-Fi 会话，结束后关闭。
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
	session, err := frameclient.WiFi(net.JoinHostPort(host, strconv.Itoa(*port)))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer session.Close()
	reply, err := picture.Send(ctx, session, *alg, src, panel.NewCrop(src.Bounds()))
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
	encoded, err := picture.Encode(*alg, src, panel.NewCrop(src.Bounds()))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := os.WriteFile(dst, encoded, 0o644); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "已写出 %s（%d 字节）\n", dst, len(encoded))
	return 0
}
