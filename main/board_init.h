#pragma once

/**
 * @brief 上电配置离散 GPIO。3V3_EXT 保持关断。
 *
 * 不管 EPD、TF、I2C 的引脚，也不配置 GPIO14（留给 RMT）。
 * 非线程安全，其他任务使用这些脚之前调用一次。
 *
 * @note 3V3_EXT 只给面板和面板上的温度传感器供电，要用面板时再调
 *       board_power_rail_on()。定时唤醒的空转不开这条轨，省掉一次上电冲击。
 */
void board_gpio_init();

/**
 * @brief 打开 3V3_EXT 并等 SY8089 软启动完成。
 *
 * @note 阻塞约 50 ms。必须在 initEPD() 之前调用，否则面板的 VDD/VDDIO 还没起来。
 * @note 重复调用没有副作用。
 */
void board_power_rail_on();
