//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The Windows private state directory is the only copy of the user's
// monitoring events, assessments, findings and inventory. The rebrand
// deliberately left it as %LOCALAPPDATA%\Radanor\Shield so a pre-rebrand
// installation is opened in place; see the private engineering record.
func TestWindowsDefaultStateDirectoryIsThePreRebrandLocation(t *testing.T) {
	t.Setenv(EnvPrefix+"STATE_DIR", "")
	t.Setenv(EnvPrefix+"RUNTIME_DIR", "")
	t.Setenv(LegacyEnvPrefix+"STATE_DIR", "")
	t.Setenv(LegacyEnvPrefix+"RUNTIME_DIR", "")
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "Radanor", "Shield"); cfg.StateDir != want {
		t.Errorf("state directory = %q, want %q", cfg.StateDir, want)
	}
	if want := filepath.Join(cfg.StateDir, "run"); cfg.RuntimeDir != want {
		t.Errorf("runtime directory = %q, want %q", cfg.RuntimeDir, want)
	}
	if _, err := os.Stat(cfg.StateDir); err == nil {
		t.Fatal("Load created the state directory; it must not touch the filesystem")
	}
}
