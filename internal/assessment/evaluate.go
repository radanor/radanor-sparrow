package assessment

import (
	"time"
)

// Evaluate applies a check's versioned rule to one observation and stamps the
// result with freshness. The separation matters: a collection problem
// (permission denied, unsupported, incomplete, failed) never becomes a FAIL,
// and only a FAIL can produce a finding.
func (c CheckDefinition) Evaluate(obs Observation) CheckResult {
	now := time.Now()
	res := CheckResult{
		CheckID:      c.ID,
		CheckVersion: c.Version,
		Category:     c.Category,
		Title:        c.Title,
		Platforms:    c.Platforms,
		Severity:     c.Severity,
		Explanation:  c.Explanation,
		Action:       c.Action,
		VerifyMethod: c.VerifyMethod,
		Limitations:  c.Limitations,
		Observation:  obs,
		CollectedAt:  obs.CollectedAt,
		EvaluatedAt:  now,
	}
	age := now.Sub(obs.CollectedAt)
	res.FreshnessAge = age.Round(time.Second).String()
	// An omitted budget falls back to the package default rather than to
	// "never stale", so a check definition cannot silently escape the freshness
	// policy by leaving MaxAge unset.
	maxAge := c.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCheckFreshness
	}
	res.Fresh = age <= maxAge

	switch obs.Status {
	case PermissionDenied:
		res.Result = Unknown
		res.Detail = "collection was refused by the operating system; the security state is not known"
		return res
	case Unsupported:
		res.Result = NotSupported
		res.Detail = "this check does not apply to the current platform"
		return res
	case Incomplete, Failed:
		res.Result = Unknown
		res.Detail = "evidence is incomplete, so no verdict is asserted"
		return res
	}

	// A stale observation cannot reconfirm a verdict; report it as stale rather
	// than silently trusting old evidence.
	if !res.Fresh {
		res.Result = Stale
		res.Detail = "supporting evidence is older than this check's freshness budget"
		return res
	}

	eval := c.EvaluateFn(obs)
	res.Result = eval.Result
	res.Detail = eval.Detail
	return res
}
