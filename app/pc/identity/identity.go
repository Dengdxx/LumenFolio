// Package identity 认出一台相框。
//
// 身份是芯片出厂烧定的基 MAC。Wi-Fi STA 地址等于它，蓝牙公开地址是它加 2，
// 所以两条链路报出的是同一台。旧固件不报 id，返回空串，调用方按没有身份处理。
package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ID 是相框的固定身份，十二个小写十六进制字符，冒号分隔。
type ID string

// Parse 接受大小写混用的冒号分隔 MAC，统一成小写。空串和格式不对都返回空。
func Parse(text string) ID {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) != 6 {
		return ""
	}
	var mac [6]byte
	for index, part := range parts {
		if len(part) != 2 {
			return ""
		}
		value, err := strconv.ParseUint(part, 16, 8)
		if err != nil {
			return ""
		}
		mac[index] = byte(value)
	}
	return format(mac)
}

// FromBluetooth 由蓝牙公开地址推出基 MAC。
//
// 当前固件用四个通用 MAC：蓝牙地址是基 MAC 加 2，进位只发生在最低一个字节。
// 减法借位或地址不是公开单播地址时返回空，不把猜测当成身份。
func FromBluetooth(address string) ID {
	mac := Parse(address)
	if mac == "" {
		return ""
	}
	var bytes [6]byte
	for index, part := range strings.Split(string(mac), ":") {
		value, _ := strconv.ParseUint(part, 16, 8)
		bytes[index] = byte(value)
	}
	if bytes[0]&0x01 != 0 || bytes[5] < 2 {
		return ""
	}
	bytes[5] -= 2
	return format(bytes)
}

func format(mac [6]byte) ID {
	return ID(fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]))
}

// Read 从 GET /ota/status 取出相框身份。
//
// 旧固件没有 id 字段时返回空串和空错误。状态本身读不出来才返回错误。
func Read(ctx context.Context, client *http.Client, endpoint string) (ID, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("读取相框身份失败：HTTP %d", response.StatusCode)
	}
	var status struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&status); err != nil {
		return "", fmt.Errorf("读取相框身份失败：%w", err)
	}
	id := Parse(status.ID)
	if status.ID != "" && id == "" {
		return "", errors.New("相框返回了无法识别的身份")
	}
	return id, nil
}
