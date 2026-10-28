package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The approved Radanor brand artwork, as supplied on 2026-10-15. The digests are
// pinned deliberately: recolouring, cropping, recompressing or substituting the
// logo changes them, so replacing the product's artwork is a reviewed change and
// never an accident. See the private engineering record.
//
// These checks read the embedded dashboard, which is what the executable
// actually serves, so they hold even in a checkout that does not carry the
// original files in web/public/.
//
// Two entries are derived rather than supplied: the launcher composition has no
// master larger than 48 px, so favicon-128x128.png and favicon-256x256.png come
// from scripts/build-icons.py, which pins the filter and the master. They are
// pinned here so a silent change is still a reviewed change, and they carry the
// same artwork with no recolouring or cropping.
var approvedBrandArtwork = []struct {
	name          string
	sha256        string
	width, height int
	// iconSizes is set for the .ico, which carries several images at once.
	iconSizes [][2]int
}{
	{"radanor-emblem-light.png", "5f6b2bdbac1096493c87a1f80346912d5042172e6a8dd8d25fa9a7b06472cbee", 167, 135, nil},
	{"radanor-logo-light.png", "d5a3e903ddebb99038ca3f09bf0995f290c4a85973011394b2314cf3202ebcd6", 527, 135, nil},
	{"favicon-16x16.png", "56afac85440694b3a51a0d7f554001460b4eab0aba645f56f88344341d1a08da", 16, 16, nil},
	{"favicon-32x32.png", "e9cf9229d8efab8862eeb0a695dd3e3ea9fe3ee621809c331dfedc039dbcda86", 32, 32, nil},
	{"favicon-48x48.png", "e77d12e53435447572463c7e1351ba652596502453a121f5268bb9de9625d36c", 48, 48, nil},
	// Upscales of favicon-48x48.png (2.67x and 5.33x) that keep the drawing and the
	// navy square exactly; a human visual check is still owed before they are
	// treated as final release artwork. See the private engineering record.
	{"favicon-128x128.png", "090a2b1d11cf2d3876bec05e0cc45bed235d767514d26500f04cf36098a80da0", 128, 128, nil},
	{"favicon-256x256.png", "57f33c0a1d280027ee63afcd1d27cbe5db36495754590deed587e20fa788e837", 256, 256, nil},
	{"apple-touch-icon.png", "27e8266993e2f19b975c09a5e9d2c66cae9d77af2ec0962f52dbfc8081364c0f", 180, 180, nil},
	{"favicon.ico", "1c0d087ddca30ded5773135a6447b1c15b8cb8e7e43e316c1aebf132e336d0c3", 0, 0, [][2]int{{16, 16}, {32, 32}, {48, 48}}},
}

// TestEmbeddedDashboardShipsTheApprovedBrandArtwork is the guard on the
// distributed product: every approved file must be present in the embedded
// dashboard, byte for byte, in the supplied format and size.
func TestEmbeddedDashboardShipsTheApprovedBrandArtwork(t *testing.T) {
	for _, want := range approvedBrandArtwork {
		content, err := embeddedAssets.ReadFile("assets/" + want.name)
		if err != nil {
			t.Errorf("the embedded dashboard does not carry %s: %v", want.name, err)
			continue
		}
		digest := sha256.Sum256(content)
		if got := hex.EncodeToString(digest[:]); got != want.sha256 {
			t.Errorf("embedded %s is not the approved artwork: sha256 %s, want %s", want.name, got, want.sha256)
		}
		if want.iconSizes != nil {
			if sizes := iconImageSizes(t, want.name, content); !equalSizes(sizes, want.iconSizes) {
				t.Errorf("embedded %s holds icon images %v, want %v", want.name, sizes, want.iconSizes)
			}
			continue
		}
		width, height := pngSize(t, want.name, content)
		if width != want.width || height != want.height {
			t.Errorf("embedded %s is %dx%d, want %dx%d", want.name, width, height, want.width, want.height)
		}
	}
}

// TestEmbeddedDashboardReferencesTheApprovedArtwork checks the shipped bundle
// actually uses the artwork. Shipping a logo nobody references would leave the
// product with no visible identity while every other check still passed.
func TestEmbeddedDashboardReferencesTheApprovedArtwork(t *testing.T) {
	var bundle strings.Builder
	err := fs.WalkDir(embeddedAssets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !referencingAsset(entry.Name()) {
			return nil
		}
		content, err := embeddedAssets.ReadFile(path)
		if err != nil {
			return err
		}
		bundle.Write(content)
		bundle.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source := bundle.String()
	for _, want := range []struct{ reference, why string }{
		{"/" + emblemAssetForTest, "the sidebar emblem"},
		{"/" + logoAssetForTest, "the full logo in About"},
		{"href=\"/favicon.ico\"", "the browser tab icon"},
	} {
		if !strings.Contains(source, want.reference) {
			t.Errorf("the embedded dashboard never references %s (%s)", want.reference, want.why)
		}
	}
}

// TestEmbeddedDashboardMatchesTheSuppliedArtwork catches the "edited the file but
// forgot to rebuild" mistake. The source of truth is web/public/; when it is
// present the embedded copy must be identical, so the two can never disagree
// about what the product's logo looks like.
func TestEmbeddedDashboardMatchesTheSuppliedArtwork(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "web", "public")
	if _, err := os.Stat(source); err != nil {
		t.Skip("web/public is not present in this checkout, so there is nothing to compare against; the embedded artwork is still pinned by digest")
	}
	for _, want := range approvedBrandArtwork {
		supplied, err := os.ReadFile(filepath.Join(source, want.name))
		if err != nil {
			t.Errorf("the checkout does not carry the approved %s in web/public: %v", want.name, err)
			continue
		}
		embedded, err := embeddedAssets.ReadFile("assets/" + want.name)
		if err != nil {
			t.Errorf("the embedded dashboard does not carry %s: %v", want.name, err)
			continue
		}
		if !bytes.Equal(supplied, embedded) {
			t.Errorf("embedded %s differs from web/public/%s: the dashboard was not rebuilt after the artwork changed", want.name, want.name)
		}
	}
}

// The names are duplicated here rather than imported so the assertion is about
// the files the dashboard references, not about whatever internal/branding says
// today. internal/branding/branding_test.go keeps the two lists in step.
const (
	emblemAssetForTest = "radanor-emblem-light.png"
	logoAssetForTest   = "radanor-logo-light.png"
)

func referencingAsset(name string) bool {
	for _, extension := range []string{".html", ".css", ".js"} {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

func pngSize(t *testing.T, name string, content []byte) (int, int) {
	t.Helper()
	if len(content) < 24 || !bytes.Equal(content[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("%s is not a PNG file", name)
	}
	return int(binary.BigEndian.Uint32(content[16:20])), int(binary.BigEndian.Uint32(content[20:24]))
}

func iconImageSizes(t *testing.T, name string, content []byte) [][2]int {
	t.Helper()
	if len(content) < 6 || binary.LittleEndian.Uint16(content[0:2]) != 0 || binary.LittleEndian.Uint16(content[2:4]) != 1 {
		t.Fatalf("%s is not a Windows icon resource", name)
	}
	count := int(binary.LittleEndian.Uint16(content[4:6]))
	if count == 0 || len(content) < 6+16*count {
		t.Fatalf("%s has a truncated icon directory", name)
	}
	sizes := make([][2]int, 0, count)
	for i := 0; i < count; i++ {
		width, height := int(content[6+16*i]), int(content[7+16*i])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		sizes = append(sizes, [2]int{width, height})
	}
	return sizes
}

func equalSizes(got, want [][2]int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
