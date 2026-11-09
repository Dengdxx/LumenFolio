package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lumenfolio/identity"
	"lumenfolio/ota"
)

// rememberedFrame 只记一台：上次确认过身份的相框，以及它当时的 Wi-Fi 地址。
type rememberedFrame struct {
	ID      identity.ID `json:"id"`
	Address string      `json:"address,omitempty"`
}

func frameMemoryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "LumenFolio", "frame.json"), nil
}

func loadRememberedFrame() rememberedFrame {
	path, err := frameMemoryPath()
	if err != nil {
		return rememberedFrame{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rememberedFrame{}
	}
	var saved rememberedFrame
	if json.Unmarshal(data, &saved) != nil {
		return rememberedFrame{}
	}
	saved.ID = identity.Parse(string(saved.ID))
	saved.Address = strings.TrimSpace(saved.Address)
	if saved.ID == "" {
		return rememberedFrame{}
	}
	return saved
}

func saveRememberedFrame(saved rememberedFrame) {
	if saved.ID == "" {
		return
	}
	path, err := frameMemoryPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	temporary := path + ".tmp"
	if os.WriteFile(temporary, append(data, '\n'), 0o644) != nil {
		return
	}
	_ = os.Rename(temporary, path)
}

// errDifferentFrame 表示这次连上的相框和已记住的不是同一台。
var errDifferentFrame = errors.New("连上的是另一台相框")

// checkFrameIdentity 核对这次连上的是不是上次那台。
//
// 任一侧没有身份时放行：旧固件不报 id，蓝牙地址推不出基 MAC 时也不拦。
// 两侧都有且不同，说明换了一台相框，调用方应断开这次连接。
func (u *ui) checkFrameIdentity(reported, derived identity.ID) error {
	if reported == "" || derived == "" || reported == derived {
		return nil
	}
	return fmt.Errorf("%w（%s），已记住的是 %s", errDifferentFrame, reported, derived)
}

// rememberFrame 在确认身份后记住这台相框。address 为空表示这次没走 Wi-Fi。
func (u *ui) rememberFrame(id identity.ID, address string) {
	if id == "" {
		return
	}
	saved := u.remembered
	if saved.ID == id && (address == "" || saved.Address == address) {
		return
	}
	if address != "" {
		saved.Address = address
	}
	saved.ID = id
	u.remembered = saved
	saveRememberedFrame(saved)
}

// identifyWiFi 读取手填地址对应的相框身份，并和已记住的那台核对。
func (u *ui) identifyWiFi(ctx context.Context, host string) (identity.ID, error) {
	id, err := ota.DeviceID(ctx, host, 8080)
	if err != nil {
		return "", err
	}
	return id, u.checkFrameIdentity(id, u.remembered.ID)
}
