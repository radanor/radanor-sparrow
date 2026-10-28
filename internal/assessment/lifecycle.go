package assessment

import "time"

// Finding is a confirmed security weakness backed by evidence. Findings exist
// only for FAIL results.
type Finding struct {
	ID           int64       `json:"id"`
	Fingerprint  string      `json:"fingerprint"`
	CheckID      string      `json:"check_id"`
	CheckVersion string      `json:"check_version"`
	Platform     string      `json:"platform"`
	Severity     Severity    `json:"severity"`
	Lifecycle    Lifecycle   `json:"lifecycle"`
	Title        string      `json:"title"`
	Summary      string      `json:"summary"`
	Action       string      `json:"action"`
	VerifyMethod string      `json:"verify_method"`
	Limitations  []string    `json:"limitations,omitempty"`
	Evidence     Observation `json:"evidence"`
	FirstSeenAt  time.Time   `json:"first_seen_at"`
	LastSeenAt   time.Time   `json:"last_seen_at"`
	ResolvedAt   *time.Time  `json:"resolved_at,omitempty"`
	ReopenedAt   *time.Time  `json:"reopened_at,omitempty"`
	RunID        int64       `json:"run_id"`
}

// Transition computes the next lifecycle for a finding given this run's
// outcome for its check. The rules are deliberately conservative:
//
//   - A finding only exists for a confirmed FAIL. UNKNOWN, NOT_SUPPORTED and
//     STALE are not evidence that a weakness is gone, so they leave the
//     lifecycle untouched (returning false means "no change").
//   - A finding becomes "continuing" only on a fresh FAIL with the same check
//     version. A materially changed rule does not silently continue the old
//     finding.
//   - A finding is "resolved" only on a fresh PASS backed by reliable evidence.
//   - A previously resolved finding that FAILs again becomes "reopened".
//   - A superseded finding is retired: if the same rule version is put back and
//     confirms the weakness again, it is reopened rather than forgotten.
func Transition(current Lifecycle, res CheckResult) (Lifecycle, bool) {
	switch res.Result {
	case Fail:
		switch current {
		case "":
			// No prior finding: this is the first sighting.
			return LifecycleNew, true
		case LifecycleNew:
			// A finding confirmed failing on a later scan is continuing, not
			// perpetually new.
			return LifecycleContinuing, true
		case LifecycleContinuing:
			return LifecycleContinuing, false
		case LifecycleResolved, LifecycleSuperseded:
			// A confirmed fresh FAIL reopens a resolved finding, and revives one
			// whose rule has come back into use.
			return LifecycleReopened, true
		case LifecycleReopened:
			return LifecycleReopened, false
		}
	case Pass:
		// Only reliable, fresh evidence resolves a finding. A pass on stale or
		// unverifiable evidence is not returned by the evaluator, but guard here
		// so the invariant holds for callers too.
		if !res.Fresh {
			return current, false
		}
		switch current {
		case LifecycleNew, LifecycleContinuing, LifecycleReopened, LifecycleSuperseded:
			return LifecycleResolved, true
		case LifecycleResolved:
			return LifecycleResolved, false
		}
	}
	// UNKNOWN, NOT_SUPPORTED, STALE: no reliable evidence, no transition.
	return current, false
}
