#include "device_status.h"

#include <cstdio>
#include <cstring>

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

}  // namespace

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
    char battery[128] = {};
    char env[96] = {};
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

    const size_t separator = (battery_len > 0 && env_len > 0) ? 1 : 0;
    const size_t total = static_cast<size_t>(battery_len) + separator + static_cast<size_t>(env_len);
    if (total == 0 || total >= size) {
        return 0;
    }

    size_t at = 0;
    if (battery_len > 0) {
        memcpy(buf, battery, static_cast<size_t>(battery_len));
        at = static_cast<size_t>(battery_len);
    }
    if (env_len > 0) {
        if (at > 0) {
            buf[at++] = ',';
        }
        memcpy(buf + at, env, static_cast<size_t>(env_len));
        at += static_cast<size_t>(env_len);
    }
    buf[at] = '\0';
    return static_cast<int>(at);
}
