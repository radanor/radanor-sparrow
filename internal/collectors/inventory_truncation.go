package collectors

import "github.com/radanor/radanor-shield/internal/inventory"

// Truncation handling for the inventory collectors, kept platform-neutral because
// both the Linux and the Windows collector sets need exactly the same rule.
//
// This file carries no build tag on purpose. The rule it encodes is a
// correctness property, not a platform detail: a collector whose own command
// output was clipped by the capture cap observed only part of the system, so it
// must say so. Without this the defect that CE-6 review finding H-1 named was
// possible — a listing clipped at the cap produced a partial record count, and
// because that partial count was also the number of records stored, the snapshot
// reported itself as complete.

// markTruncated records that a collector's own command output was clipped, so the
// reading is a partial one. The items already parsed are still returned — they
// are genuine observations — but the status becomes Incomplete and the error
// explains why, because a count taken from a clipped listing is a lower bound and
// must never be presented as the whole inventory.
func markTruncated(collection *inventory.Collection, result commandResult, what string) {
	if !result.Truncated {
		return
	}
	collection.Truncated = true
	collection.Status = inventory.Incomplete
	collection.Error = truncatedNote(what, result.Cap)
}
