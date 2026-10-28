//go:build !linux && !windows

package main

import "fmt"

func openBrowser(url string) error {
	return fmt.Errorf("browser launch is unsupported on this platform")
}
