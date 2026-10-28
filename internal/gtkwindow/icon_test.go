package gtkwindow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The approved artwork digest, the same one internal/desktop pins for the
// Windows window icon. A window that shows redrawn, upscaled or recoloured
// artwork is a branding failure, so the bytes are asserted rather than trusted.
const approvedIconDigest = "1c0d087ddca30ded5773135a6447b1c15b8cb8e7e43e316c1aebf132e336d0c3"

// TestWindowIconIsTheApprovedArtwork is what stops the Linux window from
// drifting to a different icon: it compares the copy this package embeds with
// the approved file the dashboard serves, byte for byte, in every build — the
// stub included, so the check runs even where GTK is not installed.
func TestWindowIconIsTheApprovedArtwork(t *testing.T) {
	window, err := os.ReadFile(filepath.Join("assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := os.ReadFile(filepath.Join("..", "..", "web", "public", "favicon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(window, approved) {
		t.Fatal("the Linux window icon is not the approved web/public/favicon.ico, byte for byte")
	}
	digest := sha256.Sum256(window)
	if got := hex.EncodeToString(digest[:]); got != approvedIconDigest {
		t.Fatalf("window icon sha256 = %s, want the approved %s", got, approvedIconDigest)
	}
	// The Windows window embeds its own copy of the same artwork; two copies that
	// disagree would mean one platform is showing something else.
	desktop, err := os.ReadFile(filepath.Join("..", "desktop", "assets", "radanor.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(window, desktop) {
		t.Fatal("the Linux and Windows window icons are different files")
	}
}
