package windowseventlog

import (
	"strings"
	"testing"
)

// Windows event-log identity tests. These run on every host: the identity is
// string composition, not a native call, so the rules that make it safe can be
// pinned here rather than being asserted only on a Windows machine.

// TestIdentityIsStableForTheSameRecord is the replay property. The store keeps
// its deduplication decision in the (source, event id) unique constraint, so a
// record redelivered after a restart must produce the same identity or it will
// be stored again as brand new evidence.
func TestIdentityIsStableForTheSameRecord(t *testing.T) {
	const channel = "Microsoft-Windows-Windows Defender/Operational"
	first := EventIdentity(channel, 48213, "2026-10-11T09:14:07.123456789Z")
	second := EventIdentity(channel, 48213, "2026-10-11T09:14:07.123456789Z")
	if first != second {
		t.Fatalf("identity changed between calls: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, identityPrefix+":") {
		t.Fatalf("identity = %q, want the %q prefix", first, identityPrefix)
	}
	if !strings.Contains(first, "48213") {
		t.Fatalf("identity = %q, want it to name the record", first)
	}
}

// TestIdentityOfARecordReusedAfterAChannelClear is the CE-6 M-4 fix. Windows
// restarts EventRecordID numbering when a channel is cleared, so the same record
// number can be handed to a different event. Treating the number alone as an
// identity made that event a false duplicate, and the store dropped it.
func TestIdentityOfARecordReusedAfterAChannelClear(t *testing.T) {
	const channel = "Microsoft-Windows-Windows Defender/Operational"
	before := EventIdentity(channel, 48213, "2026-10-11T09:14:07.123456789Z")
	// The channel is cleared, numbering restarts, and record 48213 is now a
	// completely different detection.
	after := EventIdentity(channel, 48213, "2026-10-12T22:41:55.000000000Z")
	if before == after {
		t.Fatal("a record id reused after a channel clear produced the same identity, so the new event would be dropped as a duplicate")
	}
	if !strings.Contains(after, "48213") {
		t.Fatalf("identity = %q, want it to keep the channel-relative record number", after)
	}
}

// TestIdentityIsScopedToTheChannel keeps two channels apart: record numbers are
// per-channel, so identities that ignored the channel would collide across logs.
func TestIdentityIsScopedToTheChannel(t *testing.T) {
	a := EventIdentity("Microsoft-Windows-Windows Defender/Operational", 7, "2026-10-11T09:14:07Z")
	b := EventIdentity("Microsoft-Windows-Sysmon/Operational", 7, "2026-10-11T09:14:07Z")
	if a == b {
		t.Fatalf("two channels produced the same identity: %q", a)
	}
}

// TestIdentityCompositionIsUnambiguous guards the separator. A channel name that
// contains the separator must not be able to imitate a different name.
func TestIdentityCompositionIsUnambiguous(t *testing.T) {
	split := EventIdentity("a:b", 1, "t")
	unsplit := EventIdentity("a", 1, "t")
	if split == unsplit {
		t.Fatalf("channel names %q and %q collided", "a:b", "a")
	}
}

// TestIdentityNeverUsesProcessState pins the rule the mandate names directly: no
// run id, process id or clock is mixed into the value. A replay after a restart
// would otherwise look like new evidence, and every record would be stored twice.
func TestIdentityNeverUsesProcessState(t *testing.T) {
	const channel = "Microsoft-Windows-Windows Defender/Operational"
	first := EventIdentity(channel, 1, "2026-10-11T09:14:07Z")
	for i := 0; i < 1000; i++ {
		if got := EventIdentity(channel, 1, "2026-10-11T09:14:07Z"); got != first {
			t.Fatalf("identity %q differs from %q: the value depends on process state", got, first)
		}
	}
}

// TestIdentityIsBounded keeps a surprising value from producing an unbounded
// identity, and keeps an empty value usable rather than panicking.
func TestIdentityIsBounded(t *testing.T) {
	hugeChannel := EventIdentity(strings.Repeat("c", 4096), 1, "2026-10-11T09:14:07Z")
	if len(hugeChannel) > maxIdentityBytes {
		t.Fatalf("identity is %d bytes, want it under the %d byte bound", len(hugeChannel), maxIdentityBytes)
	}
	hugeTime := EventIdentity("a", 18446744073709551615, strings.Repeat("t", 4096))
	if len(hugeTime) > maxIdentityBytes {
		t.Fatalf("identity is %d bytes, want it under the %d byte bound", len(hugeTime), maxIdentityBytes)
	}
	// An empty system time still produces a stable identity rather than an
	// empty value that would make two different records look identical.
	missing := EventIdentity("a", 9, "")
	if strings.HasSuffix(missing, ":") {
		t.Fatalf("identity = %q, want a placeholder in place of the missing time", missing)
	}
	if missing != EventIdentity("a", 9, "   ") {
		t.Fatal("a missing and a blank system time produced different identities")
	}
	if missing == EventIdentity("a", 10, "") {
		t.Fatal("two different records produced the same identity when their time was missing")
	}
}

// maxIdentityBytes is the largest identity this composition can produce. Every
// component is bounded, so a surprising channel name or system time cannot make
// a stored identity unbounded.
const maxIdentityBytes = 256

// TestRecordSequenceIsTheChannelPosition keeps the sequence metadata honest: it
// is the log's own record number, which is a position within one channel, not an
// identity.
func TestRecordSequenceIsTheChannelPosition(t *testing.T) {
	if got := RecordSequence(48213); got != "48213" {
		t.Fatalf("sequence = %q, want the record number", got)
	}
	if got := RecordSequence(0); got != "0" {
		t.Fatalf("sequence = %q, want 0 for the first record", got)
	}
	if got := RecordSequence(18446744073709551615); got != "18446744073709551615" {
		t.Fatalf("sequence = %q, want the full 64-bit range preserved", got)
	}
}
