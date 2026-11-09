#include "board_keys.h"

#include "board_ble.h"
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
constexpr uint32_t kAuxLongPressMs = 2000;
constexpr UBaseType_t kQueueDepth = 8;
constexpr size_t kKeyCount = 3;

struct key_route {
    gpio_num_t pin;
    board_key_t key;
};

constexpr key_route kRoutes[kKeyCount] = {
    {lumenfolio::board::kKey1, BOARD_KEY_PREV},
    {lumenfolio::board::kKey2, BOARD_KEY_NEXT},
    {lumenfolio::board::kKey3, BOARD_KEY_AUX},
};

QueueHandle_t s_queue;
TaskHandle_t s_aux_task;
bool s_aux_wake;
// 上次被采纳的按下时刻，只在中断里读写。
volatile TickType_t s_last_press[kKeyCount];

/**
 * @brief KEY1/KEY2 下降沿投递翻页，KEY3 双边沿通知任务判定按压时长。
 *
 * 不读引脚电平：中断已经说明是下降沿，再读一次只会引入新的竞态。
 */
void IRAM_ATTR key_isr(void *arg)
{
    const auto key = static_cast<board_key_t>(reinterpret_cast<intptr_t>(arg));
    if (key == BOARD_KEY_AUX) {
        BaseType_t woken = pdFALSE;
        vTaskNotifyGiveFromISR(s_aux_task, &woken);
        if (woken == pdTRUE) portYIELD_FROM_ISR();
        return;
    }
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

void post_short_aux()
{
    const board_key_event_t event = {BOARD_KEY_AUX};
    xQueueSend(s_queue, &event, 0);
}

/** @brief KEY3 在释放后确认短按，长按到点立即广播，整个过程独立于刷屏。 */
void aux_key_task(void *)
{
    ulTaskNotifyTake(pdTRUE, portMAX_DELAY);  // 等三个 GPIO 的中断装好后再采样。
    constexpr TickType_t hold_ticks = pdMS_TO_TICKS(kAuxLongPressMs);
    bool pressed = gpio_get_level(lumenfolio::board::kKey3) == 0;
    if (s_aux_wake && !pressed) post_short_aux();
    TickType_t pressed_at = xTaskGetTickCount();
    bool paired = false;
    for (;;) {
        TickType_t wait = portMAX_DELAY;
        if (pressed && !paired) {
            const TickType_t elapsed = xTaskGetTickCount() - pressed_at;
            wait = elapsed < hold_ticks ? hold_ticks - elapsed : 0;
        }
        if (ulTaskNotifyTake(pdTRUE, wait) != 0) {
            while (ulTaskNotifyTake(pdTRUE, pdMS_TO_TICKS(kDebounceMs)) != 0) {}
        }

        const bool down = gpio_get_level(lumenfolio::board::kKey3) == 0;
        if (down != pressed) {
            pressed = down;
            if (down) {
                pressed_at = xTaskGetTickCount();
                paired = false;
            } else if (!paired) {
                post_short_aux();
            }
        } else if (down && !paired && xTaskGetTickCount() - pressed_at >= hold_ticks) {
            paired = true;
            board_ble_open_window();
        }
    }
}

}  // namespace

esp_err_t board_keys_init(bool aux_wake)
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
    s_aux_wake = aux_wake;
    if (xTaskCreate(aux_key_task, "aux_key", 4096, nullptr, 3, &s_aux_task) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }

    for (size_t i = 0; i < kKeyCount; ++i) {
        // 上电后先给一个完整窗口，免得开机瞬间的抖动被当成一次按键。
        s_last_press[i] = xTaskGetTickCount() - pdMS_TO_TICKS(kDebounceMs);

        const auto edge = kRoutes[i].key == BOARD_KEY_AUX ? GPIO_INTR_ANYEDGE : GPIO_INTR_NEGEDGE;
        const esp_err_t err = gpio_set_intr_type(kRoutes[i].pin, edge);
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
    xTaskNotifyGive(s_aux_task);

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
