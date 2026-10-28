//go:build !windows

package lifecycle

import "testing"

func TestSingleInstanceLock(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir, "core")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Acquire(dir, "core"); err == nil {
		second.Close()
		t.Fatal("second instance acquired lock")
	}
	other, err := Acquire(dir, "gateway")
	if err != nil {
		t.Fatal("separate component lock failed:", err)
	}
	other.Close()
}
