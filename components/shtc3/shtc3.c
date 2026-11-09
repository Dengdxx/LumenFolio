#include "shtc3.h"

#include <stddef.h>

#include "esp_check.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

static const char *kTag = "shtc3";

/* 单次 I2C 事务的超时。传感器最慢的是 12.1 ms 的转换，不在事务内等。 */
#define SHTC3_TIMEOUT_MS 100

/* SHTC3 命令字，GXHTC3 兼容。 */
static const uint8_t kCmdWakeUp[2]   = {0x35, 0x17};
static const uint8_t kCmdSleep[2]    = {0xB0, 0x98};
static const uint8_t kCmdReadId[2]   = {0xEF, 0xC8};
static const uint8_t kCmdMeasureT[2] = {0x78, 0x66}; /* T 先出、不用时钟拉伸 */

static i2c_master_dev_handle_t s_dev;
static bool s_ready;

static esp_err_t send_cmd(const uint8_t cmd[2])
{
    return i2c_master_transmit(s_dev, cmd, 2, SHTC3_TIMEOUT_MS);
}

/** @brief Sensirion CRC-8：多项式 0x31、初值 0xFF，每 2 字节数据后跟 1 字节校验。 */
static uint8_t crc8(const uint8_t *data, size_t len)
{
    uint8_t crc = 0xFF;
    for (size_t i = 0; i < len; ++i) {
        crc ^= data[i];
        for (int bit = 0; bit < 8; ++bit) {
            crc = (crc & 0x80) ? (uint8_t)((crc << 1) ^ 0x31) : (uint8_t)(crc << 1);
        }
    }
    return crc;
}

/**
 * @brief 唤醒传感器。
 *
 * 休眠态下这是唯一会被应答的命令；如果它已经被别的路径留在唤醒态，这里会收到
 * NACK（ESP_ERR_INVALID_RESPONSE），那不是故障，接着发测量命令即可。
 */
static esp_err_t wake(void)
{
    const esp_err_t err = send_cmd(kCmdWakeUp);
    return err == ESP_ERR_INVALID_RESPONSE ? ESP_OK : err;
}

esp_err_t shtc3_init(i2c_master_bus_handle_t bus)
{
    if (s_ready) {
        return ESP_OK;
    }
    if (bus == NULL) {
        return ESP_ERR_INVALID_ARG;
    }

    i2c_device_config_t cfg = {
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .device_address  = SHTC3_I2C_ADDR,
        .scl_speed_hz    = 100000,
    };
    ESP_RETURN_ON_ERROR(i2c_master_bus_add_device(bus, &cfg, &s_dev), kTag, "挂载设备");

    /* 上电即休眠，休眠时不会应答地址，所以先唤醒再探测，否则必然误判为未贴片。 */
    esp_err_t err = wake();
    if (err == ESP_OK) {
        vTaskDelay(1);
        err = i2c_master_probe(bus, SHTC3_I2C_ADDR, SHTC3_TIMEOUT_MS);
    }
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "0x%02X 无应答（%s）：查 U3 是否贴片、R29/R61 上拉与 3V3_MCU",
                 SHTC3_I2C_ADDR, esp_err_to_name(err));
        i2c_master_bus_rm_device(s_dev);
        s_dev = NULL;
        return err;
    }

    /* ID 只用来确认通信正常。GXHTC3 的 ID 与 SHTC3 未必相同，读不到也不阻断测量。 */
    uint8_t id[3] = {};
    err = send_cmd(kCmdReadId);
    if (err == ESP_OK) {
        vTaskDelay(1);
        err = i2c_master_receive(s_dev, id, sizeof(id), SHTC3_TIMEOUT_MS);
        if (err == ESP_OK && crc8(id, 2) != id[2]) {
            err = ESP_ERR_INVALID_CRC;
        }
    }
    if (err == ESP_OK) {
        ESP_LOGI(kTag, "ID 0x%04X", (unsigned)((id[0] << 8) | id[1]));
    } else {
        ESP_LOGW(kTag, "ID 读取异常（%s），继续按兼容命令测量", esp_err_to_name(err));
    }

    (void)send_cmd(kCmdSleep);
    s_ready = true;
    return ESP_OK;
}

bool shtc3_ready(void)
{
    return s_ready;
}

esp_err_t shtc3_read(shtc3_sample_t *out)
{
    if (!s_ready || out == NULL) {
        return ESP_ERR_INVALID_STATE;
    }

    esp_err_t err = wake();
    if (err == ESP_OK) {
        vTaskDelay(1);
        err = send_cmd(kCmdMeasureT);
    }
    if (err == ESP_OK) {
        /* T 先出时转换最长 12.1 ms；tick 默认 10 ms，取 20 ms 才能跨过它。 */
        vTaskDelay(pdMS_TO_TICKS(20));
        uint8_t data[6] = {};
        err = i2c_master_receive(s_dev, data, sizeof(data), SHTC3_TIMEOUT_MS);
        if (err == ESP_OK) {
            if (crc8(data, 2) != data[2] || crc8(data + 3, 2) != data[5]) {
                err = ESP_ERR_INVALID_CRC;
            } else {
                const uint16_t raw_t  = (uint16_t)((data[0] << 8) | data[1]);
                const uint16_t raw_rh = (uint16_t)((data[3] << 8) | data[4]);
                out->temperature_c    = -45.0f + 175.0f * (float)raw_t / 65536.0f;
                out->humidity_percent = 100.0f * (float)raw_rh / 65536.0f;
            }
        }
    }

    /* 失败也让它回去睡：宁可下次重新唤醒，也不要把它留在唤醒态持续耗电。 */
    (void)send_cmd(kCmdSleep);
    return err;
}
