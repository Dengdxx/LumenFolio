// Package ota 是相框的固件升级客户端。
// 命令行和界面都调用这里：界面不解析命令行，命令行也不进入界面。
package ota

// 通过 HTTP 查询状态、流式上传应用镜像、等待重启并确认新固件。
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"lumenfolio/devicehttp"
	"lumenfolio/identity"
)

const (
	defaultUploadTimeout = 3 * time.Minute
	defaultWaitTimeout   = 90 * time.Second
)

// otaStatus 是设备 /ota/status 与 OTA 接口返回的 JSON 状态。
type otaStatus struct {
	Phase            string `json:"phase"`             // 设备 OTA 状态机阶段，如 idle、reboot_pending、rollback_failed
	Ready            bool   `json:"ready"`             // 基础启动和自检流程已返回，不代表传感器全部正常或通过飞行验证
	PendingVerify    bool   `json:"pending_verify"`    // 新固件是否等待确认
	Project          string `json:"project"`           // 工程名，应为 LumenFolio
	Version          string `json:"version"`           // 固件版本
	ImageID          string `json:"image_id"`          // app_elf_sha256 的 256 位标识，64 个十六进制字符
	RunningPartition string `json:"running_partition"` // 当前运行槽
	BootPartition    string `json:"boot_partition"`    // 下次启动槽
	UpdatePartition  string `json:"update_partition"`  // 可写入的 OTA 槽
	Capacity         int64  `json:"capacity"`          // OTA 槽容量，字节
	Received         int64  `json:"received"`          // 已接收字节数
	Total            int64  `json:"total"`             // 待接收总字节数
	LastError        int    `json:"last_error"`        // 最近错误码

	// 运行时状态，由设备在 OTA 字段之后追加；尚未采到样本时整个字段缺省。
	BatteryMV      *int     `json:"battery_mv,omitempty"`      // VBAT，毫伏
	BatteryPercent *int     `json:"battery_percent,omitempty"` // 按开路电压估算的电量，0–100
	Charge         string   `json:"charge,omitempty"`          // charging / full / idle / unknown
	TemperatureC   *float64 `json:"temperature_c,omitempty"`   // 环境温度，摄氏度
	HumidityPct    *float64 `json:"humidity_pct,omitempty"`    // 相对湿度，百分比
	DeviceID       string   `json:"id,omitempty"`              // 基 MAC；旧固件缺省
}

// otaImage 是从应用镜像头提取的标识信息。
type otaImage struct {
	Project string // 工程名
	Version string // 版本字符串
	ImageID string // 镜像标识的十六进制串
	Size    int64  // 文件字节数
}

// Inspect 读取应用镜像的版本，供用户选择固件时核对。size 是源的总字节数。
// 此步骤仅检查镜像描述，完整校验在设备写入时进行。
func Inspect(source io.ReaderAt, size int64) (version string, err error) {
	image, err := readOTAImage(source, size)
	return image.Version, err
}

// readOTAImage 从应用 .bin 提取工程名、版本、镜像标识和文件大小。
//
// IDF 的应用描述位于首个 segment 开头：24 字节镜像头 + 8 字节 segment 头。
// 完整校验留给设备 SDK；这里仅提取项目/版本等信息供上传校验和重启后的确认使用。
// 文件必须是普通 ESP-IDF 应用镜像（首字节 0xe9、描述魔数 0xabcd5432），
// 且工程为 LumenFolio、版本非空且不含非法 HTTP 头字符。
func readOTAImage(source io.ReaderAt, size int64) (otaImage, error) {
	var prefix [32 + 256]byte
	if size < int64(len(prefix)) {
		return otaImage{}, fmt.Errorf("读取应用镜像头: %w", io.EOF)
	}
	if _, err := source.ReadAt(prefix[:], 0); err != nil {
		return otaImage{}, fmt.Errorf("读取应用镜像头: %w", err)
	}
	if prefix[0] != 0xe9 || binary.LittleEndian.Uint32(prefix[32:36]) != 0xabcd5432 {
		return otaImage{}, errors.New("不是 ESP-IDF 应用镜像；请使用 LumenFolio.bin，不能使用 bootloader 或合并镜像")
	}
	field := func(value []byte) string {
		if index := bytes.IndexByte(value, 0); index >= 0 {
			value = value[:index]
		}
		return string(value)
	}
	image := otaImage{Version: field(prefix[48:80]), Project: field(prefix[80:112]),
		ImageID: hex.EncodeToString(prefix[176:208]), Size: size}
	if image.Project != "LumenFolio" || image.Version == "" {
		return otaImage{}, errors.New("镜像必须属于 LumenFolio，且包含版本信息")
	}
	for _, value := range []byte(image.Version) {
		if value < 32 || value == 127 {
			return otaImage{}, errors.New("镜像版本含有不合法的 HTTP 头字符")
		}
	}
	return image, nil
}

// decodeOTAResponse 解码 OTA HTTP 应答，并关闭响应体。
//
// 非 2xx 返回含响应文本（最多 4 KiB）的错误；JSON 最多读取 8 KiB。
// 状态缺少工程名、版本、运行槽或 64 字符镜像标识时视为不完整并返回错误。
func decodeOTAResponse(response *http.Response) (otaStatus, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return otaStatus{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	var status otaStatus
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&status); err != nil {
		return status, fmt.Errorf("读取 OTA 状态: %w", err)
	}
	if status.Project == "" || status.Version == "" || status.RunningPartition == "" || len(status.ImageID) != 64 {
		return status, errors.New("设备返回了不完整的 OTA 状态")
	}
	return status, nil
}

// otaControlRequest 为状态和确认请求设置超时，蓝牙需要预留重连时间。
func otaControlRequest(ctx context.Context, method, endpoint string) (*http.Request, context.CancelFunc, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	timeout := 3 * time.Second
	if devicehttp.IsBluetooth(request.URL.Hostname()) {
		timeout = devicehttp.BluetoothConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return request.WithContext(ctx), cancel, nil
}

// getOTAStatus 请求 GET base/ota/status 并解析状态。
func getOTAStatus(ctx context.Context, client *http.Client, base string) (otaStatus, error) {
	request, cancel, err := otaControlRequest(ctx, http.MethodGet, base+"/ota/status")
	if err != nil {
		return otaStatus{}, err
	}
	defer cancel()
	response, err := client.Do(request)
	if err != nil {
		return otaStatus{}, err
	}
	return decodeOTAResponse(response)
}

// DeviceID 读取相框的固定身份。旧固件不报 id 时返回空串。
func DeviceID(ctx context.Context, host string, port int) (identity.ID, error) {
	client, base := newOTAClient(host, port)
	defer client.CloseIdleConnections()
	request, cancel, err := otaControlRequest(ctx, http.MethodGet, base+"/ota/status")
	if err != nil {
		return "", err
	}
	defer cancel()
	return identity.Read(request.Context(), client, base+"/ota/status")
}

// DeviceStatus 读取设备的电量、充电和环境状态。还未采样的指标显示为占位符。
func DeviceStatus(ctx context.Context, host string, port int) (string, error) {
	client, base := newOTAClient(host, port)
	defer client.CloseIdleConnections()
	status, err := getOTAStatus(ctx, client, base)
	if err != nil {
		return "", err
	}
	battery, temperature, humidity := "—", "—", "—"
	if status.BatteryPercent != nil {
		battery = fmt.Sprintf("%d%%", *status.BatteryPercent)
	}
	if status.TemperatureC != nil {
		temperature = fmt.Sprintf("%.1f °C", *status.TemperatureC)
	}
	if status.HumidityPct != nil {
		humidity = fmt.Sprintf("%.1f%% RH", *status.HumidityPct)
	}
	charge := "充电状态未知"
	switch status.Charge {
	case "charging":
		charge = "充电中"
	case "full":
		charge = "已充满"
	case "idle":
		charge = "未接充电"
	}
	return fmt.Sprintf("电量 %s · %s · 温度 %s · 湿度 %s", battery, charge, temperature, humidity), nil
}

// confirmOTA 请求 POST base/ota/confirm。
//
// 通过 X-OTA-Partition、X-OTA-Version、X-OTA-Image 头携带目标槽、版本和镜像标识，
// 让设备确认当前运行的新固件。
func confirmOTA(ctx context.Context, client *http.Client, base, partition string, image otaImage) error {
	request, cancel, err := otaControlRequest(ctx, http.MethodPost, base+"/ota/confirm")
	if err != nil {
		return err
	}
	defer cancel()
	request.Header.Set("X-OTA-Partition", partition)
	request.Header.Set("X-OTA-Version", image.Version)
	request.Header.Set("X-OTA-Image", image.ImageID)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_, err = decodeOTAResponse(response)
	return err
}

// waitOTA 每 poll 轮询一次设备状态，直到目标固件在 partition 上启动并确认。
//
// 运行槽、版本、工程和镜像标识与目标一致，且 Ready、PendingVerify 均为 true 时发送确认；
// 确认答复丢失不算失败，下一轮按设备状态重试。上述标识一致、Ready、不再待验证
// 且 Phase 为 idle 时成功返回。设备已回到 idle、启动槽等于运行槽、不再待验证，
// 但目标不匹配时返回固件未运行或已回退的错误；Phase 为 rollback_failed 时立即返回错误。
// ctx 结束时返回结果未确认的错误，并附带最近一次请求错误。
func waitOTA(ctx context.Context, client *http.Client, base string, image otaImage, partition string, poll time.Duration) error {
	var lastError error
	for {
		status, err := getOTAStatus(ctx, client, base)
		if err != nil {
			lastError = err
		} else {
			matches := status.RunningPartition == partition && status.Version == image.Version && status.Project == image.Project && status.ImageID == image.ImageID
			if status.Phase == "rollback_failed" {
				return fmt.Errorf("设备回退失败（错误 %d），请通过 USB 重新烧录 factory", status.LastError)
			}
			if matches && status.Ready && status.PendingVerify {
				// 确认答复丢失时，下一轮通过设备状态判断，避免误报失败。
				lastError = confirmOTA(ctx, client, base, partition, image)
			} else if matches && status.Ready && !status.PendingVerify && status.Phase == "idle" {
				return nil
			} else if status.Phase == "idle" && status.BootPartition == status.RunningPartition && !status.PendingVerify && !matches {
				return fmt.Errorf("未运行目标固件：当前 %s/%s，目标 %s/%s；上传可能中止或新固件已回退",
					status.RunningPartition, status.Version, partition, image.Version)
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastError != nil {
				return fmt.Errorf("等待 OTA 确认结束，结果未确认: %w（最近错误：%v）", ctx.Err(), lastError)
			}
			return fmt.Errorf("等待 OTA 确认结束，结果未确认: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// Progress 是一次 OTA 进度更新。Total 大于 0 时 Sent 表示 HTTP 客户端读取的字节数。
// 设备写入或校验的结果以随后阶段为准。Total 为 0 时 Message 描述当前阶段。
type Progress struct {
	Message     string
	Sent, Total int64
}

// otaProgress 包装上传请求体，统计 HTTP 客户端读取的字节并汇报进度。
//
// 读取进度不代表设备已经收到、写入或验证镜像。closed 在上传结束后由调用方置位，
// 阻止后台继续访问镜像或汇报进度，调用方此时可以关闭本地镜像。
type otaProgress struct {
	mu     sync.Mutex // 同步镜像读取和上传完成，Read 可能由 net/http 的后台 goroutine 调用
	closed bool       // 上传结束后置位，之后不再输出进度
	reader io.Reader  // 应用镜像文件
	report func(Progress)
	total  int64 // 镜像总字节数
	sent   int64 // 已读取字节数
	last   int64 // 上次输出的百分比
}

// Read 实现 io.Reader：读取镜像，较上次输出增加至少 10 个百分点或到达 100% 时汇报进度。
//
// total 必须大于 0，上传结束后读请求体直接返回 EOF。
func (progress *otaProgress) Read(buffer []byte) (int, error) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.closed {
		return 0, io.EOF
	}
	n, err := progress.reader.Read(buffer)
	progress.sent += int64(n)
	percent := progress.sent * 100 / progress.total
	if percent >= progress.last+10 || (percent == 100 && progress.last != 100) {
		progress.report(Progress{Message: fmt.Sprintf("发送 %d%% (%d/%d 字节)", percent, progress.sent, progress.total),
			Sent: progress.sent, Total: progress.total})
		progress.last = percent
	}
	return n, err
}

// uploadOTA 以固定 Content-Length 流式 POST 镜像到 base/ota。
//
// 返回设备状态、上传结果是否不确定（网络错误，或 2xx 应答无法解析）以及错误。
// 设备不接收 chunked，因此必须显式设置 ContentLength；请求使用 Close=true，
// 避免复用可能残留请求体的连接。函数返回后停止进度输出。
func uploadOTA(ctx context.Context, client *http.Client, base string, source io.ReaderAt, image otaImage, report func(Progress)) (otaStatus, bool, error) {
	progress := &otaProgress{reader: io.NewSectionReader(source, 0, image.Size), report: report, total: image.Size}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ota", progress)
	if err != nil {
		return otaStatus{}, false, err
	}
	request.ContentLength = image.Size // 请求体包装后不能依赖 net/http 自动推断；设备不接收 chunked。
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Close = true
	response, err := client.Do(request)
	// 服务端可能提前拒绝请求；Do 返回后，后台请求体 reader 不再写入命令行输出。
	progress.mu.Lock()
	progress.closed = true
	progress.mu.Unlock()
	if err != nil {
		return otaStatus{}, true, err
	}
	definiteRejection := response.StatusCode < 200 || response.StatusCode >= 300
	status, err := decodeOTAResponse(response)
	return status, !definiteRejection && err != nil, err
}

// Upgrade 读取本地应用镜像，完成上传、重启核对和确认。设备确认了目标镜像后才返回成功。
// report 可省略。回调串行执行，可能由 HTTP goroutine 调用，Upgrade 返回后不再调用。
// 调用方须在 Upgrade 期间保持镜像数据可读且不变。取消 ctx 会停止本次客户端请求。
func Upgrade(ctx context.Context, host string, port int, source io.ReaderAt, size int64, report func(Progress)) error {
	host = strings.TrimSpace(host)
	if host == "" || port < 1 || port > 65535 {
		return errors.New("相框地址或 OTA 端口无效")
	}
	uploadTimeout := defaultUploadTimeout
	if devicehttp.IsBluetooth(host) {
		uploadTimeout = devicehttp.BluetoothTransferTimeout
	}
	return upgrade(ctx, host, port, source, size, uploadTimeout, defaultWaitTimeout, report)
}

func newOTAClient(host string, port int) (*http.Client, string) {
	base := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // 私有局域网固件直连设备，不经环境变量指定的代理。
	return &http.Client{Transport: devicehttp.WithBluetooth(transport), CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}, base
}

func upgrade(ctx context.Context, host string, port int, source io.ReaderAt, size int64, uploadTimeout, waitTimeout time.Duration, report func(Progress)) error {
	if report == nil {
		report = func(Progress) {}
	}
	image, err := readOTAImage(source, size)
	if err != nil {
		return err
	}
	client, base := newOTAClient(host, port)
	defer client.CloseIdleConnections()
	status, err := getOTAStatus(ctx, client, base)
	if err != nil {
		return err
	}
	if status.Project != image.Project {
		return fmt.Errorf("设备工程名 %q 与镜像工程名 %q 不一致", status.Project, image.Project)
	}
	partition := status.UpdatePartition
	if status.PendingVerify && status.Version == image.Version && status.ImageID == image.ImageID {
		partition = status.RunningPartition
		report(Progress{Message: "目标版本已启动，继续自动确认。"})
	} else {
		if status.Phase != "idle" || status.PendingVerify {
			return fmt.Errorf("设备尚未就绪或升级中：%s", status.Phase)
		}
		if partition == "" || status.Capacity == 0 {
			return errors.New("设备没有 OTA 槽，需要先有线部署 OTA 引导版本")
		}
		if image.Size > status.Capacity {
			return fmt.Errorf("镜像 %d 字节超过 OTA 槽 %d 字节", image.Size, status.Capacity)
		}
		report(Progress{Message: fmt.Sprintf("上传 %s，%d 字节，目标槽 %s；上传期间请勿断电或刷新屏幕。", image.Version, image.Size, partition)})
		uploadContext, cancel := context.WithTimeout(ctx, uploadTimeout)
		accepted, uncertain, uploadError := uploadOTA(uploadContext, client, base, source, image, report)
		cancel()
		if uploadError != nil {
			if !uncertain || ctx.Err() != nil {
				return uploadError
			}
			report(Progress{Message: fmt.Sprintf("上传答复未收到：%v；重连核对结果，不重复发送镜像。", uploadError)})
		} else if accepted.Phase != "reboot_pending" || accepted.BootPartition != partition {
			return errors.New("设备未返回预期的启动切换状态")
		} else {
			report(Progress{Message: "设备校验完成，等待重启与自动确认。"})
		}
	}
	waitContext, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if err := waitOTA(waitContext, client, base, image, partition, 250*time.Millisecond); err != nil {
		return err
	}
	report(Progress{Message: fmt.Sprintf("OTA 成功：%s 已在 %s 启动并确认。", image.Version, partition)})
	return nil
}

// Run 执行 ota 子命令：status 打印状态 JSON，否则上传镜像并等待确认。
//
// 位置参数为应用 .bin 路径或 status；--http-port 默认 8080，--upload-timeout
// 默认 3 分钟，--wait-timeout 默认 90 秒。上传前校验工程、槽容量和设备空闲状态；
// 若目标版本和镜像标识已在运行槽启动但待确认，则跳过上传直接继续确认。
// 返回进程退出码：0 成功，1 运行期错误，2 用法错误。
func Run(ctx context.Context, host string, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("ota", flag.ContinueOnError)
	flags.SetOutput(errOut)
	port := flags.Int("http-port", 8080, "设备 OTA HTTP 端口")
	uploadTimeout := flags.Duration("upload-timeout", defaultUploadTimeout, "上传总时限")
	waitTimeout := flags.Duration("wait-timeout", defaultWaitTimeout, "等待重启和自动确认的时限")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 || *port < 1 || *port > 65535 || *uploadTimeout <= 0 || *waitTimeout <= 0 {
		fmt.Fprintln(errOut, "用法：LumenFolio --host IP ota [选项] 应用.bin|status")
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(errOut, "OTA:", err); return 1 }
	if flags.Arg(0) == "status" {
		client, base := newOTAClient(host, *port)
		defer client.CloseIdleConnections()
		status, err := getOTAStatus(ctx, client, base)
		if err != nil {
			return fail(err)
		}
		if err := json.NewEncoder(out).Encode(status); err != nil {
			return fail(err)
		}
		return 0
	}
	file, err := os.Open(flags.Arg(0))
	if err != nil {
		return fail(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(errors.New("OTA 输入必须是普通应用 .bin 文件"))
	}
	if err := upgrade(ctx, host, *port, file, info.Size(), *uploadTimeout, *waitTimeout, func(progress Progress) {
		fmt.Fprintln(out, progress.Message)
	}); err != nil {
		return fail(err)
	}
	return 0
}
