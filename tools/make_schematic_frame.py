#!/usr/bin/env python3
"""把原理图量化成 1200×1600 六色帧。

输出 .cache/schematic_frame.bin，不覆盖开机画面 main/boot_frame.bin。
帧格式：
    magic "EPD2" + uint16 x, y, w, h（小端，固定为 0,0,1200,1600）
    uint32 压缩载荷字节数（小端）+ 标准 zlib 流，含 Adler32 校验和
    压缩后不更小时回退 "EPD1" + 同样的 x,y,w,h + 原始像素。
    解压后仍是 h 行，每行 w/2 字节：
        前 300 字节 = 左半幅 IC0（x 0..599）
        后 300 字节 = 右半幅 IC1（x 600..1199）
        偶数 x 在高半字节，与 epd_fb_put_pixel() 一致

色码（低 4 位）：0 黑 / 1 白 / 2 黄 / 3 红 / 5 蓝 / 6 绿。
"""

from __future__ import annotations

import struct
import sys
import zlib
from pathlib import Path

from PIL import Image

WIDTH = 1200
HEIGHT = 1600
IC_WIDTH = 600
ROW_BYTES = WIDTH // 2
PLANE_BYTES = IC_WIDTH // 2

# 低半字节。与 components/epd709/include/epd709.h 一致。
BLACK, WHITE, YELLOW, RED, BLUE, GREEN = 0x0, 0x1, 0x2, 0x3, 0x5, 0x6

PREVIEW_RGB = {
    BLACK: (0, 0, 0),
    WHITE: (255, 255, 255),
    YELLOW: (255, 214, 0),
    RED: (196, 0, 0),
    BLUE: (0, 70, 210),
    GREEN: (0, 150, 55),
}


def classify(r: int, g: int, b: int) -> int:
    """线稿向六色收。低饱和度的灰网格归白，只留下真正的深色线和色块。"""
    hi = r if r > g else g
    hi = hi if hi > b else b
    lo = r if r < g else g
    lo = lo if lo < b else b
    if hi - lo < 42:
        lum = (r * 299 + g * 587 + b * 114) // 1000
        return BLACK if lum < 128 else WHITE
    if r > 140 and g > 130 and b < 130 and r + g > b * 3:
        return YELLOW
    if r >= g and r >= b:
        return RED
    if g >= r and g >= b:
        return GREEN
    return BLUE


def quantize(im: Image.Image) -> list[list[int]]:
    """顺时针转 90° 后铺满 1200×1600。块里碰到黑线或色线就留下，避免细线被混成灰。"""
    src = im.load()
    sw, sh = im.size

    rows: list[list[int]] = []
    for y in range(HEIGHT):
        # 旋转后屏幕第 y 行对应源图的列 x_src = y。
        x0 = (y * sw) // HEIGHT
        x1 = max(x0 + 1, ((y + 1) * sw) // HEIGHT)
        row: list[int] = []
        for x in range(WIDTH):
            # 屏幕第 x 列对应源图从下往上的行。
            y1 = sh - (x * sh) // WIDTH
            y0 = sh - ((x + 1) * sh) // WIDTH
            if y0 >= y1:
                y0 = y1 - 1
            black = 0
            color_hits = {YELLOW: 0, RED: 0, BLUE: 0, GREEN: 0}
            for yy in range(y0, y1):
                for xx in range(x0, x1):
                    c = classify(*src[xx, yy][:3])
                    if c == BLACK:
                        black += 1
                    elif c != WHITE:
                        color_hits[c] += 1
            best = max(color_hits, key=color_hits.get)
            if color_hits[best] > black and color_hits[best] > 0:
                row.append(best)
            elif black > 0:
                row.append(BLACK)
            else:
                row.append(WHITE)
        rows.append(row)
    return rows


def pack(rows: list[list[int]]) -> bytes:
    """保留 4bpp 色码，优先输出更小的 EPD2；压缩不划算时回退 EPD1。"""
    if len(rows) != HEIGHT or any(len(row) != WIDTH for row in rows):
        raise SystemExit(f"帧尺寸必须为 {WIDTH}x{HEIGHT}")
    payload = bytearray()
    for row in rows:
        packed = bytearray(ROW_BYTES)
        for col, nib in enumerate(row):
            i = col >> 1
            if (col & 1) == 0:
                packed[i] = (nib & 0x0F) << 4
            else:
                packed[i] |= nib & 0x0F
        payload += packed
    dimensions = struct.pack("<HHHH", 0, 0, WIDTH, HEIGHT)
    compressed = zlib.compress(payload, level=6)
    if 16 + len(compressed) < 12 + len(payload):
        return b"EPD2" + dimensions + struct.pack("<I", len(compressed)) + compressed
    return b"EPD1" + dimensions + payload


def write_preview(rows: list[list[int]], path: Path) -> None:
    canvas = Image.new("RGB", (WIDTH, HEIGHT), PREVIEW_RGB[WHITE])
    px = canvas.load()
    for row_i, row in enumerate(rows):
        for col, nib in enumerate(row):
            px[col, row_i] = PREVIEW_RGB[nib]
    path.parent.mkdir(parents=True, exist_ok=True)
    canvas.save(path)


def main() -> None:
    repo = Path(__file__).resolve().parents[1]
    src_path = repo / "原理图.png"
    out_path = repo / ".cache" / "schematic_frame.bin"
    preview_path = repo / ".cache" / "schematic_preview.png"

    raw = Image.open(src_path).convert("RGBA")
    flat = Image.alpha_composite(Image.new("RGBA", raw.size, (255, 255, 255, 255)), raw).convert("RGB")
    rows = quantize(flat)
    blob = pack(rows)
    out_path.write_bytes(blob)
    write_preview(rows, preview_path)

    counts = {name: 0 for name in ("K", "W", "Y", "R", "B", "G")}
    names = {BLACK: "K", WHITE: "W", YELLOW: "Y", RED: "R", BLUE: "B", GREEN: "G"}
    for row in rows:
        for nib in row:
            counts[names[nib]] += 1
    print(f"source {flat.size} -> rotated full frame {WIDTH}x{HEIGHT}")
    print(f"bin {out_path} {len(blob)} bytes")
    print("pixels", counts)
    print(f"preview {preview_path}")


if __name__ == "__main__":
    sys.exit(main())
