//go:build !windows

package gateway

import (
	"errors"
	"os"
)

func validateNotElevated() error {
	if os.Geteuid() == 0 {
		return errors.New("the UI gateway refuses to run as root")
	}
	return nil
}
