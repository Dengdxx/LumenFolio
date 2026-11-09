#include "device_status.h"

#include <cstdio>
#include <cstring>

#include "esp_mac.h"
#include "freertos/FreeRTOS.h"

namespace {

/** @brief 充电状态的机器可读名字，写进 JSON。 */
const char *charge_code(board_charge_t charge)
{
    switch (charge) {
    case BOARD_CHARGE_CHARGING: return "charging";
    case BOARD_CHARGE_FULL:     return "full";
    case BOARD_CHARGE_IDLE:     return "idle";
    default:                    return "unknown";
    }
}

struct snapshot {
    bool battery_valid;
    int battery_mv;
    uint8_t battery_percent;
    board_charge_t charge;
    bool env_valid;
    float temperature_c;
    float humidity_percent;
};

// 写方是两个采样任务，读方是 httpd 任务；临界区只保护结构体拷贝，不包住格式化。
portMUX_TYPE s_lock = portMUX_INITIALIZER_UNLOCKED;
snapshot s_state = {};

// 基 MAC 出厂写死，启动时读一次。Wi-Fi STA 地址就是它，蓝牙公开地址是它加 2。
char s_identity[18];

}  // namespace

void deviceStatusInit()
{
    uint8_t mac[6] = {};
    if (esp_read_mac(mac, ESP_MAC_BASE) != ESP_OK) {
        return;
    }
    snprintf(s_identity, sizeof(s_identity), "%02x:%02x:%02x:%02x:%02x:%02x",
             mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]);
}

void deviceStatusSetPower(const board_power_sample_t &sample)
{
    taskENTER_CRITICAL(&s_lock);
    s_state.battery_valid   = true;
    s_state.battery_mv      = sample.millivolts;
    s_state.battery_percent = sample.percent;
    s_state.charge          = sample.charge;
    taskEXIT_CRITICAL(&s_lock);
}

void deviceStatusSetEnv(const shtc3_sample_t &sample)
{
    taskENTER_CRITICAL(&s_lock);
    s_state.env_valid         = true;
    s_state.temperature_c     = sample.temperature_c;
    s_state.humidity_percent  = sample.humidity_percent;
    taskEXIT_CRITICAL(&s_lock);
}

bool deviceStatusPower(board_power_sample_t *out)
{
    if (out == nullptr) {
        return false;
    }
    taskENTER_CRITICAL(&s_lock);
    const bool valid = s_state.battery_valid;
    if (valid) {
        out->millivolts = s_state.battery_mv;
        out->percent    = s_state.battery_percent;
        out->charge     = s_state.charge;
    }
    taskEXIT_CRITICAL(&s_lock);
    return valid;
}

bool deviceStatusEnv(shtc3_sample_t *out)
{
    if (out == nullptr) {
        return false;
    }
    taskENTER_CRITICAL(&s_lock);
    const bool valid = s_state.env_valid;
    if (valid) {
        out->temperature_c    = s_state.temperature_c;
        out->humidity_percent = s_state.humidity_percent;
    }
    taskEXIT_CRITICAL(&s_lock);
    return valid;
}

int deviceStatusJson(char *buf, size_t size)
{
    if (buf == nullptr || size == 0) {
        return 0;
    }

    snapshot state;
    taskENTER_CRITICAL(&s_lock);
    state = s_state;
    taskEXIT_CRITICAL(&s_lock);

    // 先各自成段再拼：任何一段写不下就整段丢掉，绝不能让 JSON 断在半截。
    // 身份不依赖采样，读不到基 MAC 时整段省略，旧客户端当作没有这个字段。
    char identity[40] = {};
    char battery[128] = {};
    char env[96] = {};
    int identity_len = 0;
    if (s_identity[0] != '\0') {
        identity_len = snprintf(identity, sizeof(identity), "\"id\":\"%s\"", s_identity);
        if (identity_len < 0 || static_cast<size_t>(identity_len) >= sizeof(identity)) {
            identity_len = 0;
        }
    }
    int battery_len = 0;
    int env_len = 0;

    if (state.battery_valid) {
        battery_len = snprintf(battery, sizeof(battery),
                               "\"battery_mv\":%d,\"battery_percent\":%u,\"charge\":\"%s\"",
                               state.battery_mv, static_cast<unsigned>(state.battery_percent),
                               charge_code(state.charge));
        if (battery_len < 0 || static_cast<size_t>(battery_len) >= sizeof(battery)) {
            battery_len = 0;
        }
    }
    if (state.env_valid) {
        env_len = snprintf(env, sizeof(env), "\"temperature_c\":%.1f,\"humidity_pct\":%.1f",
                           state.temperature_c, state.humidity_percent);
        if (env_len < 0 || static_cast<size_t>(env_len) >= sizeof(env)) {
            env_len = 0;
        }
    }

    const int lengths[] = {identity_len, battery_len, env_len};
    size_t total = 0;
    int parts = 0;
    for (const int length : lengths) {
        if (length > 0) {
            total += static_cast<size_t>(length);
            parts++;
        }
    }
    if (parts > 1) {
        total += static_cast<size_t>(parts - 1);
    }
    if (total == 0 || total >= size) {
        return 0;
    }

    const char *segments[] = {identity, battery, env};
    size_t at = 0;
    for (size_t index = 0; index < 3; index++) {
        if (lengths[index] <= 0) {
            continue;
        }
        if (at > 0) {
            buf[at++] = ',';
        }
        memcpy(buf + at, segments[index], static_cast<size_t>(lengths[index]));
        at += static_cast<size_t>(lengths[index]);
    }
    buf[at] = '\0';
    return static_cast<int>(at);
}
