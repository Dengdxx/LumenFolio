//go:build !windows

package desktop

func systemPrefersDark() bool {
	return true
}
