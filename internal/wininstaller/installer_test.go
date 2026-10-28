package wininstaller

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "0123456789012345678901234567890123456789"
const testSum = "0123456789012345678901234567890123456789012345678901234567890123"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func TestInstallerSafetyContract(t *testing.T) {
	text, err := ReadScript(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RequestExecutionLevel user", "SetShellVarContext current", "CreateMutexW", "BUILD_SEQUENCE", "VersionCompare", "MoveFileExW", "DeleteGeneration", "PRODUCT_MARKER"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"RMDir /r", "HKLM", "TerminateProcess", "taskkill", "monitoring.db", `Radanor\Shield`} {
		if strings.Contains(text, bad) {
			t.Errorf("unsafe installer operation: %s", bad)
		}
	}
}

func TestPortableExecutableCarriesTheBuildVersion(t *testing.T) {
	binary := os.Getenv("RADANOR_PORTABLE_EXE")
	if binary == "" {
		t.Skip("portable packaging opt-in")
	}
	info, err := ReadBuildInfo(filepath.Dir(binary))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = info.VerifyPortable(binary); err != nil {
		t.Fatal(err)
	}
	source := os.Getenv("RADANOR_PACKAGE_SOURCE_DIR")
	if source == "" {
		source = repoRoot(t)
	}
	if err = info.VerifyCommit(source); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedInstallerPayload(t *testing.T) {
	setup := os.Getenv("RADANOR_SETUP_EXE")
	binary := os.Getenv("RADANOR_PORTABLE_EXE")
	if setup == "" || binary == "" {
		t.Skip("installer artifact opt-in")
	}
	tool := os.Getenv("RADANOR_7ZIP")
	if tool == "" {
		tool = "7z"
	}
	dir := t.TempDir()
	cmd := exec.Command(tool, "x", "-y", "-o"+dir, setup)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer extraction: %v %s", err, out)
	}
	for _, name := range []string{PortableName, "LICENSE", "THIRD_PARTY_NOTICES.md", BuildInfoName} {
		want, err := os.ReadFile(filepath.Join(filepath.Dir(binary), name))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && entry.Name() == name {
				got, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if bytes.Equal(got, want) {
					found = true
				}
			}
			return nil
		})
		if err != nil || !found {
			t.Fatalf("installer lacks exact %s payload: %v", name, err)
		}
	}
}

func TestBuildRecordRejectsDuplicateAndMissingIdentity(t *testing.T) {
	if _, err := ParseBuildInfo([]byte("version=0.1.0-dev\nversion=0.2.0-dev\n")); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err := ParseBuildInfo([]byte("version=0.1.0-dev\n")); err == nil {
		t.Fatal("incomplete identity accepted")
	}
}
