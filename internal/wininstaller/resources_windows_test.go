//go:build windows

package wininstaller

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/winres"
	"golang.org/x/sys/windows"
)

func TestInstallerWindowsResources(t *testing.T) {
	setup := os.Getenv("RADANOR_SETUP_EXE")
	portable := os.Getenv("RADANOR_PORTABLE_EXE")
	if setup == "" || portable == "" {
		t.Skip("installer artifact opt-in")
	}
	info, err := ReadBuildInfo(filepath.Dir(portable))
	if err != nil {
		t.Fatal(err)
	}
	size, err := windows.GetFileVersionInfoSize(setup, nil)
	if err != nil || size == 0 {
		t.Fatalf("Windows installer version: %v", err)
	}
	data := make([]byte, size)
	if err = windows.GetFileVersionInfo(setup, 0, size, unsafe.Pointer(&data[0])); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"ProductName": branding.DisplayName, "CompanyName": branding.Company, "FileVersion": info.Version, "ProductVersion": info.Version} {
		var value *uint16
		var length uint32
		if err = windows.VerQueryValue(unsafe.Pointer(&data[0]), `\StringFileInfo\040904B0\`+key, unsafe.Pointer(&value), &length); err != nil || length < 2 {
			t.Fatalf("Windows %s unreadable: %v", key, err)
		}
		if got := windows.UTF16PtrToString(value); got != want {
			t.Errorf("Windows %s=%q, want %q", key, got, want)
		}
	}
	entries, err := winres.ReadResources(setup)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "public", "favicon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	images, err := winres.ParseICO(approved)
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range images {
		found := false
		for _, entry := range entries {
			if entry.Type == winres.ResourceIcon && bytes.Equal(entry.Data, image.Data) {
				found = true
			}
		}
		if !found {
			t.Fatal("installer lacks byte-exact approved icon image")
		}
	}
	module, err := windows.LoadLibraryEx(setup, 0, 0x22)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FreeLibrary(module)
	user := windows.NewLazySystemDLL("user32.dll")
	loaded := false
	for _, entry := range entries {
		if entry.Type == winres.ResourceGroupIcon {
			icon, _, _ := user.NewProc("LoadImageW").Call(uintptr(module), uintptr(entry.Name), 1, 32, 32, 0)
			if icon != 0 {
				user.NewProc("DestroyIcon").Call(icon)
				loaded = true
				break
			}
		}
	}
	if !loaded {
		t.Fatal("Windows cannot load installer shell icon")
	}
}
