// Package panel 把图片变成 1200×1600 的六色面板图。
// 命令行和窗口共用这里的取景、采样和抖动，不依赖界面库。
package panel

import (
	"encoding/binary"
	"image"
	"image/color"
)

// 面板是 7.09 寸 Spectra 6，两颗 IC 各管 600 列，合计 1200×1600。
// 抖动必须在这个分辨率上做完再缩小显示，否则预览的颗粒和上屏的不一样。
const (
	Width  = 1200
	Height = 1600
)

// Ink 是一种墨水。索引与固件帧缓冲的半字节一致：
// 偶数列占高半字节，奇数列占低半字节，所以索引写两遍。
// Preview 只是这种墨水在屏幕上的 RGB 近似，不是面板的真实反射。
type Ink struct {
	Name    string
	Index   byte
	Preview color.NRGBA
}

var inks = []Ink{
	{"黑", 0x00, color.NRGBA{20, 20, 20, 255}},
	{"白", 0x11, color.NRGBA{245, 245, 240, 255}},
	{"黄", 0x22, color.NRGBA{240, 200, 30, 255}},
	{"红", 0x33, color.NRGBA{190, 30, 40, 255}},
	{"蓝", 0x55, color.NRGBA{25, 70, 170, 255}},
	{"绿", 0x66, color.NRGBA{30, 140, 60, 255}},
}

// Inks 返回面板的六种墨水，顺序与固件帧缓冲一致。
func Inks() []Ink { return inks }

// FrameBytes 是 EPD1 原始帧的长度，也是编码帧的最大长度；EPD2 的实际长度可更小。
const FrameBytes = 12 + Width*Height/2

// Encode 把已抖动的 1200×1600 图编成无损帧，EPD2 不更小时回退 EPD1。
//
// 每个像素按预览色取最近的墨水，偶数列放高半字节，奇数列放低半字节。
// 标准 zlib 只压缩行优先像素，解压后与原始 4bpp 逐字节一致；错误尺寸返回 nil。
func Encode(src *image.NRGBA) []byte {
	if src == nil || src.Bounds().Dx() != Width || src.Bounds().Dy() != Height {
		return nil
	}
	frame := make([]byte, FrameBytes)
	copy(frame, "EPD1")
	binary.LittleEndian.PutUint16(frame[8:], Width)
	binary.LittleEndian.PutUint16(frame[10:], Height)
	matcher := (ColorOptions{}).matcher()
	origin := src.Bounds().Min
	for y := range Height {
		for x := 0; x < Width; x += 2 {
			i := src.PixOffset(origin.X+x, origin.Y+y)
			hi := inks[matcher.nearest(src.Pix[i], src.Pix[i+1], src.Pix[i+2])].Index >> 4
			j := src.PixOffset(origin.X+x+1, origin.Y+y)
			lo := inks[matcher.nearest(src.Pix[j], src.Pix[j+1], src.Pix[j+2])].Index & 0x0f
			frame[12+y*Width/2+x/2] = hi<<4 | lo
		}
	}
	return compressFrame(frame)
}
