#include "board_power.h"

#include "board_pins.h"
#include "driver/gpio.h"
#include "esp_adc/adc_cali.h"
#include "esp_adc/adc_cali_scheme.h"
#include "esp_adc/adc_oneshot.h"
#include "esp_check.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

namespace {

constexpr const char *kTag = "power";

// 分压接通到稳定：220k+110k 源阻抗配 C58 10nF，时间常数约 3.3 ms，留三倍余量。
constexpr uint32_t kSettleMs = 10;
// 取中位数而不是平均值：Wi-Fi 突发时 ADC 偶有单点毛刺，平均值会被带偏。
constexpr int kSampleCount = 15;

adc_oneshot_unit_handle_t s_adc;
adc_cali_handle_t s_cali;
bool s_ready;

// 单节锂电的开路电压-电量对照，单位 mV。中间值线性插值。
struct curve_point {
    int millivolts;
    uint8_t percent;
};

constexpr curve_point kCurve[] = {
    {4200, 100}, {4000, 85}, {3900, 75}, {3800, 60}, {3700, 40},
    {3600, 20},  {3500, 10}, {3400, 5},  {3300, 0},
};

uint8_t voltage_to_percent(int millivolts)
{
    constexpr size_t kCount = sizeof(kCurve) / sizeof(kCurve[0]);
    if (millivolts >= kCurve[0].millivolts) {
        return kCurve[0].percent;
    }
    if (millivolts <= kCurve[kCount - 1].millivolts) {
        return kCurve[kCount - 1].percent;
    }
    for (size_t i = 1; i < kCount; ++i) {
        if (millivolts >= kCurve[i].millivolts) {
            const int hi_mv = kCurve[i - 1].millivolts;
            const int lo_mv = kCurve[i].millivolts;
            const int hi_pc = kCurve[i - 1].percent;
            const int lo_pc = kCurve[i].percent;
            return static_cast<uint8_t>(lo_pc + (millivolts - lo_mv) * (hi_pc - lo_pc) / (hi_mv - lo_mv));
        }
    }
    return 0;
}

int median_of(int *values, int count)
{
    for (int i = 1; i < count; ++i) {
        const int key = values[i];
        int j = i - 1;
        while (j >= 0 && values[j] > key) {
            values[j + 1] = values[j];
            --j;
        }
        values[j + 1] = key;
    }
    return values[count / 2];
}

}  // namespace

esp_err_t board_power_init()
{
    if (s_ready) {
        return ESP_OK;
    }

    adc_oneshot_unit_init_cfg_t unit = {};
    unit.unit_id  = ADC_UNIT_1;
    unit.ulp_mode = ADC_ULP_MODE_DISABLE;
    ESP_RETURN_ON_ERROR(adc_oneshot_new_unit(&unit, &s_adc), kTag, "ADC 单元");

    adc_oneshot_chan_cfg_t channel = {};
    channel.atten    = ADC_ATTEN_DB_12;
    channel.bitwidth = ADC_BITWIDTH_DEFAULT;
    ESP_RETURN_ON_ERROR(adc_oneshot_config_channel(s_adc, lumenfolio::board::kBatteryChannel, &channel), kTag, "ADC 通道");

    adc_cali_curve_fitting_config_t cali = {};
    cali.unit_id  = ADC_UNIT_1;
    cali.chan     = lumenfolio::board::kBatteryChannel;
    cali.atten    = ADC_ATTEN_DB_12;
    cali.bitwidth = ADC_BITWIDTH_DEFAULT;
    if (adc_cali_create_scheme_curve_fitting(&cali, &s_cali) != ESP_OK) {
        // 没有校准也能看趋势，只是绝对电压有偏差，这里不阻断采样。
        ESP_LOGW(kTag, "ADC 校准不可用，改按满量程折算");
        s_cali = nullptr;
    }

    s_ready = true;
    return ESP_OK;
}

esp_err_t board_power_read(board_power_sample_t *out)
{
    if (!s_ready || out == nullptr) {
        return ESP_ERR_INVALID_STATE;
    }

    gpio_set_level(lumenfolio::board::kBatteryAdcCtl, 1);
    vTaskDelay(pdMS_TO_TICKS(kSettleMs));

    int raw[kSampleCount] = {};
    esp_err_t err = ESP_OK;
    for (int i = 0; i < kSampleCount; ++i) {
        err = adc_oneshot_read(s_adc, lumenfolio::board::kBatteryChannel, &raw[i]);
        if (err != ESP_OK) {
            break;
        }
    }
    // 采样结束立刻断开分压，别让这条支路一直漏电。
    gpio_set_level(lumenfolio::board::kBatteryAdcCtl, 0);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "ADC 读取失败：%s", esp_err_to_name(err));
        return err;
    }

    const int code = median_of(raw, kSampleCount);
    int pin_mv = 0;
    if (s_cali != nullptr) {
        err = adc_cali_raw_to_voltage(s_cali, code, &pin_mv);
        if (err != ESP_OK) {
            ESP_LOGE(kTag, "校准换算失败：%s", esp_err_to_name(err));
            return err;
        }
    } else {
        // 12 dB 档满量程约 3.1 V，12 位码值按比例折算。
        pin_mv = static_cast<int>(static_cast<int64_t>(code) * 3100 / 4095);
    }

    out->millivolts = pin_mv * static_cast<int>(lumenfolio::board::kBatteryDividerNum) /
                      static_cast<int>(lumenfolio::board::kBatteryDividerDen);
    out->percent = voltage_to_percent(out->millivolts);

    const bool charging = gpio_get_level(lumenfolio::board::kPowerChgStat) == lumenfolio::board::kChargeActiveLevel;
    const bool full     = gpio_get_level(lumenfolio::board::kPowerFullStat) == lumenfolio::board::kChargeActiveLevel;
    out->charge = charging ? BOARD_CHARGE_CHARGING
                : full     ? BOARD_CHARGE_FULL
                           : BOARD_CHARGE_IDLE;
    return ESP_OK;
}
