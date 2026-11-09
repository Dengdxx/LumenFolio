#pragma once

#include <stdint.h>

#include "board_keys.h"

/**
 * @file board_sleep.h
 * @brief 深度睡眠：睡前保持引脚电平、配置唤醒源、启动睡眠。
 *
 * 唤醒源只有两个：三个按键（EXT1 任意脚变低）和 RTC 定时器。充电状态用的是
 * GPIO38/39，不属于 RTC IO，**插拔 USB 不会唤醒设备**——睡眠期间插上充电器，
 * 要等到下一次定时唤醒才会发现。
 *
 * 睡前必须由调用方先收尾：熄灯、卸载 TF 卡、把面板控制脚浮空。本模块只负责
 * 引脚保持、唤醒源和最后那一下 esp_deep_sleep_start()。
 */

namespace framefolio::board_sleep {

/** @brief 定时唤醒间隔，秒。用来发现"睡眠中被插上电"，也用来计防残影的周期。 */
inline constexpr uint32_t kTimerWakeSeconds = 600;

/** @brief 每多少次定时唤醒补刷一帧。10 分钟 × 144 = 24 h，对应面板规格书的最长刷新间隔。 */
inline constexpr uint32_t kWakesPerRefresh = 144;

/** @brief 本次启动的唤醒来源。 */
struct wake_info {
    bool timer;        ///< RTC 定时器唤醒。
    bool key;          ///< 按键唤醒；此时 which 有效。
    board_key_t which; ///< 唤醒键，仅在 key 为 true 时有意义。
};

/**
 * @brief 解析本次启动的唤醒原因。
 *
 * @return 唤醒来源；冷启动（上电、复位、烧录）时各项为 false。
 *
 * @note 只在 app_main 里调用一次：只读唤醒状态寄存器，不改配置。
 * @note 非定时唤醒会把防残影的计数清零：用户刚操作过，画面已经刷新了。
 */
wake_info wakeup();

/**
 * @brief 定时唤醒计数 +1，并判断这次要不要补刷。
 *
 * @return true 表示到了补刷时间，计数已清零；false 表示直接回睡。
 * @note 只在 wakeup() 返回 timer 为 true 之后调用。
 */
bool take_refresh_slot();

/**
 * @brief 配置唤醒源并进入深度睡眠，不返回。
 *
 * @param[in] timer_seconds 定时唤醒间隔，秒；0 表示只能靠按键唤醒。
 *
 * @note 只把当前处于释放状态（高电平）的按键放进唤醒掩码：按住不放的键会让
 *       睡眠立刻结束，等于睡不下去。
 * @note 会保持 POWER_CTL、ADC_CTL、LED 数据脚的电平。唤醒后这些保持位要由
 *       release_holds() 解开。
 */
[[noreturn]] void enter(uint32_t timer_seconds);

/**
 * @brief 解开上一次睡眠留下的引脚保持。
 *
 * @note 数字脚的保持位在深睡唤醒时会被复位，RTC 脚的会留下来，所以开机第一件事
 *       统一清一遍，避免 LED、ADC_CTL 停在被锁死的电平上。
 */
void release_holds();

}  // namespace framefolio::board_sleep
