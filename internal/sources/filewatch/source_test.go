package filewatch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsSymlinkAndUnboundedRoots(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := New([]Root{{Label: "bad", Path: link}}); err == nil {
		t.Fatal("symlink root accepted")
	}
	roots := make([]Root, MaxRoots+1)
	if _, err := New(roots); err == nil {
		t.Fatal("unbounded roots accepted")
	}
}

func TestDescriptorDeclaresNoReplay(t *testing.T) {
	dir := t.TempDir()
	source, err := New([]Root{{Label: "test", Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	d := source.Descriptor()
	if d.Recovery != "none" || d.ID != SourceID {
		t.Fatalf("unexpected descriptor: %#v", d)
	}
}
