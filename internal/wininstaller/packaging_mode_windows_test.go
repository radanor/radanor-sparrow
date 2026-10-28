//go:build windows

package wininstaller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compile the exact production mode include in an unmistakable simulation.
// No release BUILD-INFO, legal approval or release artifact is fabricated.
func TestNativeCompilerPackagingModes(t *testing.T) {
	compiler := os.Getenv("MAKENSIS")
	if compiler == "" {
		t.Skip("explicit native NSIS compiler opt-in")
	}
	for _, tc := range []struct{ name, mode, variant, extra, want string }{
		{"development", "development", "development", "", ""},
		{"community simulation", "community-unsigned", "release", "", ""},
		{"default release remains signed", "", "release", "", "requires the reviewed uninstaller signer"},
		{"explicit signed requires signer", "signed-release", "release", "", "requires the reviewed uninstaller signer"},
		{"community hook refused", "community-unsigned", "release", `!define SIGNING_SCRIPT "not-a-signer.ps1"`, "rejects signing hooks"},
		{"community host refused", "community-unsigned", "release", `!define SIGNING_HOST "not-a-host.exe"`, "rejects signing hooks"},
		{"development release identity refused", "development", "release", "", "requires development identity"},
		{"community development identity refused", "community-unsigned", "development", "", "requires release identity"},
		{"unknown mode", "unsigned", "release", "", "Unknown installer packaging mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "SIMULATION-NOT-A-RELEASE.exe")
			define := ""
			if tc.mode != "" {
				define = fmt.Sprintf("!define PACKAGING_MODE %q\n", tc.mode)
			}
			source := fmt.Sprintf("Unicode true\nRequestExecutionLevel user\n!define VARIANT %q\n%s%s\n!include %q\nName \"TEST ONLY mode simulation\"\nOutFile %q\nSection\nWriteUninstaller \"$EXEDIR\\Uninstall.exe\"\nSectionEnd\nSection \"Uninstall\"\nSectionEnd\n", tc.variant, define, tc.extra, filepath.Join(repoRoot(t), "installer/windows/packaging-mode.nsh"), output)
			// NSIS quoted paths use native separators, not Go's escaped string form.
			source = strings.ReplaceAll(source, `\\`, `\`)
			file := filepath.Join(dir, "simulation.nsi")
			if err := os.WriteFile(file, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(compiler, "-V2", file).CombinedOutput()
			if tc.want != "" {
				if err == nil || !strings.Contains(string(out), tc.want) {
					t.Fatalf("missing refusal %q: %v %s", tc.want, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("simulation compile: %v %s", err, out)
			}
			tool := os.Getenv("RADANOR_7ZIP")
			if tool == "" {
				t.Fatal("RADANOR_7ZIP required for actual uninstaller proof")
			}
			extract := filepath.Join(dir, "extracted")
			if out, err = exec.Command(tool, "x", "-y", "-o"+extract, output).CombinedOutput(); err != nil {
				t.Fatalf("simulation extraction: %v %s", err, out)
			}
			var uninstallers []string
			if err = filepath.WalkDir(extract, func(path string, e os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !e.IsDir() && e.Name() == "Uninstall.exe" {
					uninstallers = append(uninstallers, path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(uninstallers) != 1 {
				t.Fatalf("embedded uninstallers: %v", uninstallers)
			}
			for _, artifact := range []string{output, uninstallers[0]} {
				cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; Import-Module (Join-Path $PSHOME 'Modules/Microsoft.PowerShell.Security/Microsoft.PowerShell.Security.psd1'); $s=Get-AuthenticodeSignature -LiteralPath $env:RADANOR_MODE_SIMULATION; if($s.Status -ne 'NotSigned' -or $s.SignerCertificate -or $s.TimeStamperCertificate){throw 'Simulation unexpectedly signed'}; 'NotSigned: no signer or timestamp'`)
				cmd.Env = append(os.Environ(), "RADANOR_MODE_SIMULATION="+artifact)
				if out, err = cmd.CombinedOutput(); err != nil {
					t.Fatalf("unsigned simulation: %v %s", err, out)
				}
			}
			t.Log("Actual NSIS simulation setup and embedded uninstaller: NotSigned; no real release approval")
		})
	}
}

func TestUnsignedInstallerPayloadVerification(t *testing.T) {
	if os.Getenv("RADANOR_UNSIGNED_INSTALLER_TEST") != "1" {
		t.Skip("explicit unsigned installer acceptance; signed release artifacts use their own verifier")
	}
	setup, binary := os.Getenv("RADANOR_SETUP_EXE"), os.Getenv("RADANOR_PORTABLE_EXE")
	if setup == "" || binary == "" {
		t.Skip("unsigned installer artifact opt-in")
	}
	for _, host := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(host, func(t *testing.T) {
			resolved, err := exec.LookPath(host)
			if err != nil {
				t.Skipf("host unavailable: %s", host)
			}
			run := func(portable string) ([]byte, error) {
				cmd := exec.Command(resolved, "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; try { & ([scriptblock]::Create([IO.File]::ReadAllText($env:RADANOR_UNSIGNED_HELPER))) -Setup $env:RADANOR_SETUP_EXE -PortableDirectory $env:RADANOR_UNSIGNED_PORTABLE -SevenZip $env:RADANOR_7ZIP } catch { Write-Output $_.Exception.Message; exit 1 }`)
				cmd.Env = append(os.Environ(), "RADANOR_UNSIGNED_HELPER="+filepath.Join(repoRoot(t), "validation/windows/Assert-SparrowUnsignedWindows.ps1"), "RADANOR_UNSIGNED_PORTABLE="+portable)
				return cmd.CombinedOutput()
			}
			if out, err := run(filepath.Dir(binary)); err != nil {
				t.Fatalf("unsigned exact payload: %v %s", err, out)
			}
			wrong := t.TempDir()
			for _, name := range []string{PortableName, BuildInfoName, "LICENSE", "THIRD_PARTY_NOTICES.md"} {
				b, err := os.ReadFile(filepath.Join(filepath.Dir(binary), name))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(wrong, name), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(wrong, "LICENSE"), []byte("NEGATIVE TEST: deliberately mismatched input, not an approval"), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := run(wrong); err == nil || !strings.Contains(string(out), "payload mismatch: LICENSE") {
				t.Fatalf("mismatched payload accepted: %v %s", err, out)
			}
		})
	}
}
