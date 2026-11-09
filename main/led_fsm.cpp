#include "led_fsm.h"

#include "board_led.h"
#include "esp_check.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"

namespace {

constexpr const char *kTag = "led_fsm";

// 灯效时序与亮度只改这里。docs/灯语设计.md 只给了名称，下列数值是待标定的占位。
constexpr uint32_t kTickMs = 50;
constexpr uint32_t kFastHalfMs = 150;     // 快闪：亮 150 ms，灭 150 ms
constexpr uint32_t kSlowHalfMs = 600;     // 慢闪：亮 600 ms，灭 600 ms
constexpr uint32_t kShortOnMs = 120;      // 短闪：亮 120 ms
constexpr uint32_t kHoldMs = 1200;        // 电量提示：常亮 1.2 s
constexpr uint8_t kBrightness = 32;       // 0–255，3V3 直驱单颗，先压低

constexpr uint8_t kAnomalyFlashes = 10;
constexpr uint8_t kQueueDepth = 8;

struct color { uint8_t r, g, b; };

constexpr color kOff   = {0, 0, 0};
constexpr color kWhite = {kBrightness, kBrightness, kBrightness};
constexpr color kBlue  = {0, 0, kBrightness};
constexpr color kCyan  = {0, kBrightness, kBrightness};
constexpr color kYellow = {kBrightness, kBrightness, 0};
constexpr color kGreen = {0, kBrightness, 0};
constexpr color kRed   = {kBrightness, 0, 0};

// 绿（100%）到红（0%）的线性渐变。满电/低电阈值尚未标定，先按百分比直接映射。
color battery_color(uint8_t percent)
{
    if (percent > 100) {
        percent = 100;
    }
    return { static_cast<uint8_t>(kBrightness * (100 - percent) / 100),
             static_cast<uint8_t>(kBrightness * percent / 100), 0 };
}

struct event_item {
    led_event_t event;
    uint8_t percent;
};

// 出队顺序：异常 → 电量 → 其余。同档内按入队先后。
int event_rank(led_event_t event)
{
    switch (event) {
    case LED_EV_ANOMALY: return 0;
    case LED_EV_BATTERY: return 1;
    default:             return 2;
    }
}

// 活动灯语盖过等待和充电：刷新 > 传输 > Pair 等待 > 充电。
int state_rank(led_state_t state)
{
    switch (state) {
    case LED_STATE_EPD_BUSY:  return 0;
    case LED_STATE_BLE_XFER:  return 1;
    case LED_STATE_WIFI_XFER: return 1;
    case LED_STATE_PAIR_WAIT: return 2;
    case LED_STATE_CHARGING:  return 3;
    default:                  return 4;
    }
}

// 两级队列：s_queue 负责跨任务投递，fsm_task 每轮至多取一条放进 s_pending。
// s_pending 按优先级插入，所以后到的高优先级会排到已积压的低优先级前面。
// s_pending、s_pending_count、s_battery_percent 只由 fsm_task 读写。
// s_state_on 由调用方写、fsm_task 读，单字节不加锁。
QueueHandle_t s_queue;
TaskHandle_t s_task;
event_item s_pending[kQueueDepth];
uint8_t s_pending_count;
bool s_state_on[LED_STATE_COUNT];
uint8_t s_battery_percent;

void enqueue(const event_item &item)
{
    if (s_pending_count == kQueueDepth) {
        ESP_LOGW(kTag, "事件队列已满，丢弃 %d", item.event);
        return;
    }
    uint8_t at = s_pending_count;
    while (at > 0 && event_rank(item.event) < event_rank(s_pending[at - 1].event)) {
        s_pending[at] = s_pending[at - 1];
        --at;
    }
    s_pending[at] = item;
    ++s_pending_count;
}

bool dequeue(event_item *out)
{
    if (s_pending_count == 0) {
        return false;
    }
    *out = s_pending[0];
    --s_pending_count;
    for (uint8_t i = 0; i < s_pending_count; ++i) {
        s_pending[i] = s_pending[i + 1];
    }
    return true;
}

// 当前应显示的持续状态；都没有时返回 false。
bool active_state(led_state_t *out)
{
    bool found = false;
    led_state_t best = LED_STATE_CHARGING;
    for (int i = 0; i < LED_STATE_COUNT; ++i) {
        auto state = static_cast<led_state_t>(i);
        if (s_state_on[i] && (!found || state_rank(state) < state_rank(best))) {
            best = state;
            found = true;
        }
    }
    *out = best;
    return found;
}

color state_color(led_state_t state)
{
    switch (state) {
    case LED_STATE_PAIR_WAIT: return kWhite;
    case LED_STATE_CHARGING:  return battery_color(s_battery_percent);
    case LED_STATE_BLE_XFER:  return kBlue;
    case LED_STATE_WIFI_XFER: return kCyan;
    case LED_STATE_EPD_BUSY:  return kYellow;
    default:                  return kOff;
    }
}

bool state_is_steady(led_state_t state)
{
    return state == LED_STATE_CHARGING;
}

uint32_t state_half_ms(led_state_t state)
{
    return (state == LED_STATE_BLE_XFER || state == LED_STATE_WIFI_XFER) ? kFastHalfMs : kSlowHalfMs;
}

void show(color c)
{
    board_led_set(c.r, c.g, c.b);
}

// 播一个一次性灯语。期间新到的事件只入队，播完再出。
void play(const event_item &item)
{
    switch (item.event) {
    case LED_EV_ANOMALY:
        for (uint8_t n = 0; n < kAnomalyFlashes; ++n) {
            show(kRed);
            vTaskDelay(pdMS_TO_TICKS(kFastHalfMs));
            show(kOff);
            vTaskDelay(pdMS_TO_TICKS(kFastHalfMs));
        }
        break;
    case LED_EV_BATTERY:
        s_battery_percent = item.percent;
        show(battery_color(item.percent));
        vTaskDelay(pdMS_TO_TICKS(kHoldMs));
        break;
    case LED_EV_BLE_CONNECTED:
    case LED_EV_WIFI_CONNECTED:
    case LED_EV_DONE:
    case LED_EV_NO_IMAGE: {
        const color c = item.event == LED_EV_BLE_CONNECTED ? kBlue
                      : item.event == LED_EV_WIFI_CONNECTED ? kCyan
                      : item.event == LED_EV_DONE ? kGreen
                      : kYellow;
        show(c);
        vTaskDelay(pdMS_TO_TICKS(item.event == LED_EV_NO_IMAGE ? kShortOnMs : kSlowHalfMs));
        break;
    }
    }
}

// 持续状态的一个节拍：常亮保持，闪烁则亮半拍、灭半拍。
void beat(led_state_t state)
{
    if (state_is_steady(state)) {
        show(state_color(state));
        vTaskDelay(pdMS_TO_TICKS(kTickMs));
        return;
    }
    const uint32_t half = state_half_ms(state);
    show(state_color(state));
    vTaskDelay(pdMS_TO_TICKS(half));
    show(kOff);
    vTaskDelay(pdMS_TO_TICKS(half));
}

void fsm_task(void *)
{
    show(kOff);
    for (;;) {
        event_item item;
        if (xQueueReceive(s_queue, &item, 0) == pdTRUE) {
            enqueue(item);
        }

        if (dequeue(&item)) {
            play(item);
            continue;
        }

        led_state_t state;
        if (active_state(&state)) {
            beat(state);
        } else {
            show(kOff);
            vTaskDelay(pdMS_TO_TICKS(kTickMs));
        }
    }
}

}  // namespace

esp_err_t led_fsm_init(void)
{
    if (s_queue != nullptr) {
        return ESP_OK;
    }
    ESP_RETURN_ON_ERROR(board_led_init(), kTag, "LED");

    s_queue = xQueueCreate(kQueueDepth, sizeof(event_item));
    if (s_queue == nullptr) {
        return ESP_ERR_NO_MEM;
    }
    if (xTaskCreate(fsm_task, "led_fsm", 3072, nullptr, 3, &s_task) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    return ESP_OK;
}

esp_err_t led_fsm_suspend(void)
{
    if (s_task == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    vTaskSuspend(s_task);
    // 任务停住时可能刚写完一帧颜色，这里补一帧黑，保证睡着时灯是灭的。
    return board_led_set(0, 0, 0);
}

esp_err_t led_event_post(led_event_t event, uint8_t percent)
{
    if (s_queue == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }
    const event_item item = { event, percent };
    return xQueueSend(s_queue, &item, 0) == pdTRUE ? ESP_OK : ESP_ERR_NO_MEM;
}

esp_err_t led_state_set(led_state_t state, bool on)
{
    if (s_queue == nullptr || state >= LED_STATE_COUNT) {
        return ESP_ERR_INVALID_STATE;
    }
    s_state_on[state] = on;
    return ESP_OK;
}
