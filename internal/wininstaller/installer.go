// Package wininstaller validates the Windows installer that
// scripts/build-windows-installer.sh produces.
//
// The installer is an NSIS executable, so its structure is checked two ways. The
// script is inspected for the properties that must not regress — per-user
// installation, no elevation, no process termination, no access to the user's
// private state directory — and the built artifact is inspected as a PE file and
// as an archive, including a byte comparison of the executable it carries with
// the portable build the distribution ships.
//
// Script checks always run. Artifact checks need a built installer and extract
// it with 7-Zip, so they skip unless RADANOR_SETUP_EXE names one; the CI job
// sets it after building.
package wininstaller

import (
	"os"
	"path/filepath"
)

const (
	// ScriptRelPath is the NSIS source, relative to the repository root.
	ScriptRelPath = "installer/windows/radanor-sparrow.nsi"
	// PortableName is the executable every distribution form carries.
	PortableName = "RadanorSparrow.exe"
	// InstallerPrefix and InstallerSuffix build the artifact file name, which
	// carries the full version so a development installer cannot be mistaken
	// for a release one.
	InstallerPrefix = "RadanorSparrow-"
	InstallerSuffix = "-setup.exe"
)

// ScriptPath is the installer script for a repository root.
func ScriptPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(ScriptRelPath))
}

// ReadScript returns the NSIS source.
func ReadScript(root string) (string, error) {
	data, err := os.ReadFile(ScriptPath(root))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// InstallerName is the artifact name for a version, matching the build script.
func InstallerName(version string) string {
	return InstallerPrefix + version + InstallerSuffix
}
