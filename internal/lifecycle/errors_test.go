package lifecycle

import (
	"errors"
	"testing"
)

func TestDuplicateInstanceHasTypedError(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir, "typed-error-regression")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Acquire(dir, "typed-error-regression")
	if second != nil {
		second.Close()
		t.Fatal("duplicate lock succeeded")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatal("duplicate owner not distinguishable from I/O failure")
	}
}
