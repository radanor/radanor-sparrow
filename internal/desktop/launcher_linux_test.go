//go:build linux

package desktop

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The launcher integration has properties that neither side can prove alone: the
// window a desktop shell matches to the entry has to be the window this product
// actually shows, and the icon the entry names has to be the artwork the window
// itself presents. These tests read the installer, the window's C surface and the
// approved artwork, and compare them — the same way the Windows icon is pinned —
// so neither the entry nor an installed icon can drift away from the product.

func readInstaller(t *testing.T) string {
	t.Helper()
	return mustRead(t, filepath.Join("..", "..", "scripts", "install-linux.sh"))
}

func readShellHeader(t *testing.T) string {
	t.Helper()
	return mustRead(t, filepath.Join("..", "gtkwindow", "shell.h"))
}

// programNameFromShell reads the program name the window sets for GTK. That value
// is what GDK uses for the X11 WM_CLASS and the Wayland application id.
func programNameFromShell(t *testing.T, shell string) string {
	t.Helper()
	match := regexp.MustCompile(`#define RADANOR_PROGRAM_NAME "([^"]+)"`).FindStringSubmatch(shell)
	if match == nil {
		t.Fatal("shell.h does not define RADANOR_PROGRAM_NAME; the window has no stable window class or application id")
	}
	return match[1]
}

func TestLauncherEntryMatchesTheWindowItStarts(t *testing.T) {
	installer := readInstaller(t)
	shell := readShellHeader(t)
	name := programNameFromShell(t, shell)

	// The name must actually be applied, before GTK initializes, or the window
	// would fall back to whatever the component file happens to be called.
	if !strings.Contains(shell, "g_set_prgname(RADANOR_PROGRAM_NAME)") {
		t.Error("shell.h defines the program name but never sets it, so the window class would follow the component's file name")
	}
	if !strings.Contains(installer, "wmclass="+name) {
		t.Errorf("the installer does not declare wmclass=%s, the window class the window presents (%s)", name, name)
	}
	if !strings.Contains(installer, "StartupWMClass=%s") {
		t.Error("the launcher entry does not set StartupWMClass, so a shell cannot match the running window to it")
	}
	// A Wayland shell matches the application id against the launcher entry's file
	// name, so the entry has to be named after the same value.
	if !strings.Contains(installer, fmt.Sprintf(`entry="$applications/%s.desktop"`, name)) {
		t.Errorf("the launcher entry is not named %s.desktop, so a Wayland window would not be matched to it", name)
	}
	if !strings.Contains(installer, "appid="+name) {
		t.Errorf("the installed icon is not named after the product (%s)", name)
	}
	if !strings.Contains(installer, "Icon=%s") {
		t.Error("the launcher entry has no Icon= key, so it would show a placeholder")
	}
	// The supervisor finds the window component only beside the executable
	// (windowHelperPathFor), so the installer must put it there.
	for _, want := range []string{
		`target="$bindir/radanor-sparrow"`,
		`component="$bindir/radanor-sparrow-window"`,
		`install -m 755 -- "$repo/dist/linux/radanor-sparrow-window" "$component"`,
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("the installer does not place the window component beside the executable: missing %q", want)
		}
	}
	for _, want := range []string{"[Desktop Entry]", "Type=Application", "Name=Radanor Sparrow", `Exec="%s"`, "Terminal=false", "Categories=System;Security;", "StartupNotify=true"} {
		if !strings.Contains(installer, want) {
			t.Errorf("the launcher entry is missing %q", want)
		}
	}
	if strings.Contains(installer, "Shield") {
		t.Error("the installer still names the old product")
	}
}

// TestInstalledIconsAreTheApprovedArtworkAtTheirOwnSize pins two things at once:
// that the installer ships the approved artwork from the dashboard's own icons,
// and that each installed file is installed at the size it really has. An icon
// placed in a directory whose size it does not have is a claim the icon theme
// acts on — GTK then scales it from the wrong base size, or ignores it entirely.
func TestInstalledIconsAreTheApprovedArtworkAtTheirOwnSize(t *testing.T) {
	installer := readInstaller(t)
	installed := regexp.MustCompile(`install_icon (\S+) ([0-9]+x[0-9]+)`).FindAllStringSubmatch(installer, -1)
	if len(installed) == 0 {
		t.Fatal("the installer installs no application icon")
	}
	for _, icon := range installed {
		source, directory := icon[1], icon[2]
		path := filepath.Join("..", "..", "web", "public", source)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("the installer names an icon that does not exist: %v", err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s is not a PNG image: %v", source, err)
		}
		if size := fmt.Sprintf("%dx%d", config.Width, config.Height); size != directory {
			t.Errorf("%s is %s but is installed into %s, so the icon theme would use the wrong base size", source, size, directory)
		}
	}
	// Uninstalling has to remove exactly what installing writes.
	uninstall := installer[strings.Index(installer, `if [[ "$mode" == uninstall ]]`):]
	sizes := uninstall[strings.Index(uninstall, "for size in"):]
	sizes = sizes[:strings.Index(sizes, "\n")]
	for _, icon := range installed {
		if !strings.Contains(sizes, icon[2]) {
			t.Errorf("the uninstall path does not remove the %s icon", icon[2])
		}
	}
}

// TestLauncherIconIsTheSameArtworkAsTheWindowIcon compares pixels: the largest
// launcher icon must be the same image the native window shows, not a redrawn or
// recoloured icon that merely looks similar. The icon file the window embeds
// carries the same frames as the approved artwork, so a mismatch here means one of
// the two was changed.
func TestLauncherIconIsTheSameArtworkAsTheWindowIcon(t *testing.T) {
	installer := readInstaller(t)
	var largestSource, largestSize string
	for _, icon := range regexp.MustCompile(`install_icon (\S+) ([0-9]+)x[0-9]+`).FindAllStringSubmatch(installer, -1) {
		if icon[2] > largestSize {
			largestSource, largestSize = icon[1], icon[2]
		}
	}
	if largestSource == "" {
		t.Fatal("the installer installs no application icon")
	}
	size := 0
	if _, err := fmt.Sscanf(largestSize, "%d", &size); err != nil || size <= 0 {
		t.Fatalf("unreadable icon size %q", largestSize)
	}
	launcher, err := os.ReadFile(filepath.Join("..", "..", "web", "public", largestSource))
	if err != nil {
		t.Fatal(err)
	}
	decodeLauncher, err := png.Decode(bytes.NewReader(launcher))
	if err != nil {
		t.Fatal(err)
	}
	window, err := os.ReadFile(filepath.Join("..", "gtkwindow", "assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	frame, frameWidth := icoFrame(t, window, size)
	if frameWidth != size {
		t.Fatalf("the window icon has no %dx%d frame to compare against", size, size)
	}
	windowPixels := dibPixels(t, frame, size)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var alpha, red, green, blue uint8
			switch value := decodeLauncher.At(x, y).(type) {
			case color.NRGBA:
				red, green, blue, alpha = value.R, value.G, value.B, value.A
			case color.RGBA:
				red, green, blue, alpha = value.R, value.G, value.B, value.A
			default:
				t.Fatalf("unexpected colour type %T in the launcher icon", decodeLauncher.At(x, y))
			}
			offset := (y*size + x) * 4
			windowRed, windowGreen, windowBlue := windowPixels[offset], windowPixels[offset+1], windowPixels[offset+2]
			windowAlpha := windowPixels[offset+3]
			if alpha == 0 && windowAlpha == 0 {
				continue // nothing is drawn here in either image
			}
			if red != windowRed || green != windowGreen || blue != windowBlue || alpha != windowAlpha {
				t.Fatalf("the launcher icon differs from the window icon at %d,%d: launcher rgba(%d,%d,%d,%d), window rgba(%d,%d,%d,%d)",
					x, y, red, green, blue, alpha, windowRed, windowGreen, windowBlue, windowAlpha)
			}
		}
	}
}

// icoFrame returns the 32-bit frame of the requested width and the width it
// actually has, so a caller can tell "no such frame" from "the frame I asked for".
func icoFrame(t *testing.T, icon []byte, size int) ([]byte, int) {
	t.Helper()
	if len(icon) < 6 || binary.LittleEndian.Uint16(icon[2:4]) != 1 {
		t.Fatal("the window icon is not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(icon[4:6]))
	nearest := 0
	for index := 0; index < count; index++ {
		entry := icon[6+index*16 : 6+(index+1)*16]
		width := int(entry[0])
		if width == 0 {
			width = 256
		}
		if width >= nearest {
			nearest = width
		}
		if width != size {
			continue
		}
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		length := int(binary.LittleEndian.Uint32(entry[8:12]))
		if offset+length > len(icon) {
			t.Fatalf("the %dx%d frame runs past the end of the icon file", size, size)
		}
		return icon[offset : offset+length], width
	}
	return nil, nearest
}

// dibPixels decodes a 32-bit BITMAPINFOHEADER frame into top-down RGBA bytes. The
// frame stores its rows bottom-up, and its height counts the colour rows and the
// AND mask together.
func dibPixels(t *testing.T, frame []byte, size int) []byte {
	t.Helper()
	if len(frame) < 40 || binary.LittleEndian.Uint32(frame[0:4]) != 40 {
		t.Fatal("the window icon frame is not a BITMAPINFOHEADER frame")
	}
	if bits := binary.LittleEndian.Uint16(frame[14:16]); bits != 32 {
		t.Fatalf("the window icon frame is %d bits per pixel, want 32", bits)
	}
	height := int(int32(binary.LittleEndian.Uint32(frame[8:12])))
	if height != size*2 {
		t.Fatalf("the window icon frame height is %d, want %d colour rows plus the mask", height, size*2)
	}
	pixels := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		row := frame[40+y*size*4:]
		for x := 0; x < size; x++ {
			blue, green, red, alpha := row[x*4], row[x*4+1], row[x*4+2], row[x*4+3]
			target := ((size-1-y)*size + x) * 4
			pixels[target], pixels[target+1], pixels[target+2], pixels[target+3] = red, green, blue, alpha
		}
	}
	return pixels
}
