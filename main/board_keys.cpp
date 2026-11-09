#include "board_keys.h"

#include "board_pins.h"
#include "driver/gpio.h"
#include "esp_attr.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

namespace {

constexpr const char *kTag = "keys";

// 按下后这段时间内的边沿都算抖动。触点抖动一般在 20 ms 以内，60 ms 留足余量，
// 又短到不会吞掉人的第二次按键。
constexpr uint32_t kDebounceMs = 60;
constexpr UBaseType_t kQueueDepth = 8;
constexpr size_t kKeyCount = 3;

struct key_route {
    gpio_num_t pin;
    board_key_t key;
};

constexpr key_route kRoutes[kKeyCount] = {
    {framefolio::board::kKey1, BOARD_KEY_PREV},
    {framefolio::board::kKey2, BOARD_KEY_NEXT},
    {framefolio::board::kKey3, BOARD_KEY_AUX},
};

QueueHandle_t s_queue;
// 上次被采纳的按下时刻，只在中断里读写。
volatile TickType_t s_last_press[kKeyCount];

/**
 * @brief 下降沿中断：时间窗内只认第一次，然后把事件投进队列。
 *
 * 不读引脚电平：中断已经说明是下降沿，再读一次只会引入新的竞态。
 */
void IRAM_ATTR key_isr(void *arg)
{
    const auto key = static_cast<board_key_t>(reinterpret_cast<intptr_t>(arg));
    const TickType_t now = xTaskGetTickCountFromISR();
    if ((now - s_last_press[key]) < pdMS_TO_TICKS(kDebounceMs)) {
        return;
    }
    s_last_press[key] = now;

    const board_key_event_t event = {key};
    BaseType_t woken = pdFALSE;
    xQueueSendFromISR(s_queue, &event, &woken);
    if (woken == pdTRUE) {
        portYIELD_FROM_ISR();
    }
}

}  // namespace

esp_err_t board_keys_init(void)
{
    if (s_queue != nullptr) {
        return ESP_OK;
    }

    // 别的模块可能已经装过服务；那种情况按成功处理。
    const esp_err_t install = gpio_install_isr_service(0);
    if (install != ESP_OK && install != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(kTag, "GPIO 中断服务安装失败：%s", esp_err_to_name(install));
        return install;
    }

    s_queue = xQueueCreate(kQueueDepth, sizeof(board_key_event_t));
    if (s_queue == nullptr) {
        return ESP_ERR_NO_MEM;
    }

    for (size_t i = 0; i < kKeyCount; ++i) {
        // 上电后先给一个完整窗口，免得开机瞬间的抖动被当成一次按键。
        s_last_press[i] = xTaskGetTickCount() - pdMS_TO_TICKS(kDebounceMs);

        const esp_err_t err = gpio_set_intr_type(kRoutes[i].pin, GPIO_INTR_NEGEDGE);
        if (err != ESP_OK) {
            ESP_LOGE(kTag, "GPIO%d 中断类型设置失败：%s", kRoutes[i].pin, esp_err_to_name(err));
            return err;
        }
        const esp_err_t add = gpio_isr_handler_add(
            kRoutes[i].pin, key_isr, reinterpret_cast<void *>(static_cast<intptr_t>(kRoutes[i].key)));
        if (add != ESP_OK) {
            ESP_LOGE(kTag, "GPIO%d 中断注册失败：%s", kRoutes[i].pin, esp_err_to_name(add));
            return add;
        }
    }

    return ESP_OK;
}

QueueHandle_t board_keys_queue(void)
{
    return s_queue;
}

esp_err_t board_keys_read(board_key_event_t *event, uint32_t timeout_ms)
{
    if (s_queue == nullptr || event == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const TickType_t ticks = (timeout_ms == UINT32_MAX) ? portMAX_DELAY : pdMS_TO_TICKS(timeout_ms);
    return xQueueReceive(s_queue, event, ticks) == pdTRUE ? ESP_OK : ESP_ERR_TIMEOUT;
}
