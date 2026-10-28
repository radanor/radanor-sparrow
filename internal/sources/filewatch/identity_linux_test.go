//go:build linux

package filewatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/monitoring"
)

// runInotifyOnce starts a source over dir, creates the same fixed set of files,
// and returns the identities it observed. The files are removed afterwards so the
// next call sees them as new creations again, which reproduces exactly the shape
// of a restart: the same directory, the same filenames, the same event types, and
// therefore the same sequence numbers.
func runInotifyOnce(t *testing.T, dir string, names []string) []string {
	t.Helper()
	source, err := New([]Root{{Label: "probe", Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report := &testReporter{healthy: make(chan struct{})}
	ids := make(chan string, 256)
	done := make(chan error, 1)
	go func() {
		done <- source.Run(ctx, nil, func(_ context.Context, e monitoring.Event) error {
			ids <- e.ID
			return nil
		}, report)
	}()
	select {
	case <-report.healthy:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not become healthy")
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Collect until every creation has been seen or the deadline passes.
	var observed []string
	deadline := time.After(3 * time.Second)
	for len(observed) < len(names) {
		select {
		case id := <-ids:
			observed = append(observed, id)
		case <-deadline:
			t.Fatalf("only %d of %d creations observed in this run", len(observed), len(names))
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("source did not stop")
	}
	for _, name := range names {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	// A rename event for the removal would be reported as well; only the
	// creations matter, and the count is what makes the comparison meaningful.
	return observed[:len(names)]
}

// TestRepeatedRunsDoNotReuseEventIdentities is the end-to-end reproduction of the
// CE-6 monitoring-continuity defect. Two consecutive runs over the same directory
// observe the same first sequence numbers. Before the per-run discriminator was
// added, the second run produced identities the store already held, and
// INSERT OR IGNORE dropped them, so a restarted core appeared to stop monitoring.
func TestRepeatedRunsDoNotReuseEventIdentities(t *testing.T) {
	dir := t.TempDir()
	names := []string{"probe-0.txt", "probe-1.txt", "probe-2.txt", "probe-3.txt"}
	first := runInotifyOnce(t, dir, names)
	second := runInotifyOnce(t, dir, names)
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("a run observed no events")
	}
	seen := map[string]bool{}
	for _, id := range first {
		if seen[id] {
			t.Fatalf("one run repeated an identity: %q", id)
		}
		seen[id] = true
	}
	for _, id := range second {
		if seen[id] {
			t.Fatalf("a later run reused the identity %q; the store would discard the event", id)
		}
		if !containsInstance(id) {
			t.Fatalf("identity %q carries no per-run discriminator", id)
		}
	}
	// The sequence numbers themselves really do restart, which is why the
	// discriminator is required.
	if first[0] == second[0] {
		t.Fatal("the test did not reproduce a restart restating the sequence")
	}
}

// containsInstance reports whether an identity has the "prefix:instance:seq" shape
// rather than the old "prefix:seq" one.
func containsInstance(id string) bool {
	return strings.Count(id, ":") >= 2
}
