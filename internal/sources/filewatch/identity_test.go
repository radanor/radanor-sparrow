package filewatch

import (
	"errors"
	"strings"
	"testing"
)

// TestEverySourceRunGetsItsOwnInstance is the regression guard for the CE-6
// monitoring-continuity defect. Native file notification identity is a counter
// that restarts at 1 in every process, and the event store enforces
// UNIQUE(source, event_id) with an INSERT OR IGNORE (a replayed observation must
// not be stored twice). Without a per-run discriminator the second session emits
// exactly the identifiers the first session already stored, so the store silently
// discarded the beginning of every session after the first: the dashboard showed
// the previous session's events and none of the new ones.
func TestEverySourceRunGetsItsOwnInstance(t *testing.T) {
	dir := t.TempDir()
	root := []Root{{Label: "probe", Path: dir}}
	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.instance == "" {
		t.Fatal("a source run has no instance discriminator")
	}
	if first.instance == second.instance {
		t.Fatal("two source runs share an instance discriminator; restarts would collide")
	}
}

// TestEventIdentityIsStableWithinARunAndUniqueAcrossRuns pins both halves of the
// contract: a redelivered observation inside one run keeps its identity (so the
// store still recognises it as a duplicate), while the same sequence number in a
// later run is a different identity (so it is stored rather than dropped).
func TestEventIdentityIsStableWithinARunAndUniqueAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	root := []Root{{Label: "probe", Path: dir}}
	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.eventID("inotify:", 7) != first.eventID("inotify:", 7) {
		t.Fatal("the identity of one observation changed inside a single run")
	}
	if first.eventID("inotify:", 7) == second.eventID("inotify:", 7) {
		t.Fatal("the same sequence number maps to the same identity in two runs")
	}
	if first.eventID("inotify:", 7) == first.eventID("rdcw:", 7) {
		t.Fatal("two event kinds share an identity")
	}
	// The platform prefixes stay recognisable and the value stays small.
	id := first.eventID("inotify:", 7)
	if !strings.HasPrefix(id, "inotify:") || !strings.HasSuffix(id, ":7") {
		t.Fatalf("unexpected identity shape: %q", id)
	}
	// Event.Validate rejects an identity longer than 256 bytes.
	if len(id) > 64 {
		t.Fatalf("identity is unexpectedly long: %d bytes", len(id))
	}
}

// TestInstanceIsUnpredictableAndDistinct pins the L-1 property of the
// discriminator: it comes from the operating system's CSPRNG, is long enough
// that two runs colliding is not a practical event, and never contains a
// character that could corrupt the event identity it is embedded in.
func TestInstanceIsUnpredictableAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		value, err := newInstanceID()
		if err != nil {
			t.Fatalf("newInstanceID failed: %v", err)
		}
		if value == "" {
			t.Fatal("newInstanceID returned an empty discriminator")
		}
		if len(value) != instanceIDBytes*2 {
			t.Fatalf("discriminator is %d bytes of hex, want %d", len(value), instanceIDBytes*2)
		}
		if strings.ContainsAny(value, ":/\\ ") {
			t.Fatalf("discriminator %q would corrupt an event identity", value)
		}
		seen[value] = true
	}
	if len(seen) != 32 {
		t.Fatalf("newInstanceID repeated itself: %d distinct of 32", len(seen))
	}
}

// TestInstanceFailsClosedWhenTheRandomSourceIsUnavailable is the L-1 correction.
// The clock fallback it replaces was guessable, was not distinct between two
// processes started in the same instant, and could go backwards across a clock
// adjustment — any of which would make two runs share an identity. The source
// now refuses to exist rather than continue on a weak one.
func TestInstanceFailsClosedWhenTheRandomSourceIsUnavailable(t *testing.T) {
	original := randomRead
	t.Cleanup(func() { randomRead = original })
	randomRead = func([]byte) (int, error) { return 0, errors.New("no entropy source") }

	if _, err := newInstanceID(); err == nil {
		t.Fatal("a run identity was minted without a working random source")
	}
	// The failure reaches the caller: a source is never built with a weak
	// discriminator, so monitoring starts without a broken identity rather than
	// with a predictable one.
	if _, err := New([]Root{{Label: "probe", Path: t.TempDir()}}); err == nil {
		t.Fatal("a source was constructed without a working random source")
	}
}
