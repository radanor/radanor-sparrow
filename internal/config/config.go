package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/radanor/radanor-shield/internal/privatefs"
)

type Config struct {
	StateDir    string
	RuntimeDir  string
	HTTPPort    int
	DevAssets   string
	OpenBrowser bool
}

func Load() (Config, error) {
	state, err := stateDir()
	if err != nil {
		return Config{}, err
	}
	run, err := runtimeDir(state)
	if err != nil {
		return Config{}, err
	}
	return Config{StateDir: state, RuntimeDir: run, HTTPPort: 0, DevAssets: Env("DEV_ASSETS"), OpenBrowser: true}, nil
}

func EnsurePrivateDirs(c Config) error {
	for _, dir := range []string{c.StateDir, c.RuntimeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create private directory: %w", err)
		}
		if err := privatefs.ProtectDir(dir); err != nil {
			return fmt.Errorf("protect private directory: %w", err)
		}
	}
	return nil
}

// The private state directory keeps its pre-rebrand name on purpose. It holds
// the only copy of the user's monitoring events, assessments, findings and
// inventory, and moving it would either orphan that data or require an atomic
// migration that has not been validated on a Windows host. The directory is not
// surfaced in the dashboard, so the old name is invisible in normal use. See
// the private engineering record.
func stateDir() (string, error) {
	if v := Env("STATE_DIR"); v != "" {
		return filepath.Clean(v), nil
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is unavailable")
		}
		return filepath.Join(base, "Radanor", "Shield"), nil
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "radanor-shield"), nil
}

// runtimeDir holds the instance lock files, the launch secret and the dashboard
// discovery record. It keeps its pre-rebrand name for the same reason as
// stateDir: an installation that is already running must be found by the lock
// the new build tries to take, not shadowed by a second one.
func runtimeDir(state string) (string, error) {
	if v := Env("RUNTIME_DIR"); v != "" {
		return filepath.Clean(v), nil
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(state, "run"), nil
	}
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		return filepath.Join(base, "radanor-shield"), nil
	}
	return filepath.Join(state, "run"), nil
}
