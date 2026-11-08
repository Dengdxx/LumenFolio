package panel

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
)

const zlibHeaderBytes = 16

// compressFrame 只压缩行优先像素，不改色码；整个 EPD2 不更小时保留原始 EPD1。
func compressFrame(raw []byte) []byte {
	var payload bytes.Buffer
	writer := zlib.NewWriter(&payload)
	_, writeErr := writer.Write(raw[12:])
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil || zlibHeaderBytes+payload.Len() >= len(raw) {
		return raw
	}
	frame := make([]byte, zlibHeaderBytes+payload.Len())
	copy(frame, raw[:12])
	copy(frame, "EPD2")
	binary.LittleEndian.PutUint32(frame[12:], uint32(payload.Len()))
	copy(frame[zlibHeaderBytes:], payload.Bytes())
	return frame
}

// ValidateFrame 验证 EPD1／EPD2 的帧头、整幅尺寸、编码上限和实际总长度。
//
// 这里只验证结构，不解压像素；压缩流和 Adler32 由设备显示任务校验。
func ValidateFrame(frame []byte) error {
	if len(frame) < 12 || len(frame) > FrameBytes {
		return fmt.Errorf("帧长度不合法：%d 字节", len(frame))
	}
	if binary.LittleEndian.Uint16(frame[4:]) != 0 || binary.LittleEndian.Uint16(frame[6:]) != 0 ||
		binary.LittleEndian.Uint16(frame[8:]) != Width || binary.LittleEndian.Uint16(frame[10:]) != Height {
		return fmt.Errorf("帧必须是整幅 %d×%d", Width, Height)
	}
	switch string(frame[:4]) {
	case "EPD1":
		if len(frame) != FrameBytes {
			return fmt.Errorf("EPD1 应为 %d 字节，实际 %d", FrameBytes, len(frame))
		}
	case "EPD2":
		if len(frame) <= zlibHeaderBytes || binary.LittleEndian.Uint32(frame[12:]) != uint32(len(frame)-zlibHeaderBytes) {
			return fmt.Errorf("EPD2 压缩长度与实际载荷不符")
		}
	default:
		return fmt.Errorf("不是 EPD1／EPD2 帧")
	}
	return nil
}

// DecodeFrame 还原整幅 EPD1／EPD2 帧，以面板近似色生成只读预览，不重新抖动。
// 压缩帧必须完整解压出一帧像素，且通过 zlib 校验；非六色墨水码视为损坏。
func DecodeFrame(frame []byte) (*image.NRGBA, error) {
	if err := ValidateFrame(frame); err != nil {
		return nil, err
	}
	pixels := frame[12:]
	if string(frame[:4]) == "EPD2" {
		compressed := bytes.NewReader(frame[zlibHeaderBytes:])
		reader, err := zlib.NewReader(compressed)
		if err != nil {
			return nil, fmt.Errorf("打开 EPD2 压缩流：%w", err)
		}
		defer reader.Close()
		const pixelBytes = Width * Height / 2
		pixels, err = io.ReadAll(io.LimitReader(reader, pixelBytes+1))
		if err != nil {
			return nil, fmt.Errorf("解压 EPD2：%w", err)
		}
		if len(pixels) != pixelBytes || compressed.Len() != 0 {
			return nil, fmt.Errorf("EPD2 的像素长度或压缩流边界不符")
		}
	}

	var palette [16]color.NRGBA
	for _, ink := range inks {
		palette[ink.Index&0xf] = ink.Preview
	}
	preview := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	for i, packed := range pixels {
		hi, lo := palette[packed>>4], palette[packed&0xf]
		if hi.A == 0 || lo.A == 0 {
			return nil, fmt.Errorf("帧包含无效墨水码：0x%02x", packed)
		}
		copy(preview.Pix[i*8:], []byte{hi.R, hi.G, hi.B, hi.A, lo.R, lo.G, lo.B, lo.A})
	}
	return preview, nil
}
