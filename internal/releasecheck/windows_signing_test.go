package releasecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Preserve the actual publishing boundary independently of the signing helper:
// even a working primitive must not omit a stage or let CI skip its acceptance.
func TestWindowsReleaseSigningCoversEmbeddedUninstallerBeforeUpload(t *testing.T) {
	workflow := releaseWorkflow(t)
	ordered := []string{
		"-Path dist/windows/RadanorSparrow.exe -Kind Portable",
		"build-windows-installer.sh --release",
		"-Path $env:RADANOR_SETUP_EXE -Kind Setup",
		"name: Verify final Windows Authenticode signatures and checksums",
		"./validation/windows/Assert-SparrowInstallerSigning.ps1",
		"write-windows-release-checksums.sh",
		"name: Upload Windows release",
	}
	at := 0
	for _, stage := range ordered {
		next := strings.Index(workflow[at:], stage)
		if next < 0 {
			t.Fatalf("missing or reordered Windows signing stage %q", stage)
		}
		at += next + len(stage)
	}
	for _, policy := range []string{"vars.RADANOR_SIGNTOOL_SHA256", "vars.RADANOR_TIMESTAMP_THUMBPRINT", "vars.RADANOR_TIMESTAMP_POLICY_OID"} {
		if !strings.Contains(workflow, policy) {
			t.Errorf("missing protected signing policy %s", policy)
		}
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"powershell.exe", "pwsh.exe"} {
		if !strings.Contains(string(raw), "'TestWindowsAuthenticodeDisposableAcceptance/"+host+"'") {
			t.Errorf("CI must require actual signing acceptance PASS for %s", host)
		}
	}
}
