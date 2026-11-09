# epd709 — OSPTEK 7.09" EPD 驱动（移植）

## 易错点（务必遵守）

- **短延时被截断为零**：FreeRTOS 为 100 Hz 时，一个 tick 是 10 ms，直接对 1 ms、5 ms 使用
  `vTaskDelay(pdMS_TO_TICKS(...))` 会变成零 tick，不能保证命令后的等待或 BUSY 上下拉采样的稳定时间。
  硬件毫秒等待统一走 `delayms()`：不足一个 tick 的非零请求用 `esp_rom_delay_us()` 短时忙等，
  其余请求仍用 `vTaskDelay()` 让出 CPU；0 ms 保留仅让出 CPU 的语义。不要把它用于 ISR。
- **读回恒为 0，但 TSC 命令不能删**：面板规格书 5.2 / 5.3 都写明 "Under serial mode, only write
  operations are allowed"，`0xF2` 状态和 `TSC` 温度在串行模式下读回来都是 `0x00`，不能据此判断
  面板故障。但 `TSC` 的副作用是让驱动 IC 去读面板温度并据此选波形：把它删掉后上板观察到左右
  两半出现明显色差，加回来就恢复。`checkDriverICStatus()` 的 "did not reply" 同样是预期现象，
  保留它是为了与厂家样例逐条一致。
- **PON 与 DRF 之间不要插命令**：上板实测，在 PON 完成之后插一次 TSC 读取，DRF 会超过 60 s 的
  等待上限直接超时；去掉后恢复 35 s 左右。刷新序列一旦开始就只能到 POF 结束。

## 出处与许可

- 上游：<https://gitee.com/osptek/display-epd-7.09inch-1200x1600>
  示例工程 `examples/esp32s3-pv61522-spi-bringup`（`main/epd/epd709.c`、`epd709_comm.c` 及头文件）
- 作者：OSPTEK 鱼鹰光电（<luyu@osptek.com>）
- 许可：**CC BY 4.0**，全文见 `LICENSE-OSPTEK-CC-BY-4.0.txt`
- 模组：EPD0709A02，1200×1600 Spectra 6，控制器 `NT61522 ×2 + EK73601 ×1`，接口 SPI/QSPI

## 相对上游的改动（共有）

1. **引脚改为本板原理图**（`include/epd709_pins.h`）：RST48 / BUSY21 / DC47 / CS0 10 / CS1 9 /
   SCK12 / SI0 11 / SI1 13；上游示例板是另一套引脚。SI2/SI3 本板未引出，也没有 LOAD_SW。
2. `epd709_comm.c` 的 `initialGpioDefault()` 改用 `EPD_PIN_MASK()`，跳过 NC 脚
   （否则 `1ULL << -1` 未定义）。
3. 保留面板初始化参数、寄存器写入顺序与 PON → DRF → POF 流程；通信层适配本板 4 线 SPI，
   BUSY 等待增加超时和日志。
4. `delayms()` 对不足一个 tick 的非零请求采用 ROM 微秒延时；BUSY 诊断的 5 ms 稳定等待也使用它。
5. 新增 `epdParkBus()`：给面板断电前把控制脚全部改成浮空输入。控制脚还拉着高电平时切
   3V3_EXT，电流会经面板输入的 ESD 二极管倒灌进已经关断的电源轨。

## 组件内的文件

| 文件 | 说明 |
|---|---|
| `include/epd709.h` | 命令码、颜色码、对外 API |
| `include/epd709_comm.h` | SPI/GPIO 层 API |
| `include/status.h` | `DONE` / `ERROR` |
| `include/epd709_pins.h` | 本板引脚（改动点） |
| `src/epd709.c` | 初始化序列、刷新、色条、局部窗口 |
| `src/epd709_comm.c` | SPI2 主机 + 双 CS 手动控制 |

## 已知限制

- 单 IC 帧缓冲 **480,000 字节**（600×1600/2），两颗共 960,000 字节。`writeEpdImage()` 需要调用方
  一次性提供整块缓冲 → **必须开 PSRAM**。PSRAM 关闭时只有
  `epdDisplayColor()` / `epdDisplayColorBar()` 这种按 32KB 分块流式的路径可用。
  整帧双缓冲已在 `components/epd_fb`（本项目自写，MIT）里实现。
- 面板的串行接口只支持写，没有读回通道：`0xF2` 状态和 `TSC` 温度都读不到有效值，恒为 0。
  温度补偿在驱动 IC 内部完成，主机查询不到；但 `TSC` 命令本身要照发，否则温度补偿不生效。
- 局部刷新 `partialWindowUpdate*()` 的窗口约束由上游保留（x 起点和宽度须按 4 像素对齐，宽度至少 16 像素等）。
