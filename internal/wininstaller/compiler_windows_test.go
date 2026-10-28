//go:build windows

package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runNsisInitializer(t *testing.T, root, cache string, verify bool) (string, error) {
	t.Helper()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; try { & ([scriptblock]::Create([IO.File]::ReadAllText($env:RADANOR_NSIS_INITIALIZER))) -MsysRoot $env:RADANOR_NSIS_ROOT -CacheDirectory $env:RADANOR_NSIS_CACHE -VerifyOnly:($env:RADANOR_NSIS_VERIFY -eq '1') } catch { Write-Output $_.Exception.Message; exit 1 }`)
	flag := "0"
	if verify {
		flag = "1"
	}
	cmd.Env = append(os.Environ(), "RADANOR_NSIS_INITIALIZER="+filepath.Join(repoRoot(t), "validation", "windows", "Initialize-SparrowNsis.ps1"), "RADANOR_NSIS_ROOT="+root, "RADANOR_NSIS_CACHE="+cache, "RADANOR_NSIS_VERIFY="+flag)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestNsisProvisioningRejectsChecksumMismatchBeforeExecution(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	archive := filepath.Join(cache, "mingw-w64-ucrt-x86_64-nsis-3.13-2-any.pkg.tar.zst")
	if err := os.WriteFile(archive, []byte("not a trusted package"), 0600); err != nil {
		t.Fatal(err)
	}
	// No pacman/compiler exists. Reaching either program would be a different
	// failure; checksum refusal must occur first, without downloading/executing.
	out, err := runNsisInitializer(t, root, cache, false)
	if err == nil || !strings.Contains(out, "NSIS package checksum mismatch; refusing installation") {
		t.Fatalf("unverified package was not refused before execution: %v %s", err, out)
	}
}

func TestNsisProvisioningRejectsBrokenRevisionWithSameUpstreamVersion(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"usr/bin", "ucrt64/bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	buildTestProgram(t, filepath.Join(root, "ucrt64", "bin"), "makensis", `package main
import "fmt"
func main() { fmt.Print("v3.13\r\n") }`)
	buildTestProgram(t, filepath.Join(root, "usr", "bin"), "pacman", `package main
import ("fmt"; "os")
func main() {
 for _, arg := range os.Args[1:] { if arg == "-Q" { fmt.Println("mingw-w64-ucrt-x86_64-nsis 3.13-1"); return } }
 fmt.Println("/ucrt64/bin/makensis.exe is owned by mingw-w64-ucrt-x86_64-nsis 3.13-1")
}`)
	out, err := runNsisInitializer(t, root, t.TempDir(), true)
	if err == nil || !strings.Contains(out, "NSIS package mismatch") || !strings.Contains(out, "3.13-1") || !strings.Contains(out, "NSIS /VERSION: [v3.13]") || !strings.Contains(out, filepath.Join(root, "ucrt64", "bin", "makensis.exe")) {
		t.Fatalf("broken package revision was not diagnosed/refused: %v %s", err, out)
	}
}
