#pragma once

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Spectra 6 颜色码，每像素 4 bit。
 * @note 高低半字节相同，可整字节填充。偶数 x 在高半字节，该约定须用棋盘格上板确认。
 */
#define BLACK           0x00
#define WHITE           0x11
#define YELLOW          0x22
#define RED             0x33
#define BLUE            0x55
#define GREEN           0x66

/**
 * @brief NT61522 命令码，走 SPI 命令相位。
 * @note 与颜色码共用数值空间，部分数值相同，两者不可混用。
 */
#define PSR             0x00
#define PWR             0x01
#define POF             0x02
#define POFS            0x03
#define PON             0x04
#define BTST_N          0x05
#define BTST_P          0x06
#define DTM             0x10
#define DRF             0x12
#define PLL             0x30
#define TSC             0x40
#define CDI             0x50
#define TCON            0x60
#define TRES            0x61
#define PTLW            0x83
#define AN_TM           0x74
#define AGID            0x86
#define CMDA4           0xA4
#define DCDC            0xA5
#define BUCK_BOOST_VDDN 0xB0
#define TFT_VCOM_POWER  0xB1
#define EN_BUF          0xB6
#define BOOST_VDDP_EN   0xB7
#define CCSET           0xE0
#define PWS             0xE3
#define CMD66           0xF0

/**
 * @brief PTLW 的窗口使能。
 * @note 局部刷新结束后必须发 PTLW_DISABLE，否则后续整帧会被裁进旧窗口。
 */
#define PTLW_ENABLE     0x01
#define PTLW_DISABLE    0x00

/** @brief 单颗 IC 的一次 DTM 长度。两颗各写一次，合计 960000 字节。 */
#define EPD_IMAGE_BYTES_PER_IC  480000
/** @brief 色条中一条色带的字节数。六条之和必须等于 EPD_IMAGE_BYTES_PER_IC。 */
#define EPD_COLOR_BAR_BYTES     80000

/** @brief 硬复位。返回时 BUSY 未必已拉高，initEPD() 才包含这次等待。 */
void epdHardwareReset(void);

/** @brief 同时驱动两片 CS。GPIO_LOW 选中，用于两颗 IC 都要收的命令。 */
void setPinCsAll(unsigned int level);

/**
 * @brief 单独选中一颗 IC。
 * @param cs_index 只接受 EPD_CS_MAIN 或 EPD_CS_SUB，其他值越界。
 */
void setPinCs(uint8_t cs_index, unsigned int level);

/**
 * @brief 等到 BUSY 变高，即面板空闲。
 * @param stage 只用于日志，标出超时发生在哪一步。
 * @return DONE，或 ERROR（到时仍为低，不复位面板）。
 */
uint8_t checkBusyHigh(const char *stage, uint32_t timeout_ms);

/** @brief 等到 BUSY 变低。超时只记日志，调用方看不出失败。 */
void checkBusyLow(void);

/**
 * @brief 完成 GPIO、SPI、硬复位和初始化序列。
 * @return DONE，或 ERROR（BUSY 未拉高，或 SPI 失败）。失败后不要刷屏。
 * @note 须在 3V3_EXT 稳定后调用。阻塞可达 15 s。
 */
uint8_t initEPD(void);

/**
 * @brief 发一条命令和参数，不操作片选。
 * @note 参数不超过 EPD_SPI_CHUNK_SIZE 时先拷进内部 DMA 缓冲。SPI 错误不返回给调用方。
 */
void writeEpd(uint8_t command, uint8_t *data, unsigned int length);

/** @brief 发读命令并收回数据，不操作片选。当前只用于 0xF2 状态。 */
void readEpd(uint8_t command, uint8_t *data, unsigned int length);

/** @brief 只发命令字。调用方在命令与后续数据之间保持 CS 有效。 */
void writeEpdCommand(uint8_t command);

/**
 * @brief 在当前片选下连续发数据，不带命令。
 * @note data 必须在内部 DMA RAM。超过 32 KB 时分块。
 */
void writeEpdData(uint8_t *data, unsigned int length);

/**
 * @brief 一次完整刷新：PON、DRF、POF，各阶段在 BUSY 期间保持 CS。
 * @return DONE，或 ERROR（任一阶段超时）。
 * @note 前置条件是两颗 IC 的 DTM 都已写满。每阶段最长等 60 s。
 */
uint8_t epdDisplay(void);

/**
 * @brief 执行整帧刷新，DRF 经 SPI 送出后、等待 BUSY 前通知调用方。
 * @note 回调在显示任务内同步执行，须立即返回；PON 或 DRF 发送失败时不调用。
 */
uint8_t epdDisplayWithCallback(void (*refresh_started)(void *), void *context);

/**
 * @brief 两颗 IC 填同一颜色后刷新。
 * @note 不依赖 PSRAM。epdDisplay() 的返回值被忽略，失败只能看日志。
 */
void epdDisplayColor(uint8_t color);

/**
 * @brief 向一颗 IC 写整块平面，不触发刷新。
 * @param cs_index 0 为主 IC（EPD_CS_MAIN），1 为从 IC。
 * @note length 应为 EPD_IMAGE_BYTES_PER_IC，本函数不校验。image 必须能直接作 SPI DMA 源。
 */
void writeEpdImage(uint8_t cs_index, const uint8_t *image, unsigned long length);

/**
 * @brief 每颗 IC 画六条色带并刷新，用来确认色序和左右分区。
 * @note 不依赖 PSRAM。
 * @return DONE，或 ERROR（PON、DRF、POF 任一阶段超时）。
 */
uint8_t epdDisplayColorBar(void);

/**
 * @brief 让主 IC 读一次面板温度并据此选波形。
 *
 * @note 串行模式下只能写，读回的字节恒为 0；这条命令的价值在于触发面板内部的
 *       温度读取。撤销它之后上板观察到左右两半出现色差，不要当成无效调用删掉。
 */
void executeTscCommand(void);

/**
 * @brief 读两颗 IC 的 0xF2，bit0 为 1 表示就绪。
 *
 * @note 串行模式下读回恒为 0，这个检查永远不会通过；保留它是为了和厂家样例
 *       逐条一致，日志里的 "did not reply" 是预期现象，不代表故障。
 */
uint8_t checkDriverICStatus(void);

/**
 * @brief 面板断电前的收尾：把所有控制脚改成浮空输入。
 *
 * @note 面板的 VDD/VDDIO 取自 3V3_EXT。断电后若控制脚仍被拉高，电流会经面板输入的
 *       ESD 二极管倒灌进已经关断的电源轨，相当于给面板"半供电"。所以必须先调本函数，
 *       再把 3V3_EXT 断掉。调用之后不要再刷屏，直到重新 initEPD()。
 */
void epdParkBus(void);

/**
 * @brief 设局部窗口并写入窗口内像素。
 * @param cs_index 0 为主 IC。坐标相对该 IC 的 600 列。
 * @param display_enable 非 0 时写完立即刷新并关闭窗口。
 * @return DONE，或负数表示窗口不满足硬件约束，此时不写数据。
 * @note x 起点不大于 584、宽度不超过 600，起止都须按 8 个寄存器单位对齐；y 起点加高度须为偶数。
 *       不刷新时调用方必须自己发 PTLW_DISABLE。
 */
char partialWindowUpdateWithImageData(uint8_t cs_index, const uint8_t *image, unsigned long length,
                                      unsigned int x_start, unsigned int y_start,
                                      unsigned int x_pixel, unsigned int y_line,
                                      uint8_t display_enable);
/**
 * @brief 只设局部窗口，不写像素。刷新的是窗口内已有的内容。
 * @return DONE，或负数。窗口约束与带数据的版本相同。
 */
char partialWindowUpdateWithoutImageData(uint8_t cs_index, unsigned int x_start, unsigned int y_start,
                                         unsigned int x_pixel, unsigned int y_line,
                                         uint8_t display_enable);

#ifdef __cplusplus
}  /* extern "C" */
#endif
