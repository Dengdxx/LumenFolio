package panel

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
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
