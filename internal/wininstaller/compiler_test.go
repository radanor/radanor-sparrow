package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompilerValidationRejectsWrongVersionAndFailedProbe(t *testing.T) {
	shell := bashForTests(t)
	compiler := buildTestProgram(t, t.TempDir(), "makensis", `package main
import ("fmt"; "os")
func main() {
 fmt.Print(os.Getenv("RADANOR_FAKE_NSIS_VERSION"))
 if os.Getenv("RADANOR_FAKE_NSIS_FAILURE") == "1" { os.Exit(1) }
}`)
	for _, test := range []struct{ name, output, failure, want string }{
		{"older package", "v3.12\r\n", "0", "Reviewed NSIS 3.13 is required"},
		{"unreviewed newer compiler", "v3.14", "0", "Reviewed NSIS 3.13 is required"},
		{"extra output", "v3.13\nunexpected", "0", "Reviewed NSIS 3.13 is required"},
		{"failed probe", "v3.13", "1", "NSIS version query failed"},
		// A successful CRLF probe must reach the real missing-payload guard.
		{"supported CRLF", "v3.13\r\n", "0", "Missing "},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(shell, filepath.Join(repoRoot(t), "scripts", "build-windows-installer.sh"), "--portable-dir", t.TempDir())
			cmd.Env = append(os.Environ(), "MAKENSIS="+filepath.ToSlash(compiler), "RADANOR_FAKE_NSIS_VERSION="+test.output, "RADANOR_FAKE_NSIS_FAILURE="+test.failure)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), test.want) || !strings.Contains(string(out), test.output[:5]) {
				t.Fatalf("unexpected compiler validation: error=%v output=%s", err, out)
			}
			if !strings.Contains(string(out), filepath.ToSlash(compiler)) {
				t.Fatal("compiler diagnostic omitted the selected executable path")
			}
		})
	}
}
