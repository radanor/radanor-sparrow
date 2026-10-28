package gateway

import (
	"bytes"
	"io/fs"
	"os"
	"testing"
)

// Read-only verification of the actual built artifact, not a test HTTP server.
func TestDistributionEmbedsEveryApplicationAsset(t *testing.T) {
	path := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if path == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built distribution")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = fs.WalkDir(embeddedAssets, "assets", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := embeddedAssets.ReadFile(path)
		if err != nil {
			return err
		}
		if len(content) == 0 || !bytes.Contains(binary, content) {
			t.Errorf("distribution does not contain exact asset %s", path)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 3 {
		t.Fatal("distribution asset fixture lacks HTML, JavaScript or styles")
	}
}
