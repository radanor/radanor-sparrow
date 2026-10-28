//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBrowserLifetimeDoesNotBlockApplicationLifecycle(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "browser.pid")
	t.Setenv("RADANOR_BROWSER_FIXTURE_PID", pidFile)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\necho $$ > \"$RADANOR_BROWSER_FIXTURE_PID\"\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		value, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(value)))
			if err == nil {
				process, err := os.FindProcess(pid)
				if err == nil {
					_ = process.Kill()
				}
			}
		}
	})
	started := time.Now()
	if err := openBrowser("http://127.0.0.1:12345/"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("browser lifetime blocked application startup/shutdown")
	}
}
