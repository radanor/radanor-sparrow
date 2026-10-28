//go:build !windows

package privatefs

import "os"

func ProtectDir(path string) error  { return os.Chmod(path, 0700) }
func ProtectFile(path string) error { return os.Chmod(path, 0600) }
