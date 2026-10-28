package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The rebrand renamed the environment variables from RADANOR_SHIELD_* to
// RADANOR_SPARROW_*. These tests pin the promise made in
// the private engineering record: an existing installation's launcher,
// service unit or shell profile keeps selecting the same private state
// directory, so the user's monitoring events, assessments and inventory are
// found rather than replaced by an empty database.

func TestEnvPrefersTheCurrentNameAndFallsBackToTheLegacyOne(t *testing.T) {
	t.Setenv(EnvPrefix+"STATE_DIR", "")
	t.Setenv(LegacyEnvPrefix+"STATE_DIR", "/legacy/state")
	if got := Env("STATE_DIR"); got != "/legacy/state" {
		t.Errorf("Env with only the legacy name set = %q, want the legacy value", got)
	}
	t.Setenv(EnvPrefix+"STATE_DIR", "/current/state")
	if got := Env("STATE_DIR"); got != "/current/state" {
		t.Errorf("Env with both names set = %q, want the current name to win", got)
	}
}

func TestALegacyInstallationKeepsItsStateAndRuntimeDirectories(t *testing.T) {
	root := t.TempDir()
	legacyState := filepath.Join(root, "pre-rebrand-state")
	legacyRuntime := filepath.Join(root, "pre-rebrand-run")
	// Only the pre-rebrand names are set, exactly as an existing launcher does.
	t.Setenv(EnvPrefix+"STATE_DIR", "")
	t.Setenv(EnvPrefix+"RUNTIME_DIR", "")
	t.Setenv(LegacyEnvPrefix+"STATE_DIR", legacyState)
	t.Setenv(LegacyEnvPrefix+"RUNTIME_DIR", legacyRuntime)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != legacyState {
		t.Errorf("state directory = %q, want the legacy override %q", cfg.StateDir, legacyState)
	}
	if cfg.RuntimeDir != legacyRuntime {
		t.Errorf("runtime directory = %q, want the legacy override %q", cfg.RuntimeDir, legacyRuntime)
	}
	// The core opens exactly this file, so an unselected override would mean an
	// empty database beside the old one instead of the user's real history.
	if err := EnsurePrivateDirs(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyState); err != nil {
		t.Errorf("legacy state directory was not used: %v", err)
	}
}

func TestLimitEnvironmentVariablesAreAlsoReadFromTheLegacyNames(t *testing.T) {
	t.Setenv(EnvPrefix+"RETENTION_DAYS", "")
	t.Setenv(LegacyEnvPrefix+"RETENTION_DAYS", "11")
	if got := Env("RETENTION_DAYS"); got != "11" {
		t.Errorf("legacy retention override = %q, want %q", got, "11")
	}
}

// TestDefaultDirectoriesKeepTheirPreRebrandNames asserts the default location
// itself did not move. An existing profile is used in place; no migration runs.
func TestDefaultDirectoriesKeepTheirPreRebrandNames(t *testing.T) {
	t.Setenv(EnvPrefix+"STATE_DIR", "")
	t.Setenv(EnvPrefix+"RUNTIME_DIR", "")
	t.Setenv(LegacyEnvPrefix+"STATE_DIR", "")
	t.Setenv(LegacyEnvPrefix+"RUNTIME_DIR", "")
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	want := filepath.Join("Radanor", "Shield")
	if runtime.GOOS != "windows" {
		want = "radanor-shield"
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.Clean(cfg.StateDir), want) {
		t.Errorf("default state directory = %q, want a path ending in %q", cfg.StateDir, want)
	}
	if strings.Contains(strings.ToLower(cfg.StateDir), "sparrow") {
		t.Errorf("default state directory was renamed to %q, which would orphan existing data", cfg.StateDir)
	}
}
