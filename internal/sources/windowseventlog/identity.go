package windowseventlog

// Channel-scoped event identity.
//
// This file deliberately carries no build tag, following the same reasoning as
// the CE-6 Windows inventory model: the identity of an observed record is string
// composition that must be reviewed and tested without a Windows host, so it
// lives where the ordinary test suite drives it from fixtures. Only the
// subscription and rendering calls stay behind the windows build tag.
//
// # The problem
//
// Windows numbers every event in a channel with an EventRecordID that is unique
// only within that channel *and within one instance of that log*. Clearing a
// channel — an operator action, or a retention policy — restarts the numbering,
// so a record id that has already been stored can be handed to a genuinely new
// event. Using the bare record id as the storage identity made that new event
// look like a replay of an old one, and the store dropped it: a real detection
// silently became a duplicate.
//
// # The contract
//
// The identity is composed of three values that all come from the record itself:
//
//	wevt:<len(channel)>:<channel>:<record id>:<system time>
//
//   - Replay-stable. Redelivering the same record — after a restart, after a
//     queue overflow, or through a bookmark replay — produces the same identity,
//     so it is still recognised as a duplicate. Nothing process-local is part of
//     the value: appending a run or process id would make every replay look like
//     brand new evidence, which is the opposite of what an identity is for.
//   - Channel-scoped. Two channels can never collide, because the channel is part
//     of the value, and the length prefix makes the composition unambiguous even
//     if a channel name ever contained the separator.
//   - Clear-aware. After a channel clear, the reused record id carries the new
//     record's own system time, so it produces a different identity and is stored
//     as new evidence rather than being mistaken for a replay of the old record.
//
// # What this does not fix
//
// The *cursor* is separate from the identity. The cursor is the bookmark XML
// Windows returns for a record: it names a position in a log instance, not a
// stable position across instances. After a channel clear, a stored bookmark may
// name a record that no longer exists. Detecting that needs the channel's
// current oldest and newest record numbers (EvtGetLogInfo), which cannot be
// verified without a native Windows host, so it is documented and deferred in
// the private engineering record rather than implemented on assumption.
// Until it is verified, the observable behaviour is the existing one: a bookmark
// Windows can no longer honour produces a monitoring gap and an invalid-checkpoint
// recovery, which is visible rather than silent.

import (
	"strconv"
	"strings"
)

// identityPrefix marks this source's identities in the store's (source, event id)
// space, so a value from another source can never be read as one of these.
const identityPrefix = "wevt"

// maxIdentityChannelLen bounds the channel component of an identity. A channel
// name is a fixed, allowlisted constant in this source; the bound exists so an
// unexpected value cannot make stored identities unbounded.
const maxIdentityChannelLen = 128

// maxIdentityTimeLen bounds the system-time component for the same reason.
const maxIdentityTimeLen = 64

// EventIdentity renders the storage identity of one observed record.
//
// Every argument is read from the record itself, so the function is pure: two
// calls for the same record return the same value no matter how far apart in
// time they are made, which is exactly the property a replay must have.
func EventIdentity(channel string, recordID uint64, systemTime string) string {
	name := bounded(channel, maxIdentityChannelLen, "unknown-channel")
	stamp := bounded(strings.TrimSpace(systemTime), maxIdentityTimeLen, "unknown-time")
	// The channel is length-prefixed rather than escaped: a name that contains
	// the separator cannot then be confused with a different name.
	return identityPrefix + ":" + strconv.Itoa(len(name)) + ":" + name + ":" +
		strconv.FormatUint(recordID, 10) + ":" + stamp
}

// RecordSequence renders the channel-relative position of a record. It is the
// record number, which is the only sequence the log itself provides; it is
// reported as source sequence metadata rather than used as an identity, because
// it is not unique across a channel clear.
func RecordSequence(recordID uint64) string { return strconv.FormatUint(recordID, 10) }

// bounded trims a component to its limit and substitutes a stable placeholder
// when it is empty, so a record that is missing a value still has an identity —
// and the same identity every time.
func bounded(value string, limit int, placeholder string) string {
	if value == "" {
		return placeholder
	}
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
