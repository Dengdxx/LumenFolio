// Package main 是 FrameFolio，Windows 上的相框地面站。
//
// 不带参数或传入一张 PNG/JPEG 时打开界面；ota 子命令走命令行上传固件。
// Gio 的事件循环只在打开界面时从主 goroutine 启动，命令行路径不进入它。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"framefolio/ota"
)

// launchGUI 判断这组参数应打开界面还是进入命令行。
//
// 没有参数时打开空白界面。恰好一个 .png、.jpg 或 .jpeg 路径时打开并载入该图。
// ota、选项和其他参数都留给命令行，避免未知命令误开窗口。
func launchGUI(args []string) (bool, string) {
	if len(args) == 0 {
		return true, ""
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || args[0] == "ota" {
		return false, ""
	}
	switch strings.ToLower(filepath.Ext(args[0])) {
	case ".png", ".jpg", ".jpeg":
		return true, args[0]
	default:
		return false, ""
	}
}

// run 解析命令行并返回进程退出码。
//
// 退出码：0 成功（含 -h），1 运行期错误（网络、设备或文件），2 参数或用法错误。
// 界面入口不走这里。ctx 用于取消上传与等待；out 输出正常结果，errOut 输出用法与错误。
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	global := flag.NewFlagSet("FrameFolio", flag.ContinueOnError)
	global.SetOutput(errOut)
	host := global.String("host", "", "设备 IP，取设备串口日志里打印的地址")
	global.Usage = func() {
		fmt.Fprintln(errOut, "FrameFolio\n用法：FrameFolio [图片.png|jpg|jpeg] | [全局选项] ota [命令选项] 应用.bin|status\n不带参数时打开界面。")
		global.PrintDefaults()
	}
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if global.NArg() == 0 {
		global.Usage()
		return 2
	}
	if global.Arg(0) != "ota" {
		fmt.Fprintf(errOut, "未知命令：%s\n", global.Arg(0))
		global.Usage()
		return 2
	}
	if *host == "" {
		fmt.Fprintln(errOut, "缺少 --host：设备地址由热点动态分配，填串口日志里打印的 IP")
		return 2
	}
	return ota.Run(ctx, *host, global.Args()[1:], out, errOut)
}

func main() {
	args := os.Args[1:]
	if open, image := launchGUI(args); open {
		os.Exit(runGUI(image))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, args, os.Stdout, os.Stderr))
}
