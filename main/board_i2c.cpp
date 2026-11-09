#include "board_i2c.h"

#include "board_pins.h"
#include "esp_log.h"

namespace {

constexpr const char *kTag = "i2c";

i2c_master_bus_handle_t s_bus = nullptr;

}  // namespace

esp_err_t board_i2c_init()
{
    if (s_bus != nullptr) {
        return ESP_OK;
    }

    i2c_master_bus_config_t cfg = {};
    cfg.i2c_port                    = -1;  // 自动挑一个空闲主机
    cfg.sda_io_num                  = framefolio::board::kI2cSda;
    cfg.scl_io_num                  = framefolio::board::kI2cScl;
    cfg.clk_source                  = I2C_CLK_SRC_DEFAULT;
    cfg.glitch_ignore_cnt           = 7;
    cfg.flags.enable_internal_pullup = 0;  // 板上已有 10k 外部上拉

    const esp_err_t err = i2c_new_master_bus(&cfg, &s_bus);
    if (err != ESP_OK) {
        ESP_LOGE(kTag, "总线创建失败：%s", esp_err_to_name(err));
        s_bus = nullptr;
        return err;
    }

    ESP_LOGI(kTag, "主机总线就绪 sda=%d scl=%d", framefolio::board::kI2cSda, framefolio::board::kI2cScl);
    return ESP_OK;
}

i2c_master_bus_handle_t board_i2c_bus()
{
    return s_bus;
}
