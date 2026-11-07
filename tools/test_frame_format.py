"""验证 Python 产帧与固件共用的 EPD1／EPD2 布局。"""

import struct
import unittest
from unittest.mock import patch
import zlib

import make_schematic_frame as frame


class FrameFormatTests(unittest.TestCase):
    def test_six_inks_round_trip(self):
        for ink in frame.PREVIEW_RGB:
            with self.subTest(ink=ink):
                encoded = frame.pack([[ink] * frame.WIDTH] * frame.HEIGHT)
                magic, x, y, width, height, length = struct.unpack("<4s4HI", encoded[:16])
                self.assertEqual((magic, x, y, width, height), (b"EPD2", 0, 0, 1200, 1600))
                self.assertEqual(length, len(encoded) - 16)
                self.assertLess(len(encoded), 960012)
                self.assertEqual(zlib.decompress(encoded[16:]), bytes([ink * 17]) * 960000)

    def test_nibble_order_is_unchanged(self):
        row = [frame.BLACK, frame.WHITE, frame.YELLOW, frame.RED, frame.BLUE, frame.GREEN] * 200
        encoded = frame.pack([row] * frame.HEIGHT)
        self.assertEqual(zlib.decompress(encoded[16:]), b"\x01\x23\x56" * 320000)

    def test_raw_fallback(self):
        with patch.object(frame.zlib, "compress", return_value=b"x" * 960000):
            encoded = frame.pack([[frame.WHITE] * frame.WIDTH] * frame.HEIGHT)
        self.assertEqual(encoded[:4], b"EPD1")
        self.assertEqual(len(encoded), 960012)
        self.assertEqual(encoded[12:], b"\x11" * 960000)

    def test_wrong_dimensions_are_rejected(self):
        for rows in ([], [[0]], [[0] * frame.WIDTH] * (frame.HEIGHT - 1),
                     [[0] * frame.WIDTH] * (frame.HEIGHT - 1) + [[0]]):
            with self.subTest(rows=len(rows)):
                with self.assertRaises(SystemExit):
                    frame.pack(rows)


if __name__ == "__main__":
    unittest.main()
