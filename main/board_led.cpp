#include "board_led.h"

#include <stdlib.h>

#include "board_pins.h"
#include "driver/rmt_tx.h"
#include "esp_check.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

namespace {

constexpr const char *kTag = "board_led";
constexpr uint32_t kResolutionHz = 10'000'000;  // 1 tick = 0.1 µs

typedef struct {
    rmt_encoder_t base;
    rmt_encoder_t *bytes;
    rmt_encoder_t *copy;
    int state;
    rmt_symbol_word_t reset;
} led_encoder_t;

rmt_channel_handle_t s_channel;
rmt_encoder_handle_t s_encoder;

/**
 * @brief WS2812 帧的编码状态机：先像素字节，再复位低电平。
 *
 * RMT 缓冲不够时会多次回调。state 跨调用保留：0 发像素，1 发复位；
 * 未发完不得清 state，否则像素会被当成复位符号。
 */
RMT_ENCODER_FUNC_ATTR
size_t encode(rmt_encoder_t *encoder, rmt_channel_handle_t channel,
              const void *data, size_t data_size, rmt_encode_state_t *ret_state)
{
    auto *led = __containerof(encoder, led_encoder_t, base);
    rmt_encode_state_t session = RMT_ENCODING_RESET;
    uint32_t state = RMT_ENCODING_RESET;
    size_t symbols = 0;

    switch (led->state) {
    case 0:
        symbols += led->bytes->encode(led->bytes, channel, data, data_size, &session);
        if (session & RMT_ENCODING_COMPLETE) {
            led->state = 1;
        }
        if (session & RMT_ENCODING_MEM_FULL) {
            state |= RMT_ENCODING_MEM_FULL;
            break;
        }
        [[fallthrough]];
    case 1:
        symbols += led->copy->encode(led->copy, channel, &led->reset, sizeof(led->reset), &session);
        if (session & RMT_ENCODING_COMPLETE) {
            led->state = RMT_ENCODING_RESET;
            state |= RMT_ENCODING_COMPLETE;
        }
        if (session & RMT_ENCODING_MEM_FULL) {
            state |= RMT_ENCODING_MEM_FULL;
        }
        break;
    }
    *ret_state = static_cast<rmt_encode_state_t>(state);
    return symbols;
}

esp_err_t encoder_delete(rmt_encoder_t *encoder)
{
    auto *led = __containerof(encoder, led_encoder_t, base);
    rmt_del_encoder(led->bytes);
    rmt_del_encoder(led->copy);
    free(led);
    return ESP_OK;
}

RMT_ENCODER_FUNC_ATTR
esp_err_t encoder_reset(rmt_encoder_t *encoder)
{
    auto *led = __containerof(encoder, led_encoder_t, base);
    rmt_encoder_reset(led->bytes);
    rmt_encoder_reset(led->copy);
    led->state = RMT_ENCODING_RESET;
    return ESP_OK;
}

// WS2812 时序：0 码 0.3/0.9 µs，1 码 0.9/0.3 µs，高位先发；帧结束后拉低 50 µs 以上。
esp_err_t new_encoder(rmt_encoder_handle_t *out)
{
    auto *led = static_cast<led_encoder_t *>(rmt_alloc_encoder_mem(sizeof(led_encoder_t)));
    if (led == nullptr) {
        return ESP_ERR_NO_MEM;
    }
    led->base.encode = encode;
    led->base.del = encoder_delete;
    led->base.reset = encoder_reset;

    const uint32_t tick = kResolutionHz / 1'000'000;
    rmt_bytes_encoder_config_t bytes = {};
    bytes.bit0.duration0 = 3 * tick / 10;
    bytes.bit0.level0 = 1;
    bytes.bit0.duration1 = 9 * tick / 10;
    bytes.bit1.duration0 = 9 * tick / 10;
    bytes.bit1.level0 = 1;
    bytes.bit1.duration1 = 3 * tick / 10;
    bytes.flags.msb_first = 1;
    esp_err_t err = rmt_new_bytes_encoder(&bytes, &led->bytes);
    if (err == ESP_OK) {
        rmt_copy_encoder_config_t copy = {};
        err = rmt_new_copy_encoder(&copy, &led->copy);
    }
    if (err != ESP_OK) {
        if (led->bytes != nullptr) {
            rmt_del_encoder(led->bytes);
        }
        free(led);
        return err;
    }

    const uint32_t reset_ticks = 50 * tick / 2;
    led->reset.duration0 = reset_ticks;
    led->reset.duration1 = reset_ticks;
    *out = &led->base;
    return ESP_OK;
}

}  // namespace

esp_err_t board_led_init(void)
{
    if (s_channel != nullptr) {
        return ESP_OK;
    }

    rmt_tx_channel_config_t channel = {
        .gpio_num = framefolio::board::kLedWs2812,
        .clk_src = RMT_CLK_SRC_DEFAULT,
        .resolution_hz = kResolutionHz,
        .mem_block_symbols = 64,
        .trans_queue_depth = 1,
        .intr_priority = 0,
        .flags = {},
    };
    ESP_RETURN_ON_ERROR(rmt_new_tx_channel(&channel, &s_channel), kTag, "RMT 通道");
    ESP_RETURN_ON_ERROR(new_encoder(&s_encoder), kTag, "WS2812 编码器");
    ESP_RETURN_ON_ERROR(rmt_enable(s_channel), kTag, "RMT 使能");

    return board_led_set(0, 0, 0);
}

esp_err_t board_led_set(uint8_t red, uint8_t green, uint8_t blue)
{
    if (s_channel == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    // WS2812 线上的字节序是 G、R、B。
    const uint8_t pixel[3] = {green, red, blue};
    const rmt_transmit_config_t transmit = {};
    ESP_RETURN_ON_ERROR(rmt_transmit(s_channel, s_encoder, pixel, sizeof(pixel), &transmit), kTag, "发送");
    return rmt_tx_wait_all_done(s_channel, pdMS_TO_TICKS(100));
}
