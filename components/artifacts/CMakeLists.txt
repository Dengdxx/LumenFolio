idf_component_register(SRCS "artifacts.c"
                    INCLUDE_DIRS "include"
                    REQUIRES esp_partition spi_flash epd_fb epd_frame freertos)
