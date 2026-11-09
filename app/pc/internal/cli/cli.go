// Package cli 实现 LumenFolio 的命令行入口。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"lumenfolio/ota"
)

// Run 解析命令行并返回进程退出码。
//
// 退出码：0 成功（含 -h），1 运行期错误（网络、设备或文件），2 参数或用法错误。
// ctx 用于取消上传与等待；out 输出正常结果，errOut 输出用法与错误。
func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	global := flag.NewFlagSet("LumenFolio", flag.ContinueOnError)
	global.SetOutput(errOut)
	host := global.String("host", "", "设备 IP，取设备串口日志里打印的地址")
	global.Usage = func() {
		fmt.Fprintln(errOut, "LumenFolio\n用法：LumenFolio [图片.png|jpg|jpeg]\n      LumenFolio [全局选项] ota [命令选项] 应用.bin|status\n      LumenFolio --host IP frame [命令选项] 图片\n      LumenFolio export [命令选项] 图片 输出.bin\n不带参数时打开界面。")
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
	switch global.Arg(0) {
	case "export":
		return runExport(global.Args()[1:], out, errOut)
	case "ota", "frame":
	default:
		fmt.Fprintf(errOut, "未知命令：%s\n", global.Arg(0))
		global.Usage()
		return 2
	}
	if *host == "" {
		fmt.Fprintln(errOut, "缺少 --host：设备地址由热点动态分配，填串口日志里打印的 IP")
		return 2
	}
	if global.Arg(0) == "frame" {
		return runFrame(ctx, *host, global.Args()[1:], out, errOut)
	}
	return ota.Run(ctx, *host, global.Args()[1:], out, errOut)
}
