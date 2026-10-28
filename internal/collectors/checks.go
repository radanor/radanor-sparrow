package collectors

// Collectors return facts only. Checks apply versioned rules to those facts and
// are the only place a PASS/FAIL verdict is produced. A check that cannot be
// grounded in collected evidence returns UNKNOWN rather than a reassuring
// verdict, and only a FAIL produces a finding.

import (
	"github.com/radanor/radanor-shield/internal/assessment"
)

// checkVersion is the rule version stamped on every definition in this package.
// It participates in the finding fingerprint: bumping it deliberately retires
// findings produced by an older rule instead of silently continuing them.
const checkVersion = "1.0.0"

// defaultFreshness is how long a collected observation may support a verdict
// before it must be re-collected. Assessment observations are gathered at scan
// time, so this mostly guards reused or resumed evidence. It is the assessment
// package's default, so a check definition that omits MaxAge and a check that
// sets it explicitly cannot disagree about the freshness policy.
const defaultFreshness = assessment.DefaultCheckFreshness

// mkCheck assembles a check definition with the shared bookkeeping fields.
func mkCheck(id, collectorID, category, title string, severity assessment.Severity, platforms []string, explanation, action, verify string, fn func(assessment.Observation) assessment.Eval, limits ...string) assessment.CheckDefinition {
	return assessment.CheckDefinition{
		ID:           id,
		Version:      checkVersion,
		CollectorID:  collectorID,
		Category:     category,
		Title:        title,
		Platforms:    platforms,
		Severity:     severity,
		Explanation:  explanation,
		Action:       action,
		VerifyMethod: verify,
		Limitations:  limits,
		MaxAge:       defaultFreshness,
		EvaluateFn:   fn,
	}
}
