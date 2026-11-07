package panel

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"math/rand"
	"testing"
)

func decodedPixels(t *testing.T, frame []byte) []byte {
	t.Helper()
	if err := ValidateFrame(frame); err != nil {
		t.Fatal(err)
	}
	if string(frame[:4]) == "EPD1" {
		return frame[12:]
	}
	reader, err := zlib.NewReader(bytes.NewReader(frame[16:]))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	pixels, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) != Width*Height/2 {
		t.Fatalf("解压后应为一整帧，实际 %d 字节", len(pixels))
	}
	return pixels
}

func TestEncodePacksInksAndRejectsWrongSize(t *testing.T) {
	if Encode(nil) != nil || Encode(image.NewNRGBA(image.Rect(0, 0, 2, 2))) != nil {
		t.Fatal("尺寸不对的图不应编码")
	}
	src := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	for _, ink := range Inks() {
		for y := range Height {
			for x := range Width {
				src.SetNRGBA(x, y, ink.Preview)
			}
		}
		frame := Encode(src)
		if len(frame) >= FrameBytes || string(frame[:4]) != "EPD2" {
			t.Fatalf("%s 的纯色帧应压缩，实际 %d 字节", ink.Name, len(frame))
		}
		for _, b := range decodedPixels(t, frame) {
			if b != ink.Index {
				t.Fatalf("%s 的像素应为 %02x，出现了 %02x", ink.Name, ink.Index, b)
			}
		}
	}
	// 相邻两列必须落在同一字节，压缩不能改变高低半字节顺序。
	src.SetNRGBA(0, 0, color.NRGBA{20, 20, 20, 255})
	src.SetNRGBA(1, 0, color.NRGBA{245, 245, 240, 255})
	if got := decodedPixels(t, Encode(src))[0]; got != 0x01 {
		t.Fatalf("黑白相邻应为 0x01，实际 %02x", got)
	}
}

func TestEncodeHandlesNonZeroImageOrigin(t *testing.T) {
	bounds := image.Rect(7, 11, 7+Width, 11+Height)
	src := image.NewNRGBA(bounds)
	src.SetNRGBA(7, 11, inks[1].Preview)
	src.SetNRGBA(8, 11, inks[4].Preview)
	src.SetNRGBA(bounds.Max.X-1, bounds.Max.Y-1, inks[5].Preview)
	pixels := decodedPixels(t, Encode(src))
	if pixels[0] != 0x15 || pixels[len(pixels)-1] != 0x06 {
		t.Fatalf("源图坐标原点不应改变像素位置：首字节 %02x、末字节 %02x", pixels[0], pixels[len(pixels)-1])
	}
}

func rawTestFrame() []byte {
	raw := make([]byte, FrameBytes)
	copy(raw, "EPD1")
	binary.LittleEndian.PutUint16(raw[8:], Width)
	binary.LittleEndian.PutUint16(raw[10:], Height)
	return raw
}

func TestCompressionRoundTripAndRawFallback(t *testing.T) {
	raw := rawTestFrame()
	rng := rand.New(rand.NewSource(7))
	for i := 12; i < len(raw); i++ {
		hi, lo := inks[rng.Intn(len(inks))].Index, inks[rng.Intn(len(inks))].Index
		raw[i] = hi&0xf0 | lo&0x0f
	}
	encoded := compressFrame(raw)
	if string(encoded[:4]) != "EPD2" || !bytes.Equal(decodedPixels(t, encoded), raw[12:]) {
		t.Fatal("混合六色帧没有无损还原")
	}
	// 任意高熵字节仅用于验证回退分支，不作为面板色码发送。
	if _, err := rng.Read(raw[12:]); err != nil {
		t.Fatal(err)
	}
	if got := compressFrame(raw); !bytes.Equal(got, raw) {
		t.Fatal("压缩不划算时必须原样保留 EPD1")
	}
}

func TestValidateFrameRejectsMalformedHeaders(t *testing.T) {
	compressed := compressFrame(rawTestFrame())
	for name, frame := range map[string][]byte{"raw": rawTestFrame(), "zlib": compressed} {
		if err := ValidateFrame(frame); err != nil {
			t.Fatalf("合法 %s 被拒绝：%v", name, err)
		}
	}
	bad := func(change func([]byte)) []byte {
		frame := bytes.Clone(compressed)
		change(frame)
		return frame
	}
	cases := map[string][]byte{
		"empty":             nil,
		"short":             []byte("EPD2"),
		"short zlib header": compressed[:15],
		"truncated raw":     rawTestFrame()[:FrameBytes-1],
		"truncated zlib":    compressed[:len(compressed)-1],
		"trailing zlib":     append(bytes.Clone(compressed), 0),
		"oversized":         make([]byte, FrameBytes+1),
		"magic":             bad(func(f []byte) { copy(f, "NOPE") }),
		"position":          bad(func(f []byte) { f[4] = 1 }),
		"width":             bad(func(f []byte) { f[8] = 0 }),
		"height":            bad(func(f []byte) { f[10] = 0 }),
		"zero payload":      bad(func(f []byte) { binary.LittleEndian.PutUint32(f[12:], 0) }),
		"overflow payload":  bad(func(f []byte) { binary.LittleEndian.PutUint32(f[12:], ^uint32(0)) }),
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			if ValidateFrame(frame) == nil {
				t.Fatal("非法帧不应通过结构验证")
			}
		})
	}
}
