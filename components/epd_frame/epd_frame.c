#include "epd_frame.h"

#include <stdlib.h>
#include <string.h>

#include "miniz.h"

static uint16_t read_u16(const uint8_t *p)
{
    return (uint16_t)p[0] | ((uint16_t)p[1] << 8);
}

static uint32_t read_u32(const uint8_t *p)
{
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

esp_err_t epd_frame_parse_header(const uint8_t *header, size_t size, epd_frame_info_t *info)
{
    if (header == NULL || info == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    if (size < EPD_FRAME_RAW_HEADER_BYTES) {
        return ESP_ERR_INVALID_SIZE;
    }
    const bool compressed = memcmp(header, "EPD2", 4) == 0;
    if (!compressed && memcmp(header, "EPD1", 4) != 0) {
        return ESP_ERR_NOT_FOUND;
    }
    if (read_u16(header + 4) != 0 || read_u16(header + 6) != 0 ||
        read_u16(header + 8) != EPD_FRAME_WIDTH || read_u16(header + 10) != EPD_FRAME_HEIGHT) {
        return ESP_ERR_INVALID_SIZE;
    }

    size_t header_bytes = EPD_FRAME_RAW_HEADER_BYTES;
    size_t payload_bytes = EPD_FRAME_PIXEL_BYTES;
    if (compressed) {
        if (size < EPD_FRAME_ZLIB_HEADER_BYTES) {
            return ESP_ERR_INVALID_SIZE;
        }
        header_bytes = EPD_FRAME_ZLIB_HEADER_BYTES;
        payload_bytes = read_u32(header + 12);
        if (payload_bytes == 0 || payload_bytes > EPD_FRAME_MAX_BYTES - header_bytes) {
            return ESP_ERR_INVALID_SIZE;
        }
    }
    *info = (epd_frame_info_t){
        .header_bytes = header_bytes,
        .frame_bytes = header_bytes + payload_bytes,
        .compressed = compressed,
    };
    return ESP_OK;
}

esp_err_t epd_frame_decode(epd_frame_read_fn read, void *context, size_t source_bytes,
                           uint8_t *pixels, uint8_t *compressed)
{
    if (read == NULL || pixels == NULL || compressed == NULL || pixels == compressed) {
        return ESP_ERR_INVALID_ARG;
    }
    if (source_bytes < EPD_FRAME_RAW_HEADER_BYTES) {
        return ESP_ERR_INVALID_SIZE;
    }
    uint8_t header[EPD_FRAME_ZLIB_HEADER_BYTES] = {0};
    esp_err_t err = read(context, 0, header, EPD_FRAME_RAW_HEADER_BYTES);
    if (err != ESP_OK) {
        return err;
    }
    if (memcmp(header, "EPD2", 4) == 0) {
        if (source_bytes < sizeof(header)) {
            return ESP_ERR_INVALID_SIZE;
        }
        err = read(context, EPD_FRAME_RAW_HEADER_BYTES, header + EPD_FRAME_RAW_HEADER_BYTES,
                   EPD_FRAME_ZLIB_HEADER_BYTES - EPD_FRAME_RAW_HEADER_BYTES);
        if (err != ESP_OK) {
            return err;
        }
    }
    epd_frame_info_t info;
    err = epd_frame_parse_header(header, sizeof(header), &info);
    if (err != ESP_OK) {
        return err;
    }
    if (info.frame_bytes > source_bytes) {
        return ESP_ERR_INVALID_SIZE;
    }
    if (!info.compressed) {
        return read(context, info.header_bytes, pixels, EPD_FRAME_PIXEL_BYTES);
    }

    tinfl_decompressor *state = calloc(1, sizeof(*state));
    if (state == NULL) {
        return ESP_ERR_NO_MEM;
    }
    size_t input_bytes = info.frame_bytes - info.header_bytes;
    err = read(context, info.header_bytes, compressed, input_bytes);
    if (err == ESP_OK) {
        tinfl_init(state);
        size_t output_bytes = EPD_FRAME_PIXEL_BYTES;
        const size_t expected_input = input_bytes;
        const tinfl_status status = tinfl_decompress(
            state, compressed, &input_bytes, pixels, pixels, &output_bytes,
            TINFL_FLAG_PARSE_ZLIB_HEADER | TINFL_FLAG_USING_NON_WRAPPING_OUTPUT_BUF);
        if (status == TINFL_STATUS_ADLER32_MISMATCH) {
            err = ESP_ERR_INVALID_CRC;
        } else if (status != TINFL_STATUS_DONE || input_bytes != expected_input ||
                   output_bytes != EPD_FRAME_PIXEL_BYTES) {
            // 拒绝截断、尾随流及解压膨胀；绝不能把未完整验证的半帧交给屏幕。
            err = ESP_ERR_INVALID_RESPONSE;
        }
    }
    free(state);
    return err;
}
