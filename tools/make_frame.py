"""把一张图按比例放进 1200×1600，用半强度 Floyd–Steinberg 抖成 Spectra 6 六色。

用法：python make_frame.py <图片>
输出 main/boot_frame.bin。它不进应用镜像，烧录时写到 artifacts 分区（0x620000）。
帧格式与 make_schematic_frame.py 的 pack() 一致。预览写在 .cache。
"""

import sys
from pathlib import Path

import numpy as np
from PIL import Image

import make_schematic_frame as frame


def fit(src: Image.Image) -> Image.Image:
    scale = min(frame.WIDTH / src.width, frame.HEIGHT / src.height)
    size = (round(src.width * scale), round(src.height * scale))
    resized = src.resize(size, Image.Resampling.BOX)
    canvas = Image.new("RGB", (frame.WIDTH, frame.HEIGHT), (255, 255, 255))
    canvas.paste(resized, ((frame.WIDTH - size[0]) // 2, (frame.HEIGHT - size[1]) // 2))
    return canvas


def quantize(canvas: Image.Image) -> list[list[int]]:
    # Floyd–Steinberg：每个像素取最近的一色，误差按 7/16、3/16、5/16、1/16 扩散到右、左下、下、右下。
    # 蛇形扫描，避免误差只往一个方向堆。中间色靠相邻像素的混色来表现。
    codes = list(frame.PREVIEW_RGB)
    palette = np.array([frame.PREVIEW_RGB[c] for c in codes], dtype=np.float32)
    table = np.array(codes, dtype=np.uint8)
    pixels = np.asarray(canvas, dtype=np.float32)
    height, width = pixels.shape[:2]
    chosen = np.empty((height, width), dtype=np.uint8)

    for y in range(height):
        forward = (y % 2) == 0
        columns = range(width) if forward else range(width - 1, -1, -1)
        for x in columns:
            value = np.clip(pixels[y, x], 0, 255)
            index = int(np.argmin(np.sum((palette - value) ** 2, axis=1)))
            chosen[y, x] = table[index]
            # 只扩散一半误差。全额扩散会把实色打成稀疏的点，上屏后发淡。
            error = (value - palette[index]) * 0.5
            step = 1 if forward else -1
            if 0 <= x + step < width:
                pixels[y, x + step] += error * (7 / 16)
            if y + 1 < height:
                if 0 <= x - step < width:
                    pixels[y + 1, x - step] += error * (3 / 16)
                pixels[y + 1, x] += error * (5 / 16)
                if 0 <= x + step < width:
                    pixels[y + 1, x + step] += error * (1 / 16)
    return [row.tolist() for row in chosen]


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("用法：python make_frame.py <图片>")
    src = Image.open(sys.argv[1]).convert("RGB")
    rows = quantize(fit(src))
    repo = Path(__file__).resolve().parents[1]
    out = repo / "main" / "boot_frame.bin"
    out.write_bytes(frame.pack(rows))
    frame.write_preview(rows, repo / ".cache" / "boot_preview.png")
    names = {frame.BLACK: "K", frame.WHITE: "W", frame.YELLOW: "Y",
             frame.RED: "R", frame.BLUE: "B", frame.GREEN: "G"}
    counts = {name: 0 for name in names.values()}
    for row in rows:
        for nib in row:
            counts[names[nib]] += 1
    total = sum(counts.values())
    print(out, out.stat().st_size)
    print({k: f"{v * 100 / total:.1f}%" for k, v in counts.items()})


if __name__ == "__main__":
    main()
