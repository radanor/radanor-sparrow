package assessment

import (
	"testing"
	"time"
)

// collectAt builds a minimal collected observation stamped at a chosen age.
func collectAt(age time.Duration) Observation {
	return Observation{
		ID: "probe", Collector: "probe", Platform: "ubuntu",
		Status: Collected, CollectedAt: time.Now().Add(-age),
	}
}

// TestEvaluateAppliesDefaultFreshnessWhenBudgetOmitted is the regression guard
// for the CE-5 low-severity finding on missing freshness policy defaults. A
// check definition that omits MaxAge must not be treated as forever-fresh: it
// falls back to DefaultCheckFreshness, so a verdict cannot be reconfirmed
// indefinitely from stale evidence.
func TestEvaluateAppliesDefaultFreshnessWhenBudgetOmitted(t *testing.T) {
	def := CheckDefinition{
		ID: "probe", Version: "1.0.0", CollectorID: "probe", Severity: SeverityLow,
		EvaluateFn: func(Observation) Eval { return Eval{Result: Pass, Detail: "ok"} },
	}

	fresh := def.Evaluate(collectAt(time.Minute))
	if !fresh.Fresh {
		t.Fatalf("a recent observation must be fresh; freshness_age=%s", fresh.FreshnessAge)
	}
	if fresh.Result != Pass {
		t.Fatalf("recent observation result = %q, want pass", fresh.Result)
	}

	stale := def.Evaluate(collectAt(DefaultCheckFreshness + time.Minute))
	if stale.Fresh {
		t.Fatal("an observation older than the default budget must not be fresh")
	}
	if stale.Result != Stale {
		t.Fatalf("stale observation result = %q, want stale", stale.Result)
	}
}

// TestEvaluateHonorsExplicitBudgetLongerThanDefault confirms the default is a
// fallback, not an override: a check that declares a longer budget keeps it.
func TestEvaluateHonorsExplicitBudgetLongerThanDefault(t *testing.T) {
	def := CheckDefinition{
		ID: "probe", Version: "1.0.0", CollectorID: "probe", Severity: SeverityLow,
		MaxAge:     24 * time.Hour,
		EvaluateFn: func(Observation) Eval { return Eval{Result: Pass, Detail: "ok"} },
	}
	res := def.Evaluate(collectAt(2 * time.Hour))
	if !res.Fresh || res.Result != Pass {
		t.Fatalf("an explicit 24h budget must accept a 2h old observation: fresh=%v result=%q", res.Fresh, res.Result)
	}
}

// TestDefaultFreshnessIsPositive guards against a zero default reintroducing the
// "forever fresh" behaviour through the back door.
func TestDefaultFreshnessIsPositive(t *testing.T) {
	if DefaultCheckFreshness <= 0 {
		t.Fatalf("DefaultCheckFreshness = %v, must be positive", DefaultCheckFreshness)
	}
}
