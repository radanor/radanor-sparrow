//go:build linux

package appimage

// These tests cover the two supply-chain properties the installer gained in P6.1:
// a detached signature is checked before the image's runtime is executed at all,
// and an upgrade either replaces the installation completely or leaves the
// previous one — and its database — untouched.
//
// They use a stand-in image rather than the built AppImage so that an A→B upgrade
// can be expressed without building two artifacts, and so that a failed install can
// be forced at a chosen point. The real artifact is still exercised by the tests in
// install_test.go when RADANOR_APPIMAGE is set.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeImageOptions struct {
	// pause is how long the stand-in "runtime" waits after announcing that it was
	// started, which gives a test a window in which to interrupt it.
	pause time.Duration
	// marker is written as soon as the runtime runs, so a test can prove whether
	// the image was executed before or after a check.
	marker string
	// missingIcon omits the 48 px icon, which makes the installer refuse after it
	// has unpacked the image but before it replaces anything.
	missingIcon bool
}

// writeFakeImage writes a stand-in AppImage: a script that emulates only the one
// runtime call the installer makes (--appimage-extract) and produces the payload
// layout a real image carries.
func writeFakeImage(t *testing.T, dir, version, variant string, options fakeImageOptions) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("RadanorSparrow-%s-x86_64.AppImage", version))

	sizes := "16 32 48 128 256"
	if options.missingIcon {
		sizes = "16 32"
	}
	marker := ""
	if options.marker != "" {
		marker = fmt.Sprintf("printf 'started\\n' > '%s'\n", options.marker)
	}
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "${1:-}" != "--appimage-extract" ]; then
  echo "the stand-in AppImage supports only --appimage-extract" >&2
  exit 2
fi
%sif [ %d -gt 0 ]; then sleep %d; fi
root="$PWD/squashfs-root"
mkdir -p "$root/usr/share/radanor-sparrow" "$root/usr/share/doc/radanor-sparrow"
printf 'version=%%s\nvariant=%%s\n' > "$root/usr/share/radanor-sparrow/BUILD-INFO"
printf 'The Business Source License 1.1\n' > "$root/usr/share/doc/radanor-sparrow/LICENSE"
printf '# Third-Party Notices\n' > "$root/usr/share/doc/radanor-sparrow/THIRD_PARTY_NOTICES.md"
for size in %s; do
  mkdir -p "$root/usr/share/icons/hicolor/${size}x${size}/apps"
  printf 'png' > "$root/usr/share/icons/hicolor/${size}x${size}/apps/radanor-sparrow.png"
done
`, marker, int64(options.pause/time.Second), int64(options.pause/time.Second), sizes)
	// The version and variant are data, not format verbs.
	script = strings.Replace(script, "printf 'version=%s\\nvariant=%s\\n'",
		fmt.Sprintf("printf 'version=%s\\nvariant=%s\\n'", version, variant), 1)

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// gpgHome creates an isolated GnuPG home for a throwaway test key. A Linux host
// without gpg cannot run these tests at all: skipping them would drop the coverage
// silently in CI, so on CI it is a failure and only a local box may skip.
func gpgHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("gpg is not installed, so the installer's signature checks cannot be exercised here: %v", err)
		}
		t.Skip("gpg is not installed, so signature verification cannot be exercised here")
	}
	home := filepath.Join(t.TempDir(), "gnupg")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// testKey generates a throwaway signing key and returns its fingerprint.
func testKey(t *testing.T, home, uid string) string {
	t.Helper()
	generate := exec.Command("gpg", "--batch", "--passphrase", "", "--quiet", "--quick-gen-key", uid, "ed25519", "sign", "0")
	generate.Env = append(os.Environ(), "GNUPGHOME="+home)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Skipf("gpg cannot generate a throwaway key here (%v): %s", err, output)
	}
	list := exec.Command("gpg", "--batch", "--with-colons", "--list-secret-keys")
	list.Env = append(os.Environ(), "GNUPGHOME="+home)
	listed, err := list.Output()
	if err != nil {
		t.Fatalf("listing the test key failed: %v", err)
	}
	for _, line := range strings.Split(string(listed), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			return strings.Split(line, ":")[9]
		}
	}
	t.Fatal("the throwaway test key has no fingerprint")
	return ""
}

// signImage signs path with the key in home and returns the signature and public
// key files.
func signImage(t *testing.T, home, fingerprint, path string) (signature, publicKey string) {
	t.Helper()
	sign := exec.Command("gpg", "--batch", "--yes", "--armor", "--detach-sign",
		"--local-user", fingerprint, "--output", path+".asc", path)
	sign.Env = append(os.Environ(), "GNUPGHOME="+home)
	if output, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("signing the stand-in image failed: %v\n%s", err, output)
	}
	export := exec.Command("gpg", "--batch", "--armor", "--export", fingerprint)
	export.Env = append(os.Environ(), "GNUPGHOME="+home)
	armored, err := export.Output()
	if err != nil {
		t.Fatalf("exporting the test public key failed: %v", err)
	}
	publicKey = filepath.Join(t.TempDir(), "release-key.asc")
	if err := os.WriteFile(publicKey, armored, 0o644); err != nil {
		t.Fatal(err)
	}
	return path + ".asc", publicKey
}

const installedImage = ".local/bin/radanor-sparrow.AppImage"

// monitoringHistory writes a database the way an installed user would have one.
func monitoringHistory(t *testing.T, home string) string {
	t.Helper()
	state := filepath.Join(home, ".local", "state", "radanor-shield")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "monitoring.db")
	if err := os.WriteFile(path, []byte("existing monitoring history"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// noStagingDirectories asserts that a failed or interrupted install cleaned up
// after itself inside the bin directory.
func noStagingDirectories(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, ".local", "bin"))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".radanor-install.") {
			t.Errorf("a staging directory was left behind: %s", entry.Name())
		}
	}
}

func installScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("install script: %v", err)
	}
	return script
}

func TestInstallVerifiesASignatureBeforeExtracting(t *testing.T) {
	home := freshHome(t)
	marker := filepath.Join(t.TempDir(), "runtime-ran")
	image := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{marker: marker})
	keyHome := gpgHome(t)
	fingerprint := testKey(t, keyHome, "Install Test <install-test@example.invalid>")
	signature, publicKey := signImage(t, keyHome, fingerprint, image)

	output, err := runScript(t, home, installScript(t),
		"--signature", signature, "--public-key", publicKey, "--fingerprint", fingerprint, image)
	if err != nil {
		t.Fatalf("installing a signed image failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the installer did not run the image runtime for a verified image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, installedImage)); err != nil {
		t.Errorf("the verified image was not installed: %v", err)
	}
}

func TestInstallRefusesATamperedImageBeforeExtracting(t *testing.T) {
	home := freshHome(t)
	marker := filepath.Join(t.TempDir(), "runtime-ran")
	image := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{marker: marker})
	keyHome := gpgHome(t)
	fingerprint := testKey(t, keyHome, "Install Test <install-test@example.invalid>")
	signature, publicKey := signImage(t, keyHome, fingerprint, image)

	// One byte appended after signing: the signature is intact, the file is not.
	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, append(data, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := runScript(t, home, installScript(t),
		"--signature", signature, "--public-key", publicKey, "--fingerprint", fingerprint, image)
	if err == nil {
		t.Fatalf("a tampered image was installed:\n%s", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("the image runtime ran before its signature was checked")
	}
	for _, path := range installedPaths(home) {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("a refused install wrote %s", path)
		}
	}
}

func TestInstallRefusesASignatureFromAnUntrustedKey(t *testing.T) {
	home := freshHome(t)
	marker := filepath.Join(t.TempDir(), "runtime-ran")
	image := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{marker: marker})

	trustedHome := gpgHome(t)
	trusted := testKey(t, trustedHome, "Trusted Publisher <trusted@example.invalid>")
	foreignHome := gpgHome(t)
	foreign := testKey(t, foreignHome, "Foreign Signer <foreign@example.invalid>")
	if trusted == foreign {
		t.Fatal("the two throwaway keys are the same key")
	}
	// The image is signed by the foreign key, and that key's public key accompanies
	// it, exactly as an attacker would deliver it.
	signature, foreignPublicKey := signImage(t, foreignHome, foreign, image)
	trustedPublicKey := filepath.Join(t.TempDir(), "trusted-key.asc")
	export := exec.Command("gpg", "--batch", "--armor", "--export", trusted)
	export.Env = append(os.Environ(), "GNUPGHOME="+trustedHome)
	armored, err := export.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustedPublicKey, armored, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		key     string
		trusted string
	}{
		{"the signer's key beside the download is not trusted", foreignPublicKey, trusted},
		{"a signature by another key is rejected against the trusted key", trustedPublicKey, trusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, err := runScript(t, home, installScript(t),
				"--signature", signature, "--public-key", tc.key, "--fingerprint", tc.trusted, image)
			if err == nil {
				t.Fatalf("an image signed by an untrusted key was installed:\n%s", output)
			}
			if !strings.Contains(output, foreign) && !strings.Contains(output, trusted) {
				t.Errorf("the refusal names no fingerprint, so it does not explain the failure:\n%s", output)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Error("the image runtime ran before the signature was rejected")
			}
			for _, path := range installedPaths(home) {
				if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
					t.Errorf("a refused install wrote %s", path)
				}
			}
		})
	}
}

// A failed signature check must not disturb an installation that already exists:
// the image keeps running the version that was verified before, and the monitoring
// database is byte-identical.
func TestFailedSignatureLeavesTheInstallationAndDataUnchanged(t *testing.T) {
	home := freshHome(t)
	script := installScript(t)
	installed := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{})
	if output, err := runScript(t, home, script, installed); err != nil {
		t.Fatalf("the first install failed: %v\n%s", err, output)
	}
	database := monitoringHistory(t, home)
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}

	// The upgrade is signed by a key the user does not trust, so the code is valid
	// but the file still must not replace a working installation.
	candidate := writeFakeImage(t, t.TempDir(), "0.2.0-dev.1+g222222222222", "development", fakeImageOptions{})
	trustedHome := gpgHome(t)
	trusted := testKey(t, trustedHome, "Trusted Publisher <trusted@example.invalid>")
	foreignHome := gpgHome(t)
	foreign := testKey(t, foreignHome, "Foreign Signer <foreign@example.invalid>")
	signature, foreignPublicKey := signImage(t, foreignHome, foreign, candidate)

	if output, err := runScript(t, home, script,
		"--signature", signature, "--public-key", foreignPublicKey, "--fingerprint", trusted, candidate); err == nil {
		t.Fatalf("an upgrade signed by an untrusted key was installed:\n%s", output)
	}

	got, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldBytes, got) {
		t.Error("a failed signature check changed the installed image")
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a failed signature check changed the monitoring database")
	}
	noStagingDirectories(t, home)
}

// A keyring that already holds the trusted key is the second acceptable trust
// anchor: nothing is imported from the download, and the keyring is the user's own.
func TestInstallVerifiesWithAPreviouslyTrustedKeyring(t *testing.T) {
	home := freshHome(t)
	marker := filepath.Join(t.TempDir(), "runtime-ran")
	image := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{marker: marker})
	keyHome := gpgHome(t)
	fingerprint := testKey(t, keyHome, "Trusted Keyring <keyring@example.invalid>")
	signature, publicKey := signImage(t, keyHome, fingerprint, image)

	trusted := filepath.Join(t.TempDir(), "trusted-keyring")
	if err := os.MkdirAll(trusted, 0o700); err != nil {
		t.Fatal(err)
	}
	importKey := exec.Command("gpg", "--homedir", trusted, "--batch", "--quiet", "--import", publicKey)
	if output, err := importKey.CombinedOutput(); err != nil {
		t.Fatalf("importing the trusted key into the test keyring failed: %v\n%s", err, output)
	}

	output, err := runScript(t, home, installScript(t),
		"--signature", signature, "--keyring", trusted, "--fingerprint", fingerprint, image)
	if err != nil {
		t.Fatalf("installing with a trusted keyring failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, installedImage)); err != nil {
		t.Errorf("the image verified against the keyring was not installed: %v", err)
	}

	// A keyring that does not hold the trusted key is not a trust anchor.
	empty := filepath.Join(t.TempDir(), "empty-keyring")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	otherHome := freshHome(t)
	if output, err := runScript(t, otherHome, installScript(t),
		"--signature", signature, "--keyring", empty, "--fingerprint", fingerprint, image); err == nil {
		t.Fatalf("an empty keyring was accepted as a trust anchor:\n%s", output)
	}
	for _, path := range installedPaths(otherHome) {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("a refused install wrote %s", path)
		}
	}
}

func TestInstallRequiresATrustedFingerprintForASignature(t *testing.T) {
	home := freshHome(t)
	marker := filepath.Join(t.TempDir(), "runtime-ran")
	image := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{marker: marker})

	// A public key and a signature without a fingerprint is exactly the "trust the
	// key that came with the download" mistake the installer must refuse.
	keyHome := gpgHome(t)
	fingerprint := testKey(t, keyHome, "Install Test <install-test@example.invalid>")
	signature, publicKey := signImage(t, keyHome, fingerprint, image)

	output, err := runScript(t, home, installScript(t), "--signature", signature, "--public-key", publicKey, image)
	if err == nil {
		t.Fatalf("a signature without a trusted fingerprint was accepted:\n%s", output)
	}
	if !strings.Contains(output, "--fingerprint") {
		t.Errorf("the refusal does not ask for the fingerprint:\n%s", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Error("the image runtime ran although the signature options were incomplete")
	}
}

func TestUpgradeReplacesTheImageAndKeepsData(t *testing.T) {
	home := freshHome(t)
	script := installScript(t)
	oldImage := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{})
	newImage := writeFakeImage(t, t.TempDir(), "0.2.0-dev.1+g222222222222", "development", fakeImageOptions{})

	if output, err := runScript(t, home, script, oldImage); err != nil {
		t.Fatalf("the first install failed: %v\n%s", err, output)
	}
	database := monitoringHistory(t, home)
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}

	output, err := runScript(t, home, script, newImage)
	if err != nil {
		t.Fatalf("the upgrade failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "0.2.0") {
		t.Errorf("the upgrade did not report the new version:\n%s", output)
	}

	installed, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(newImage)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, want) {
		t.Error("the installed image is not the image that was upgraded to")
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatalf("the upgrade removed the monitoring database: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the upgrade changed the monitoring database")
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "applications", "radanor-sparrow.desktop")); err != nil {
		t.Errorf("the launcher entry is missing after the upgrade: %v", err)
	}
	noStagingDirectories(t, home)
}

func TestUpgradeRefusesAMismatchedVariant(t *testing.T) {
	home := freshHome(t)
	script := installScript(t)
	oldImage := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{})
	releaseImage := writeFakeImage(t, t.TempDir(), "1.0.0", "release", fakeImageOptions{})

	if output, err := runScript(t, home, script, oldImage); err != nil {
		t.Fatalf("the first install failed: %v\n%s", err, output)
	}
	database := monitoringHistory(t, home)
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}

	// A release artifact must never replace a development installation through the
	// development path, even after the image was unpacked and inspected.
	if output, err := runScript(t, home, script, releaseImage); err == nil {
		t.Fatalf("a release artifact replaced a development installation:\n%s", output)
	}

	installed, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldBytes, installed) {
		t.Error("the refused upgrade changed the installed image")
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused upgrade changed the monitoring database")
	}
	noStagingDirectories(t, home)
}

// A refused upgrade after the image was unpacked — the icons are validated only
// then — must still leave the working installation and its data in place.
func TestUpgradeRefusedAfterUnpackingKeepsTheInstalledImage(t *testing.T) {
	home := freshHome(t)
	script := installScript(t)
	oldImage := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{})
	brokenImage := writeFakeImage(t, t.TempDir(), "0.2.0-dev.1+g222222222222", "development", fakeImageOptions{missingIcon: true})

	if output, err := runScript(t, home, script, oldImage); err != nil {
		t.Fatalf("the first install failed: %v\n%s", err, output)
	}
	database := monitoringHistory(t, home)
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}

	if output, err := runScript(t, home, script, brokenImage); err == nil {
		t.Fatalf("an image without the approved artwork was installed:\n%s", output)
	}

	installed, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldBytes, installed) {
		t.Error("the refused upgrade changed the installed image")
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused upgrade changed the monitoring database")
	}
	noStagingDirectories(t, home)
}

// TestInterruptedUpgradeLeavesTheInstallationIntact interrupts the installer while
// the image runtime is running, before anything in the home directory is written.
func TestInterruptedUpgradeLeavesTheInstallationIntact(t *testing.T) {
	home := freshHome(t)
	script := installScript(t)
	oldImage := writeFakeImage(t, t.TempDir(), "0.1.0-dev.1+g111111111111", "development", fakeImageOptions{})

	if output, err := runScript(t, home, script, oldImage); err != nil {
		t.Fatalf("the first install failed: %v\n%s", err, output)
	}
	database := monitoringHistory(t, home)
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "runtime-started")
	slowImage := writeFakeImage(t, t.TempDir(), "0.2.0-dev.1+g222222222222", "development",
		fakeImageOptions{marker: marker, pause: 10 * time.Second})

	command := exec.Command("bash", script, slowImage)
	// A process group lets the interruption reach the stand-in runtime too, so the
	// test does not leave a sleeping child behind or wait for it.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	environment := []string{"HOME=" + home}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			environment = append(environment, entry)
		}
	}
	command.Env = environment
	var combined bytes.Buffer
	command.Stdout, command.Stderr = &combined, &combined
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	// Wait until the runtime is definitely running, so the interruption happens
	// inside the extraction rather than before the installer started.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("the stand-in runtime never started:\n%s", combined.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	installed, err := os.ReadFile(filepath.Join(home, installedImage))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldBytes, installed) {
		t.Error("an interrupted upgrade changed the installed image")
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("an interrupted upgrade changed the monitoring database")
	}
	noStagingDirectories(t, home)
}
