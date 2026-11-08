// Package mobilepanel 为 Android 暴露取景、抖动与帧编码的 gomobile 接口。
package mobilepanel

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"

	"lumenfolio/panel"
)

var algorithms = panel.Algorithms()

// Width 是面板的像素宽度。
func Width() int32 { return panel.Width }

// Height 是面板的像素高度。
func Height() int32 { return panel.Height }

// AlgorithmCount 返回桌面端预设数量。
func AlgorithmCount() int32 { return int32(len(algorithms)) }

// AlgorithmName 返回桌面端同一序号的预设名称，index 须在 AlgorithmCount 范围内。
func AlgorithmName(index int32) string { return algorithms[index].Name() }

// InkCount 返回面板墨水数量。
func InkCount() int32 { return int32(len(panel.Inks())) }

// InkARGB 返回 Android 使用的 0xAARRGGBB 近似色，index 须在 InkCount 范围内。
func InkARGB(index int32) int32 {
	c := panel.Inks()[index].Preview
	return int32(uint32(c.A)<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B))
}

// InkName 返回墨水名称，index 须在 InkCount 范围内。
func InkName(index int32) string { return panel.Inks()[index].Name }

// Picture 持有解码后的原图，可与不同的取景快照一起交给后台处理。
type Picture struct {
	source image.Image
}

// OpenPicture 按桌面端的解码规则读取 PNG 或 JPEG 原图。
func OpenPicture(data []byte) (*Picture, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("没能读图：%w", err)
	}
	return &Picture{source: src}, nil
}

// Width 是原图的像素宽度。
func (p *Picture) Width() int32 { return int32(p.source.Bounds().Dx()) }

// Height 是原图的像素高度。
func (p *Picture) Height() int32 { return int32(p.source.Bounds().Dy()) }

// InitialCrop 返回原图完整居中的初始取景。
func (p *Picture) InitialCrop() *Crop {
	return &Crop{value: panel.NewCrop(p.source.Bounds())}
}

// Crop 是取景状态的不可变快照，坐标以面板像素为单位。
type Crop struct {
	value panel.Crop
}

// OffsetX 是原图中心相对面板中心的水平偏移量。
func (c *Crop) OffsetX() float64 { return c.value.OX }

// OffsetY 是原图中心相对面板中心的垂直偏移量。
func (c *Crop) OffsetY() float64 { return c.value.OY }

// Scale 是面板像素与原图像素的比例。
func (c *Crop) Scale() float64 { return c.value.Scale }

// Rotation 返回面板坐标系下的角度，单位是度。
func (c *Crop) Rotation() float64 { return c.value.Angle * 180 / math.Pi }

// Transform 返回一次手势后的取景，依次按触点缩放、绕面板中心旋转和平移。
func (c *Crop) Transform(dx, dy, factor, anchorX, anchorY, degrees float64) *Crop {
	next := c.value
	next.Zoom(factor, anchorX, anchorY)
	next.RotateAroundFrameCenter(degrees)
	next.Move(dx, dy)
	return &Crop{value: next}
}

// Rotate 返回绕面板中心旋转指定角度后的取景。
func (c *Crop) Rotate(degrees float64) *Crop {
	next := c.value
	next.RotateAroundFrameCenter(degrees)
	return &Crop{value: next}
}

// Render 按同一取景和预设生成整幅六色图，供预览、上传和导出共用。
func (p *Picture) Render(crop *Crop, algorithm int32) (*Rendered, error) {
	if crop == nil {
		return nil, errors.New("缺少取景")
	}
	if algorithm < 0 || int(algorithm) >= len(algorithms) {
		return nil, fmt.Errorf("没有这个抖动算法：%d", algorithm)
	}
	return &Rendered{pixels: algorithms[algorithm].Dither(crop.value.Sample(p.source))}, nil
}

// Rendered 持有不可变的面板像素，预览和帧编码使用同一份结果。
type Rendered struct {
	pixels *image.NRGBA
}

// Preview 返回可由 Android 解码的全分辨率 PNG。
func (r *Rendered) Preview() ([]byte, error) {
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, r.pixels); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// Frame 按桌面端的 EPD1／EPD2 规则编码当前六色图。
func (r *Rendered) Frame() ([]byte, error) {
	frame := panel.Encode(r.pixels)
	if frame == nil {
		return nil, errors.New("构图没有编成帧")
	}
	return frame, nil
}
