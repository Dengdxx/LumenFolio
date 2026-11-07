#include <stdlib.h>
#include <string.h>

#include "epd_frame.h"
#include "miniz.h"
#include "unity.h"
#include "unity_test_runner.h"

typedef struct {
    uint8_t *data;
    size_t size;
    bool fail;
} memory_frame_t;

static esp_err_t read_memory(void *context, size_t offset, uint8_t *data, size_t size)
{
    const memory_frame_t *frame = context;
    if (frame->fail || offset > frame->size || size > frame->size - offset) {
        return ESP_FAIL;
    }
    memcpy(data, frame->data + offset, size);
    return ESP_OK;
}

static void put_u32(uint8_t *p, uint32_t value)
{
    for (int i = 0; i < 4; ++i) {
        p[i] = (uint8_t)(value >> (8 * i));
    }
}

static void make_header(uint8_t *header, const char *magic)
{
    memset(header, 0, EPD_FRAME_ZLIB_HEADER_BYTES);
    memcpy(header, magic, 4);
    header[8] = EPD_FRAME_WIDTH & 0xff;
    header[9] = EPD_FRAME_WIDTH >> 8;
    header[10] = EPD_FRAME_HEIGHT & 0xff;
    header[11] = EPD_FRAME_HEIGHT >> 8;
}

static memory_frame_t compressed_white(size_t pixel_bytes)
{
    memory_frame_t frame = {.data = malloc(EPD_FRAME_MAX_BYTES)};
    uint8_t *pixels = malloc(pixel_bytes);
    tdefl_compressor *compressor = calloc(1, sizeof(*compressor));
    TEST_ASSERT_NOT_NULL(frame.data);
    TEST_ASSERT_NOT_NULL(pixels);
    TEST_ASSERT_NOT_NULL(compressor);
    memset(pixels, 0x11, pixel_bytes);
    make_header(frame.data, "EPD2");
    TEST_ASSERT_EQUAL(TDEFL_STATUS_OKAY,
                      tdefl_init(compressor, NULL, NULL, TDEFL_WRITE_ZLIB_HEADER | 1500));
    size_t input_bytes = pixel_bytes;
    size_t output_bytes = EPD_FRAME_MAX_BYTES - EPD_FRAME_ZLIB_HEADER_BYTES;
    TEST_ASSERT_EQUAL(TDEFL_STATUS_DONE,
                      tdefl_compress(compressor, pixels, &input_bytes,
                                     frame.data + EPD_FRAME_ZLIB_HEADER_BYTES, &output_bytes,
                                     TDEFL_FINISH));
    TEST_ASSERT_EQUAL(pixel_bytes, input_bytes);
    put_u32(frame.data + 12, output_bytes);
    frame.size = EPD_FRAME_ZLIB_HEADER_BYTES + output_bytes;
    free(compressor);
    free(pixels);
    return frame;
}

TEST_CASE("EPD 帧头拒绝错误尺寸和压缩长度", "[epd_frame]")
{
    uint8_t header[EPD_FRAME_ZLIB_HEADER_BYTES];
    epd_frame_info_t info;
    make_header(header, "EPD1");
    TEST_ASSERT_EQUAL(ESP_OK, epd_frame_parse_header(header, 12, &info));
    TEST_ASSERT_EQUAL(EPD_FRAME_MAX_BYTES, info.frame_bytes);
    TEST_ASSERT_FALSE(info.compressed);
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 11, &info));
    make_header(header, "EPD2");
    put_u32(header + 12, 100);
    TEST_ASSERT_EQUAL(ESP_OK, epd_frame_parse_header(header, 16, &info));
    TEST_ASSERT_EQUAL(116, info.frame_bytes);
    TEST_ASSERT_TRUE(info.compressed);
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 15, &info));
    put_u32(header + 12, 0);
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 16, &info));
    put_u32(header + 12, UINT32_MAX);
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 16, &info));
    put_u32(header + 12, 100);
    header[4] = 1;
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 16, &info));
    header[4] = 0;
    header[8] = 0;
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_SIZE, epd_frame_parse_header(header, 16, &info));
    make_header(header, "NOPE");
    TEST_ASSERT_EQUAL(ESP_ERR_NOT_FOUND, epd_frame_parse_header(header, 16, &info));
}

TEST_CASE("EPD1 与 EPD2 还原同一幅 4bpp 像素", "[epd_frame]")
{
    uint8_t *pixels = malloc(EPD_FRAME_PIXEL_BYTES);
    uint8_t *scratch = malloc(EPD_FRAME_PIXEL_BYTES);
    TEST_ASSERT_NOT_NULL(pixels);
    TEST_ASSERT_NOT_NULL(scratch);
    memory_frame_t frame = compressed_white(EPD_FRAME_PIXEL_BYTES);
    TEST_ASSERT_EQUAL(ESP_OK, epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    for (size_t i = 0; i < EPD_FRAME_PIXEL_BYTES; ++i) {
        TEST_ASSERT_EQUAL_HEX8(0x11, pixels[i]);
    }
    // 固定 Flash 槽可大于实际帧；解码器只读帧头声明的载荷。
    TEST_ASSERT_EQUAL(ESP_OK, epd_frame_decode(read_memory, &frame, 0x100000, pixels, scratch));
    make_header(frame.data, "EPD1");
    memset(frame.data + EPD_FRAME_RAW_HEADER_BYTES, 0x11, EPD_FRAME_PIXEL_BYTES);
    frame.size = EPD_FRAME_MAX_BYTES;
    TEST_ASSERT_EQUAL(ESP_OK, epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    for (size_t i = 0; i < EPD_FRAME_PIXEL_BYTES; ++i) {
        TEST_ASSERT_EQUAL_HEX8(0x11, pixels[i]);
    }
    free(frame.data);
    free(scratch);
    free(pixels);
}

TEST_CASE("EPD2 拒绝损坏 截断 尾随数据和解压膨胀", "[epd_frame]")
{
    uint8_t *pixels = malloc(EPD_FRAME_PIXEL_BYTES);
    uint8_t *scratch = malloc(EPD_FRAME_PIXEL_BYTES);
    TEST_ASSERT_NOT_NULL(pixels);
    TEST_ASSERT_NOT_NULL(scratch);
    memory_frame_t frame = compressed_white(EPD_FRAME_PIXEL_BYTES);
    frame.data[frame.size - 1] ^= 1;
    TEST_ASSERT_EQUAL(ESP_ERR_INVALID_CRC,
                      epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    frame.data[frame.size - 1] ^= 1;
    TEST_ASSERT_NOT_EQUAL(ESP_OK,
                          epd_frame_decode(read_memory, &frame, frame.size - 1, pixels, scratch));
    // 伪造更短声明，仍须由解压器发现流被截断，而非只依靠外层长度比较。
    put_u32(frame.data + 12, frame.size - EPD_FRAME_ZLIB_HEADER_BYTES - 1);
    TEST_ASSERT_NOT_EQUAL(ESP_OK,
                          epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    frame.data[frame.size++] = 0;
    put_u32(frame.data + 12, frame.size - EPD_FRAME_ZLIB_HEADER_BYTES);
    TEST_ASSERT_NOT_EQUAL(ESP_OK,
                          epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    frame.fail = true;
    TEST_ASSERT_EQUAL(ESP_FAIL, epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
    free(frame.data);
    for (int delta = -1; delta <= 1; delta += 2) {
        frame = compressed_white((size_t)((int)EPD_FRAME_PIXEL_BYTES + delta));
        TEST_ASSERT_NOT_EQUAL(ESP_OK,
                              epd_frame_decode(read_memory, &frame, frame.size, pixels, scratch));
        free(frame.data);
    }
    free(scratch);
    free(pixels);
}

/** @brief 独立上板测试入口；不链接相框的 Flash 写入或屏幕刷新代码。 */
void app_main(void)
{
    unity_run_all_tests();
}
