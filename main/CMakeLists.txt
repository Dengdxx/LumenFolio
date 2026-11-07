idf_component_register(SRCS "main.cpp" "board_init.cpp" "board_led.cpp" "led_fsm.cpp" "board_wifi.cpp" "board_i2c.cpp" "board_power.cpp" "board_sd.cpp" "board_keys.cpp" "board_sleep.cpp" "console.cpp" "device_status.cpp"
                    INCLUDE_DIRS "."
                    REQUIRES epd709 epd_fb esp_driver_rmt esp_driver_i2c esp_driver_usb_serial_jtag esp_adc fatfs esp_driver_sdmmc sdmmc ota esp_wifi esp_netif esp_event nvs_flash esp_timer artifacts shtc3)
