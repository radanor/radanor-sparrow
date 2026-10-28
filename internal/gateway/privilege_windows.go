//go:build windows

package gateway

import "golang.org/x/sys/windows"

func validateNotElevated() error {
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() {
		return windows.ERROR_ACCESS_DENIED
	}
	return nil
}
