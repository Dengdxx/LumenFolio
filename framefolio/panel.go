package main

import "image/color"

// 面板是 7.09 寸 Spectra 6，两颗 IC 各管 600 列，合计 1200×1600。
// 抖动必须在这个分辨率上做完再缩小显示，否则预览的颗粒和上屏的不一样。
const (
	panelWidth  = 1200
	panelHeight = 1600
)

// ink 是一种墨水。索引与固件帧缓冲的半字节一致：
// 偶数列占高半字节，奇数列占低半字节，所以索引写两遍。
// preview 只是这种墨水在屏幕上的 RGB 近似，不是面板的真实反射。
type ink struct {
	name    string
	index   byte
	preview color.NRGBA
}

var inks = []ink{
	{"黑", 0x00, color.NRGBA{20, 20, 20, 255}},
	{"白", 0x11, color.NRGBA{245, 245, 240, 255}},
	{"黄", 0x22, color.NRGBA{240, 200, 30, 255}},
	{"红", 0x33, color.NRGBA{190, 30, 40, 255}},
	{"蓝", 0x55, color.NRGBA{25, 70, 170, 255}},
	{"绿", 0x66, color.NRGBA{30, 140, 60, 255}},
}
