//go:build windows

package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsAuthenticodeDisposableAcceptance(t *testing.T) {
	if os.Getenv("RADANOR_TEST_SIGNTOOL") == "" {
		t.Skip("explicit SDK SignTool/disposable certificate acceptance opt-in")
	}
	for _, host := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(host, func(t *testing.T) {
			resolved, err := exec.LookPath(host)
			if err != nil {
				t.Skipf("PowerShell host unavailable: %s", host)
			}
			cmd := exec.Command(resolved, "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; try { $root=Join-Path $env:RADANOR_SIGNING_TEST_REPO 'validation/windows'; $body=[IO.File]::ReadAllText((Join-Path $root 'Test-SparrowAuthenticode.ps1')).Replace('$PSScriptRoot',("'"+$root.Replace("'","''")+"'")); & ([scriptblock]::Create($body)) -SignTool $env:RADANOR_TEST_SIGNTOOL -UnsignedExecutable $env:RADANOR_SPARROW_TEST_BINARY } catch { Write-Output $_.Exception.ToString(); Write-Output $_.ScriptStackTrace; exit 1 }`)
			cmd.Env = append(os.Environ(), "RADANOR_SIGNING_TEST_REPO="+repoRoot(t))
			out, err := cmd.CombinedOutput()
			t.Log(string(out))
			if err != nil {
				t.Fatalf("disposable Windows signing acceptance: %v (helper %s)", err, filepath.Join(repoRoot(t), "validation/windows/Test-SparrowAuthenticode.ps1"))
			}
		})
	}
}

func TestInstallerSigningModeGuards(t *testing.T) {
	compiler := os.Getenv("MAKENSIS")
	if compiler == "" {
		t.Skip("explicit NSIS compiler opt-in")
	}
	for _, test := range []struct{ name, variant, hook, want string }{
		{"release missing hook", "release", "", "Release installer requires the reviewed uninstaller signer"},
		{"development production hook", "development", "unapproved.ps1", "Development installer must not use the production signer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"-V2", "-DVARIANT=" + test.variant}
			if test.hook != "" {
				args = append(args, "-DSIGNING_SCRIPT="+test.hook)
			}
			args = append(args, ScriptPath(repoRoot(t)))
			out, err := exec.Command(compiler, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(out), test.want) {
				t.Fatalf("installer mode guard: %v %s", err, out)
			}
		})
	}
}
