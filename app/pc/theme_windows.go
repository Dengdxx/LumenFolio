package main

import "golang.org/x/sys/windows/registry"

// systemPrefersDark 读取 Windows 的应用主题偏好。设置不可用时沿用深色界面。
func systemPrefersDark() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer key.Close()
	light, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err != nil || light == 0
}
