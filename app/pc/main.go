// Package main 是 LumenFolio 的 Windows 桌面端。
//
// 不带参数或传入一张 PNG/JPEG 时打开界面。ota 上传固件，frame 把一张图发到相框，export 把图编成帧写到本地。
// Gio 的事件循环只在打开界面时从主 goroutine 启动，命令行路径不进入它。
package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"lumenfolio/internal/cli"
	"lumenfolio/internal/desktop"
)

// launchGUI 判断这组参数应打开界面还是进入命令行。
//
// 没有参数时打开空白界面。恰好一个 .png、.jpg 或 .jpeg 路径时打开并载入该图。
// ota、frame、export、选项和其他参数都留给命令行，避免未知命令误开窗口。
func launchGUI(args []string) (bool, string) {
	if len(args) == 0 {
		return true, ""
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || args[0] == "ota" || args[0] == "frame" || args[0] == "export" {
		return false, ""
	}
	switch strings.ToLower(filepath.Ext(args[0])) {
	case ".png", ".jpg", ".jpeg":
		return true, args[0]
	default:
		return false, ""
	}
}

func main() {
	args := os.Args[1:]
	if open, image := launchGUI(args); open {
		os.Exit(desktop.Run(image))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, args, os.Stdout, os.Stderr))
}
