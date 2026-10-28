package appimage

// The per-user integration is the only part of L4 that writes into the user's
// home, so it is exercised end to end against a temporary HOME: install the real
// artifact, check exactly what was installed, uninstall, and prove that a
// pre-existing monitoring profile was never touched.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// freshHome is an isolated home directory the installer may write into, so the
// test never touches the developer's own integration.
func freshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// runScript runs an installer command with HOME replaced, not merely appended:
// a duplicate HOME in the environment would be resolved either way depending on
// the libc, and the test must not be able to install into the real home.
func runScript(t *testing.T, home string, script string, args ...string) (string, error) {
	t.Helper()
	return runScriptInDir(t, "", home, script, args...)
}

// runScriptInDir is runScript with a chosen working directory, so a test can prove
// the installer does not depend on being run from the repository.
func runScriptInDir(t *testing.T, dir string, home string, script string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{script}, args...)...)
	command.Dir = dir
	environment := []string{"HOME=" + home}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			environment = append(environment, entry)
		}
	}
	command.Env = environment
	var combined bytes.Buffer
	command.Stdout, command.Stderr = &combined, &combined
	err := command.Run()
	return combined.String(), err
}

// copyFile puts a copy of the built artifact where a download would land: outside
// the repository, under a name the caller chooses.
func copyFile(t *testing.T, source, destination string, mode os.FileMode) string {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, mode); err != nil {
		t.Fatal(err)
	}
	return destination
}

// installedPaths are the paths the installer owns, so a test can assert that a
// refusal left none of them behind.
func installedPaths(home string) []string {
	return []string{
		filepath.Join(home, ".local", "bin", "radanor-sparrow.AppImage"),
		filepath.Join(home, ".local", "share", "applications", "radanor-sparrow.desktop"),
		filepath.Join(home, ".local", "share", "doc", "radanor-sparrow"),
	}
}

// variantOf reads the artifact's own build metadata, so a test can state which
// variant it expects the installer to enforce.
func variantOf(t *testing.T, path string) string {
	t.Helper()
	info, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	variant := buildInfoFields(t, info)["variant"]
	if variant == "" {
		t.Fatal("the artifact does not record a variant")
	}
	return variant
}

func TestInstallAppImageIsPerUserAndUninstallKeepsUserData(t *testing.T) {
	path := artifact(t)
	variant := variantOf(t, path)
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("install script: %v", err)
	}
	home := freshHome(t)

	// A profile that already exists, as an installed user's would.
	stateDir := filepath.Join(home, ".local", "state", "radanor-shield")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(stateDir, "monitoring.db")
	if err := os.WriteFile(profile, []byte("existing monitoring history"), 0o600); err != nil {
		t.Fatal(err)
	}

	if output, err := runScript(t, home, script, path); err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}

	installed := filepath.Join(home, ".local", "bin", "radanor-sparrow.AppImage")
	installedInfo, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("the AppImage was not installed: %v", err)
	}
	if installedInfo.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed AppImage is not executable: %v", installedInfo.Mode())
	}
	installedBytes, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installedBytes, original) {
		t.Error("the installed AppImage is not the artifact that was built")
	}

	entryPath := filepath.Join(home, ".local", "share", "applications", "radanor-sparrow.desktop")
	entry, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("the launcher entry was not written: %v", err)
	}
	for _, want := range []string{
		"Type=Application",
		"Name=Radanor Sparrow",
		"Exec=\"" + installed + "\"",
		"Icon=radanor-sparrow",
		"Terminal=false",
		"StartupWMClass=radanor-sparrow",
		"Categories=System;Security;",
		"X-Radanor-Variant=" + variant,
	} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("the launcher entry does not contain %q:\n%s", want, entry)
		}
	}
	if validate, err := exec.LookPath("desktop-file-validate"); err == nil {
		if output, err := exec.Command(validate, entryPath).CombinedOutput(); err != nil {
			t.Errorf("desktop-file-validate rejected the installed entry: %v\n%s", err, output)
		}
	}
	for _, icon := range []struct{ size, approved string }{
		{"16x16", "favicon-16x16.png"},
		{"32x32", "favicon-32x32.png"},
		{"48x48", "favicon-48x48.png"},
		{"128x128", "favicon-128x128.png"},
		{"256x256", "favicon-256x256.png"},
	} {
		installedIcon := filepath.Join(home, ".local", "share", "icons", "hicolor", icon.size, "apps", "radanor-sparrow.png")
		got, err := os.ReadFile(installedIcon)
		if err != nil {
			t.Fatalf("the %s icon was not installed: %v", icon.size, err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "web", "public", icon.approved))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the installed %s icon is not the approved artwork", icon.size)
		}
	}
	for _, document := range []string{"LICENSE", "THIRD_PARTY_NOTICES.md"} {
		installed := filepath.Join(home, ".local", "share", "doc", "radanor-sparrow", document)
		source, err := os.ReadFile(filepath.Join("..", "..", document))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(installed)
		if err != nil {
			t.Fatalf("%s was not installed: %v", document, err)
		}
		if !bytes.Equal(got, source) {
			t.Errorf("the installed %s differs from the repository copy", document)
		}
	}

	if output, err := runScript(t, home, script, "--uninstall"); err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, output)
	}
	for _, removed := range []string{
		installed,
		entryPath,
		filepath.Join(home, ".local", "share", "icons", "hicolor", "16x16", "apps", "radanor-sparrow.png"),
		filepath.Join(home, ".local", "share", "icons", "hicolor", "32x32", "apps", "radanor-sparrow.png"),
		filepath.Join(home, ".local", "share", "icons", "hicolor", "48x48", "apps", "radanor-sparrow.png"),
		filepath.Join(home, ".local", "share", "doc", "radanor-sparrow"),
	} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Errorf("uninstall left %s behind (%v)", removed, err)
		}
	}
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("uninstall removed the user's monitoring profile: %v", err)
	}
	if string(content) != "existing monitoring history" {
		t.Errorf("uninstall modified the user's monitoring profile: %q", content)
	}
}

// The installer is documented as working with a downloaded AppImage and no
// checkout, so this test gives it exactly that: the image in a directory that
// looks like a download, the script beside it, and a working directory that
// contains nothing from the repository. Nothing it installs may come from the
// repository either, which is why the artwork and the documents are compared with
// the repository's copies only as the approved originals.
func TestInstallAppImageFromADownloadedFileInAnyDirectory(t *testing.T) {
	path := artifact(t)
	variant := variantOf(t, path)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "install-appimage.sh"))
	if err != nil {
		t.Fatal(err)
	}
	home := freshHome(t)

	downloads := t.TempDir()
	image := copyFile(t, path, filepath.Join(downloads, "RadanorSparrow-downloaded-x86_64.AppImage"), 0o755)
	// A copy of the script where a downloader would put it, so the run cannot fall
	// back on the repository being one directory above the script.
	standalone := copyFile(t, script, filepath.Join(downloads, "install-appimage.sh"), 0o644)

	// Relative name from the download directory: the installer must resolve it
	// before it changes directory to unpack the image.
	output, err := runScriptInDir(t, downloads, home, standalone, filepath.Base(image))
	if err != nil {
		t.Fatalf("installing a downloaded AppImage failed: %v\n%s", err, output)
	}
	if strings.Contains(output, "web/public") {
		t.Errorf("the installer still refers to the checkout:\n%s", output)
	}

	installed := filepath.Join(home, ".local", "bin", "radanor-sparrow.AppImage")
	installedBytes, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("the AppImage was not installed: %v", err)
	}
	downloaded, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installedBytes, downloaded) {
		t.Error("the installed file is not the downloaded artifact")
	}

	entry, err := os.ReadFile(filepath.Join(home, ".local", "share", "applications", "radanor-sparrow.desktop"))
	if err != nil {
		t.Fatalf("the launcher entry was not written: %v", err)
	}
	for _, want := range []string{
		"Exec=\"" + installed + "\"",
		"Icon=radanor-sparrow",
		"StartupWMClass=radanor-sparrow",
		"X-Radanor-Variant=" + variant,
	} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("the launcher entry does not contain %q:\n%s", want, entry)
		}
	}

	// The artwork and the documents now come out of the image, and they are still
	// the approved ones.
	for _, icon := range []struct{ size, approved string }{
		{"16x16", "favicon-16x16.png"},
		{"32x32", "favicon-32x32.png"},
		{"48x48", "favicon-48x48.png"},
		{"128x128", "favicon-128x128.png"},
		{"256x256", "favicon-256x256.png"},
	} {
		got, err := os.ReadFile(filepath.Join(home, ".local", "share", "icons", "hicolor", icon.size, "apps", "radanor-sparrow.png"))
		if err != nil {
			t.Fatalf("the %s icon was not installed: %v", icon.size, err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "web", "public", icon.approved))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the installed %s icon is not the approved artwork", icon.size)
		}
	}
	for _, document := range []string{"LICENSE", "THIRD_PARTY_NOTICES.md"} {
		want, err := os.ReadFile(filepath.Join("..", "..", document))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(home, ".local", "share", "doc", "radanor-sparrow", document))
		if err != nil {
			t.Fatalf("%s was not installed: %v", document, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the installed %s is not the approved document", document)
		}
	}
}

// The variant check the installer documents: a development path must not install a
// release artifact, and --release must not install a development one. The second
// direction cannot be produced here while the licensing gate blocks release builds,
// so the assertion is written against the artifact's own variant and the test says
// which half it exercised.
func TestInstallAppImageRefusesAMismatchedVariant(t *testing.T) {
	path := artifact(t)
	variant := variantOf(t, path)
	mismatch := "--release"
	if variant == "release" {
		mismatch = "--development"
	}
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	home := freshHome(t)

	output, err := runScript(t, home, script, mismatch, path)
	if err == nil {
		t.Fatalf("installing a variant=%s artifact with %s succeeded:\n%s", variant, mismatch, output)
	}
	if !strings.Contains(output, "variant="+variant) {
		t.Errorf("the refusal does not name the artifact's variant:\n%s", output)
	}
	if !strings.Contains(output, "Nothing was installed") {
		t.Errorf("the refusal does not say that nothing was installed:\n%s", output)
	}
	for _, path := range installedPaths(home) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a refused install created %s (%v)", path, err)
		}
	}
}

// An AppImage that has not been made executable is the most common first mistake,
// so it is reported as that rather than as an unreadable file.
func TestInstallAppImageRefusesANonExecutableFile(t *testing.T) {
	path := artifact(t)
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	home := freshHome(t)
	image := copyFile(t, path, filepath.Join(t.TempDir(), "RadanorSparrow-x86_64.AppImage"), 0o644)

	output, err := runScript(t, home, script, image)
	if err == nil {
		t.Fatalf("installing a non-executable file succeeded:\n%s", output)
	}
	if !strings.Contains(output, "chmod +x") {
		t.Errorf("the refusal does not name the fix:\n%s", output)
	}
	for _, path := range installedPaths(home) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("a refused install created %s (%v)", path, err)
		}
	}
}

// Uninstalling must work when nothing is installed: it is the documented first
// step of a reinstall, and it must never need the AppImage it once installed.
func TestUninstallWithoutAnInstallationIsHarmless(t *testing.T) {
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	home := freshHome(t)
	output, err := runScript(t, home, script, "--uninstall")
	if err != nil {
		t.Fatalf("uninstall in an empty home failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "left in place") {
		t.Errorf("uninstall does not state that user data was kept:\n%s", output)
	}
}

// The installer can verify the download against the publisher's manifest before it
// touches the home directory. A manifest that does not describe this file, or
// describes it with another hash, must stop the installation.
func TestInstallAppImageVerifiesThePublishedChecksum(t *testing.T) {
	path := artifact(t)
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	home := freshHome(t)
	image := copyFile(t, path, filepath.Join(t.TempDir(), "RadanorSparrow-x86_64.AppImage"), 0o755)
	data, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	manifestDir := t.TempDir()
	manifest := filepath.Join(manifestDir, "SHA256SUMS.appimage")
	if err := os.WriteFile(manifest, []byte(fmt.Sprintf("%x  %s\n", digest, filepath.Base(image))), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runScript(t, home, script, "--checksums", manifest, image)
	if err != nil {
		t.Fatalf("installing a file that matches its manifest failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Checksum verified") {
		t.Errorf("the installer did not report the checksum check:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "radanor-sparrow.AppImage")); err != nil {
		t.Fatalf("the verified image was not installed: %v", err)
	}

	// The same manifest with another hash must refuse, and leave nothing behind.
	refusedHome := freshHome(t)
	wrong := filepath.Join(t.TempDir(), "SHA256SUMS.appimage")
	if err := os.WriteFile(wrong, []byte(fmt.Sprintf("%064x  %s\n", 0, filepath.Base(image))), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runScript(t, refusedHome, script, "--checksums", wrong, image)
	if err == nil {
		t.Fatalf("a file with a mismatched checksum was installed:\n%s", output)
	}
	if !strings.Contains(output, "Checksum mismatch") {
		t.Errorf("the refusal does not name the mismatch:\n%s", output)
	}
	for _, leftover := range installedPaths(refusedHome) {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Errorf("a checksum-refused install created %s (%v)", leftover, err)
		}
	}

	// A manifest that does not mention this file is not evidence of anything.
	unrelated := filepath.Join(t.TempDir(), "SHA256SUMS.appimage")
	if err := os.WriteFile(unrelated, []byte("0000000000000000000000000000000000000000000000000000000000000000  other.AppImage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runScript(t, freshHome(t), script, "--checksums", unrelated, image)
	if err == nil {
		t.Fatalf("a manifest that does not list the file was accepted:\n%s", output)
	}
	if !strings.Contains(output, "does not list") {
		t.Errorf("the refusal does not explain the missing entry:\n%s", output)
	}
}

// A refused install must leave an existing installation exactly as it was: the
// image and the launcher entry are staged and moved into place, so a failure
// cannot replace a working installation with a partial one.
func TestRefusedInstallLeavesTheExistingInstallationIntact(t *testing.T) {
	path := artifact(t)
	if variantOf(t, path) != "development" {
		t.Skip("the artifact is not a development build, so this test cannot force a refusal")
	}
	script := filepath.Join("..", "..", "scripts", "install-appimage.sh")
	home := freshHome(t)

	if output, err := runScript(t, home, script, path); err != nil {
		t.Fatalf("the initial install failed: %v\n%s", err, output)
	}
	installed := filepath.Join(home, ".local", "bin", "radanor-sparrow.AppImage")
	entry := filepath.Join(home, ".local", "share", "applications", "radanor-sparrow.desktop")
	before := map[string][]byte{}
	for _, file := range []string{installed, entry} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		before[file] = data
	}

	// --release refuses a development artifact after the image was validated and
	// unpacked, which is the point where a careless installer would already have
	// written something.
	if output, err := runScript(t, home, script, "--release", path); err == nil {
		t.Fatalf("the mismatched install unexpectedly succeeded:\n%s", output)
	}

	for file, want := range before {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("the refused install removed %s: %v", file, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("the refused install changed %s", file)
		}
	}
	// No staging directory may be left behind, empty or otherwise.
	entries, err := os.ReadDir(filepath.Join(home, ".local", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".radanor-install.") {
			t.Errorf("the installer left a staging directory behind: %s", entry.Name())
		}
	}
}

// The installer is a supply-chain surface: it must not ask for privileges, and it
// must not fetch or execute anything from the network. This pins both properties
// so a future convenience feature cannot quietly add them.
func TestInstallerIsUnprivilegedAndOffline(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-appimage.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	privileged := regexp.MustCompile(`(?m)(^|[;&|])[[:space:]]*(sudo|doas|pkexec)[[:space:]]`)
	if match := privileged.FindString(script); match != "" {
		t.Errorf("the installer must never invoke a privilege tool; found %q", strings.TrimSpace(match))
	}
	for _, network := range []string{"curl", "wget", "git clone", "apt-get", "dpkg", "systemctl"} {
		if strings.Contains(script, network) {
			t.Errorf("the installer must not use %q: it installs only the file it was given", network)
		}
	}
	for _, systemPath := range []string{"\"/etc/", "\"/usr/", "\"/opt/", "\"/var/"} {
		if strings.Contains(script, systemPath) {
			t.Errorf("the installer must not write outside the user's home; found %s", systemPath)
		}
	}
}
