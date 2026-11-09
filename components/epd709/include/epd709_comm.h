#pragma once

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @file epd709_comm.h
 * @brief EPD 的 GPIO 与 SPI 出口。
 *
 * 4 线 SPI，命令相位 8 位，半双工。片选由调用方用 GPIO 保持，本层不管 CS。
 * 单次 DMA 不超过 EPD_SPI_CHUNK_SIZE，源缓冲必须在内部 RAM，PSRAM 不能直接作 DMA 源。
 */

#define GPIO_LOW    0
#define GPIO_HIGH   1

/** @brief 单次 SPI DMA 的上限，也是 epdChunkBuffer() 的长度。 */
#define EPD_SPI_CHUNK_SIZE  32768

/**
 * @brief 初始化 SPI2，并分配分块缓冲。
 * @return DONE，或 ERROR（缓冲、总线或设备注册失败）。
 */
uint8_t initialSpi(void);

/** @brief 建立控制脚的空闲态：CS 与 RST 释放，D/C 为低，BUSY 为输入。硬复位之前调用。 */
void initialGpioDefault(void);

/**
 * @brief 把全部输出脚改成浮空输入。
 *
 * @note 给面板断电前调用：GPIO 还拉着高电平时切掉 3V3_EXT，电流会经面板输入的
 *       ESD 二极管倒灌回已经关断的电源轨。调用之后不要再发任何 SPI 命令。
 */
void epdFloatControlPins(void);

/** @brief 把 CS 与 RST 再拉高。只改电平，不重新配置方向。 */
void initialGpio(void);

/**
 * @brief 毫秒延时；不足一个 FreeRTOS tick 的非零请求用 ROM 微秒延时，避免截断为零。
 * @note 其余请求用 vTaskDelay() 让出 CPU，0 ms 仅让出 CPU。仅在任务上下文调用。
 */
void delayms(unsigned int delay_time);

/** @brief 只发命令字。内部把 D/C 拉低；片选仍由调用方保持。 */
uint8_t spiTransmitCommand(uint8_t command);

/**
 * @brief 发任意长度数据，按 EPD_SPI_CHUNK_SIZE 分块。
 * @return 某一块失败则停止，剩余数据不再发。
 * @note 调用前 CS 必须有效。data 必须在内部 RAM。
 */
uint8_t spiTransmitData(uint8_t *data, unsigned long length);

/** @brief 先发命令再发数据。约束同 spiTransmitCommand() 与 spiTransmitData()。 */
uint8_t spiTransmitLargeData(uint8_t command, uint8_t *data, unsigned long length);

/** @brief 发命令和不超过一块的参数。片选由调用方保持。 */
uint8_t spiTransmit(uint8_t command, uint8_t *data, unsigned int length);

/** @brief 发读命令并收回数据。片选由调用方保持。 */
uint8_t spiReceive(uint8_t command, uint8_t *data, unsigned int length);

/** @brief 在已经发出的读命令之后收数据。D/C 为高。 */
uint8_t spiReceiveData(uint8_t *data, unsigned int length);

/** @brief 写 GPIO 电平。pin 为 gpio_num_t 的数值。 */
void setGpioLevel(uint8_t pin, uint8_t level);

/** @brief 读 GPIO 电平。 */
uint8_t getGpioLevel(uint8_t pin);

/**
 * @brief 判断 BUSY 是悬空还是被强驱动。
 * @note 会短暂改该脚的上下拉，只在等待超时后调用。
 */
void diagnoseBusyPin(void);

/** @brief 内部 DMA 分块缓冲。initialSpi() 成功前为 NULL。 */
uint8_t *epdChunkBuffer(void);

#ifdef __cplusplus
}  /* extern "C" */
#endif
