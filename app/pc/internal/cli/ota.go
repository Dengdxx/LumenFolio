package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"lumenfolio/frameclient"
	"lumenfolio/ota"
)

// 与 ota 包内部的默认上传与等待时限一致。ota 未导出这两个常量，
// 命令行在这里单独声明，保证用法文本里的缺省值与实际行为相符。
const (
	defaultUploadTimeout = 3 * time.Minute
	defaultWaitTimeout   = 90 * time.Second
)

// runOTA 执行 ota 子命令：status 打印状态 JSON，否则上传镜像并等待确认。
//
// 位置参数为应用 .bin 路径或 status；--http-port 默认 8080，--upload-timeout
// 默认 3 分钟，--wait-timeout 默认 90 秒。上传前校验工程、槽容量和设备空闲状态；
// 若目标版本和镜像标识已在运行槽启动但待确认，则跳过上传直接继续确认。
// 上传与确认共用一次 Wi-Fi 会话，命令结束前关闭。
// 返回进程退出码：0 成功，1 运行期错误，2 用法错误。
func runOTA(ctx context.Context, host string, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("ota", flag.ContinueOnError)
	flags.SetOutput(errOut)
	port := flags.Int("http-port", 8080, "设备 OTA HTTP 端口")
	uploadTimeout := flags.Duration("upload-timeout", defaultUploadTimeout, "上传总时限")
	waitTimeout := flags.Duration("wait-timeout", defaultWaitTimeout, "等待重启和自动确认的时限")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || *port < 1 || *port > 65535 || *uploadTimeout <= 0 || *waitTimeout <= 0 {
		fmt.Fprintln(errOut, "用法：LumenFolio --host IP ota [选项] 应用.bin|status")
		return 2
	}
	if flags.Arg(0) == "status" {
		// frameclient 只把状态整理成展示文本，不暴露设备应答原文；
		// 这里仍借用 ota.Run 解析并打印原始 JSON，行为与之前一致。
		return ota.Run(ctx, host, args, out, errOut)
	}
	fail := func(err error) int { fmt.Fprintln(errOut, "OTA:", err); return 1 }
	session, err := frameclient.WiFi(net.JoinHostPort(host, strconv.Itoa(*port)))
	if err != nil {
		return fail(err)
	}
	defer session.Close()
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("OTA 输入必须是普通应用 .bin 文件"))
	}
	if err := session.UpgradeReader(ctx, file, info.Size(), *uploadTimeout, *waitTimeout, func(progress ota.Progress) {
		fmt.Fprintln(out, progress.Message)
	}); err != nil {
		return fail(err)
	}
	return 0
}
