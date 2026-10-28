package desktop

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsDesktopUsesApprovedIconBytes(t *testing.T) {
	icon, err := os.ReadFile(filepath.Join("assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(icon)
	if got, want := hex.EncodeToString(digest[:]), "1c0d087ddca30ded5773135a6447b1c15b8cb8e7e43e316c1aebf132e336d0c3"; got != want {
		t.Fatalf("native Windows icon sha256 = %s, want approved favicon %s", got, want)
	}
	public, err := os.ReadFile(filepath.Join("..", "..", "web", "public", "favicon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(icon, public) {
		t.Fatal("native Windows icon differs from the approved web/public/favicon.ico")
	}
}

func TestWindowsDesktopContractKeepsAuthenticationAndFallback(t *testing.T) {
	source, err := os.ReadFile("webview_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		`ipc.Call(ctx, runtimeDir, secret, "issue_ticket", "")`,
		`origin + "/#ticket="`,
		`return func() {}, openBrowser(url)`,
		`case wmClose:`,
		`procShowWindow.Call(hwnd, swHide)`,
		`case menuExit:`,
		`w.cancel()`,
		`if activateDesktopWindow()`,
	} {
		if !strings.Contains(text+mustRead(t, "activation.go"), required) {
			t.Errorf("Windows desktop contract is missing %q", required)
		}
	}
	if strings.Contains(text, ".Bind(") {
		t.Fatal("Windows desktop exposes a JavaScript-to-native binding")
	}
}

func TestCrossBuiltWindowsArtifactIntegration(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_WINDOWS_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_WINDOWS_BINARY to a cross-built executable")
	}
	file, err := pe.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	_ = file.Close()
	if !ok || header.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatal("Windows distribution is not linked for the GUI subsystem")
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	icon, err := os.ReadFile(filepath.Join("assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(executable, icon) {
		t.Fatal("cross-built distribution does not contain the approved icon bytes")
	}
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
