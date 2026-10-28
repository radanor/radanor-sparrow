//go:build windows

// gpg-msys is a test-runner path adapter for Git for Windows' MSYS GnuPG.
// Build it as an isolated gpg.exe ahead of Git on a test terminal's PATH.
// It does not emulate GnuPG, alter arguments other than absolute path syntax,
// intercept key material, or change the underlying command's exit status.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func posixPath(path string) string {
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) {
		return "/" + strings.ToLower(path[:1]) + strings.ReplaceAll(path[2:], `\`, "/")
	}
	if strings.HasPrefix(path, `\\`) {
		return strings.ReplaceAll(path, `\`, "/")
	}
	return path
}

func main() {
	actual := os.Getenv("RADANOR_TEST_MSYS_GPG")
	home := os.Getenv("GNUPGHOME")
	if !filepath.IsAbs(actual) || home == "" {
		fmt.Fprintln(os.Stderr, "MSYS GnuPG test adapter requires an absolute RADANOR_TEST_MSYS_GPG and explicit isolated GNUPGHOME")
		os.Exit(2)
	}
	self, _ := os.Executable()
	if strings.EqualFold(filepath.Clean(self), filepath.Clean(actual)) {
		fmt.Fprintln(os.Stderr, "MSYS GnuPG test adapter cannot invoke itself")
		os.Exit(2)
	}
	args := append([]string(nil), os.Args[1:]...)
	for index, arg := range args {
		args[index] = posixPath(arg)
	}
	_ = os.Setenv("GNUPGHOME", posixPath(home)) // this adapter process only
	command := exec.Command(actual, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			os.Exit(failure.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "cannot start configured MSYS GnuPG:", err)
		os.Exit(2)
	}
}
