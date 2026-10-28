package appimage

// These tests validate the AppImage that was actually built, not the build
// script's intentions. They need the artifact and unsquashfs, so they skip unless
// RADANOR_APPIMAGE names one. The `linux-appimage` CI job sets it to the image it
// just built, and a normal `go test ./...` stays independent of build artifacts.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func artifact(t *testing.T) string {
	t.Helper()
	path := os.Getenv("RADANOR_APPIMAGE")
	if path == "" {
		t.Skip("set RADANOR_APPIMAGE to a built AppImage to validate the artifact")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("RADANOR_APPIMAGE=%s: %v", path, err)
	}
	if _, err := exec.LookPath("unsquashfs"); err != nil {
		t.Skip("unsquashfs is required to inspect the AppImage")
	}
	return path
}

// cat reads one file out of the AppImage without mounting it.
func cat(t *testing.T, info Info, name string) []byte {
	t.Helper()
	command := exec.Command("unsquashfs", "-o", strconv.FormatInt(info.Offset, 10), "-cat", info.Path, name)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("read %s from the AppImage: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes()
}

// listing is `unsquashfs -ll`, whose output carries the mode bits the runtime
// will see.
func listing(t *testing.T, info Info) string {
	t.Helper()
	command := exec.Command("unsquashfs", "-o", strconv.FormatInt(info.Offset, 10), "-ll", info.Path)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("list the AppImage: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String()
}

func TestAppImageHasAValidContainer(t *testing.T) {
	path := artifact(t)
	info, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Offset <= 0 || info.Offset >= info.Size {
		t.Fatalf("squashfs offset %d is not inside the %d-byte artifact", info.Offset, info.Size)
	}
	// The runtime is a self-contained ELF. A few megabytes of runtime plus the
	// two static Go binaries is expected; a WebKit bundle would be an order of
	// magnitude larger, so this catches an accidental system-library bundle.
	if info.Size > 40<<20 {
		t.Errorf("the AppImage is %d bytes; GTK/WebKitGTK must stay system-provided", info.Size)
	}
	// The runtime itself must be able to report the same offset we computed.
	command := exec.Command(path, "--appimage-offset")
	command.Env = append(os.Environ(), "APPIMAGE_EXTRACT_AND_RUN=0")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("the AppImage runtime cannot report its offset: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != strconv.FormatInt(info.Offset, 10) {
		t.Errorf("runtime offset = %s, computed offset = %d", got, info.Offset)
	}
}

func TestAppImageCarriesTheApplicationAndItsDocuments(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	list := listing(t, info)
	for _, required := range []string{
		"AppRun",
		".DirIcon",
		"radanor-sparrow.desktop",
		"radanor-sparrow.png",
		"usr/bin/radanor-sparrow",
		"usr/bin/radanor-sparrow-window",
		"usr/share/applications/radanor-sparrow.desktop",
		"usr/share/doc/radanor-sparrow/LICENSE",
		"usr/share/doc/radanor-sparrow/THIRD_PARTY_NOTICES.md",
		"usr/share/icons/hicolor/16x16/apps/radanor-sparrow.png",
		"usr/share/icons/hicolor/32x32/apps/radanor-sparrow.png",
		"usr/share/icons/hicolor/48x48/apps/radanor-sparrow.png",
		"usr/share/icons/hicolor/128x128/apps/radanor-sparrow.png",
		"usr/share/icons/hicolor/256x256/apps/radanor-sparrow.png",
		"usr/share/metainfo/radanor-sparrow.metainfo.xml",
		"usr/share/radanor-sparrow/BUILD-INFO",
	} {
		if !strings.Contains(list, required) {
			t.Errorf("the AppImage does not contain %s", required)
		}
	}
	if strings.Contains(list, "libwebkit") || strings.Contains(list, "libgtk") ||
		strings.Contains(list, "libjavascriptcore") || strings.Contains(list, "libsoup") {
		t.Error("the AppImage bundles GTK/WebKitGTK; they must stay system-provided so the host's sandbox and security updates apply")
	}
	for _, executable := range []string{"usr/bin/radanor-sparrow", "usr/bin/radanor-sparrow-window", "AppRun"} {
		var found bool
		for _, line := range strings.Split(list, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), executable) {
				found = true
				if !strings.HasPrefix(line, "-rwxr-xr-x") && !strings.HasPrefix(line, "-rwxrwxr-x") {
					t.Errorf("%s is not executable in the image: %s", executable, line)
				}
			}
		}
		if !found {
			t.Errorf("%s is missing from the listing", executable)
		}
	}
}

// buildInfoFields reads the key=value metadata without running the application.
func buildInfoFields(t *testing.T, info Info) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(cat(t, info, "usr/share/radanor-sparrow/BUILD-INFO"))), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			fields[key] = value
		}
	}
	return fields
}

func TestAppImageNamesItselfAndItsDevelopmentVariant(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	fields := buildInfoFields(t, info)
	version := fields["version"]
	switch {
	case version == "":
		t.Fatal("BUILD-INFO does not name a version")
	case fields["variant"] != "development":
		t.Errorf("BUILD-INFO variant = %q; a development AppImage must say so", fields["variant"])
	case fields["artifact"] != "AppImage":
		t.Errorf("BUILD-INFO artifact = %q, want AppImage", fields["artifact"])
	case fields["architecture"] != "x86_64":
		t.Errorf("BUILD-INFO architecture = %q, want x86_64", fields["architecture"])
	}
	if !strings.Contains(filepath.Base(info.Path), version) {
		t.Errorf("artifact name %q does not carry the version %q", filepath.Base(info.Path), version)
	}

	entry := string(cat(t, info, "radanor-sparrow.desktop"))
	for _, want := range []string{
		"Type=Application",
		"Name=Radanor Sparrow",
		"Exec=radanor-sparrow",
		"Icon=radanor-sparrow",
		"Terminal=false",
		"StartupWMClass=radanor-sparrow",
		"Categories=System;Security;",
		"X-AppImage-Version=" + version,
		"X-Radanor-Variant=development",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("the desktop entry does not contain %q", want)
		}
	}
	if installed := string(cat(t, info, "usr/share/applications/radanor-sparrow.desktop")); installed != entry {
		t.Error("the embedded launcher entry and the one in usr/share/applications differ")
	}
	if !strings.Contains(string(cat(t, info, "AppRun")), `exec "$APPDIR/usr/bin/radanor-sparrow" "$@"`) {
		t.Error("AppRun does not start the bundled supervisor")
	}
	// The version must be compiled into the supervisor, not only written beside it.
	if !bytes.Contains(cat(t, info, "usr/bin/radanor-sparrow"), []byte(version)) {
		t.Errorf("the supervisor does not contain the build version %q", version)
	}
}

// The checksums the image records for its own executables must match the bytes it
// carries, so the provenance record cannot describe a build that is not inside.
func TestAppImageChecksumsMatchThePackagedBinaries(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	fields := buildInfoFields(t, info)
	for _, name := range []string{"radanor-sparrow", "radanor-sparrow-window"} {
		recorded := fields["binary."+name]
		if recorded == "" {
			t.Errorf("BUILD-INFO records no checksum for binary.%s", name)
			continue
		}
		embedded := cat(t, info, "usr/bin/"+name)
		if len(embedded) == 0 {
			t.Errorf("usr/bin/%s is empty", name)
			continue
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(embedded))
		if actual != recorded {
			t.Errorf("BUILD-INFO records %s for %s, but the packaged file hashes to %s", recorded, name, actual)
		}
	}
}

// A reproducible build is only reproducible if the timestamps inside it are the
// recorded source date and not the clock of the machine that ran the build. The
// packaging step normalizes every inode to that value, so the artifact itself has
// to show it: this reads the squashfs listing back and compares every entry.
func TestAppImageTimestampsAreTheRecordedSourceDate(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	fields := buildInfoFields(t, info)
	if fields["source_date_epoch"] == "" {
		t.Fatal("BUILD-INFO does not record a source_date_epoch, so the timestamps in the image have no documented origin")
	}
	epoch, err := strconv.ParseInt(fields["source_date_epoch"], 10, 64)
	if err != nil || epoch < 0 {
		t.Fatalf("BUILD-INFO source_date_epoch = %q, want a non-negative integer", fields["source_date_epoch"])
	}
	switch fields["source_date_epoch_source"] {
	case "environment", "git-commit", "fixed-epoch":
	default:
		t.Errorf("BUILD-INFO source_date_epoch_source = %q, want environment, git-commit or fixed-epoch", fields["source_date_epoch_source"])
	}

	command := exec.Command("unsquashfs", "-o", strconv.FormatInt(info.Offset, 10), "-ll", info.Path)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("list the AppImage: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	// unsquashfs prints the inode time in the local zone, which is the same one
	// time.Time formats in, so the expected value is compared in local time.
	want := time.Unix(epoch, 0).Format("2006-01-02 15:04")
	checked := 0
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 6 || !strings.Contains(parts[5], "squashfs-root") {
			continue
		}
		if got := parts[3] + " " + parts[4]; got != want {
			t.Errorf("%s has timestamp %s, want the recorded source date %s (epoch %s)", parts[5], got, want, fields["source_date_epoch"])
		}
		checked++
	}
	if checked == 0 {
		t.Error("no squashfs entries were checked; the listing format changed")
	}
}

func TestAppImageCarriesTheApprovedArtworkUnchanged(t *testing.T) {
	info, err := Read(artifact(t))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "web", "public")
	for _, want := range []struct{ embedded, approved string }{
		{"radanor-sparrow.png", "favicon-256x256.png"},
		{"usr/share/icons/hicolor/16x16/apps/radanor-sparrow.png", "favicon-16x16.png"},
		{"usr/share/icons/hicolor/32x32/apps/radanor-sparrow.png", "favicon-32x32.png"},
		{"usr/share/icons/hicolor/48x48/apps/radanor-sparrow.png", "favicon-48x48.png"},
		{"usr/share/icons/hicolor/128x128/apps/radanor-sparrow.png", "favicon-128x128.png"},
		{"usr/share/icons/hicolor/256x256/apps/radanor-sparrow.png", "favicon-256x256.png"},
	} {
		approved, err := os.ReadFile(filepath.Join(root, want.approved))
		if err != nil {
			t.Fatalf("approved artwork %s: %v", want.approved, err)
		}
		embedded := cat(t, info, want.embedded)
		if !bytes.Equal(embedded, approved) {
			t.Errorf("%s is not byte-identical to the approved %s", want.embedded, want.approved)
		}
	}
}
