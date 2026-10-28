package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Finding identity is deliberately derived from the *check*, not from the
// evidence it happened to observe. Evidence changes between scans — a count, a
// hostname, a sampled address — while the weakness stays the same, so including
// it in the identity would mint a new finding on every scan and leave the old
// one permanently unresolved. Identity is therefore:
//
//	check id + rule version + platform + collector
//
// The rule version participates so a materially changed rule cannot silently
// continue or resolve a finding produced by an older rule.
func Fingerprint(checkID, checkVersion, platform string, obs Observation) string {
	parts := []string{checkID, checkVersion, platform, obs.Collector}
	return stableHex(strings.Join(parts, "\x00"))
}

// boundText trims a value to the documented text limit so stored evidence
// cannot be inflated by unbounded collector output.
func boundText(s string) string {
	if len(s) > MaxTextValue {
		return s[:MaxTextValue]
	}
	return s
}

// stableHex returns a hex-encoded SHA-256 digest, giving each finding a stable,
// fixed-size identity.
func stableHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
