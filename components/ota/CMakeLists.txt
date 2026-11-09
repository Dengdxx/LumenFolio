idf_component_register(SRCS "ota_update.c" "ota_service.c"
                    INCLUDE_DIRS "include"
                    REQUIRES app_update esp_http_server esp_timer esp_app_format lwip)
