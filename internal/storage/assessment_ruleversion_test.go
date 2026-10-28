package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// versionedResult builds a check result under a specific rule version, which is
// what lets these tests exercise a rule change end to end through the real
// reconciliation path.
func versionedResult(id, version string, result assessment.Result) assessment.CheckResult {
	r := checkResult(id, result)
	r.CheckVersion = version
	return r
}

func applyVersionedRun(t *testing.T, s *Store, id, version string, result assessment.Result) int64 {
	t.Helper()
	ctx := context.Background()
	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	r := versionedResult(id, version, result)
	if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{r}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{r}); err != nil {
		t.Fatal(err)
	}
	return runID
}

func findingsFor(t *testing.T, s *Store, checkID string) []FindingRecord {
	t.Helper()
	all, err := s.Findings(context.Background(), "", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []FindingRecord
	for _, f := range all {
		if f.CheckID == checkID {
			out = append(out, f)
		}
	}
	return out
}

func historyDetails(t *testing.T, s *Store, fingerprint string) []string {
	t.Helper()
	entries, err := s.FindingHistory(context.Background(), fingerprint, 50)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, string(e.Lifecycle)+":"+string(e.Result)+":"+e.Detail)
	}
	return out
}

// TestRuleVersionFailThenFailIsSupersededNotDuplicated is the core M2 case. A
// revised rule that still reports the weakness must not leave the previous
// rule's finding open beside it: that is both a duplicate and a permanent
// orphan, because nothing will ever evaluate the retired rule again.
func TestRuleVersionFailThenFailIsSupersededNotDuplicated(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	run1 := applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "1.0.0", assessment.Fail)
	first := findingsFor(t, s, "ssh_permit_empty_passwords_disabled")
	if len(first) != 1 || first[0].Lifecycle != assessment.LifecycleNew {
		t.Fatalf("after the first run findings = %+v, want a single new finding", first)
	}
	old := first[0]

	run2 := applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "2.0.0", assessment.Fail)
	all := findingsFor(t, s, "ssh_permit_empty_passwords_disabled")
	if len(all) != 2 {
		t.Fatalf("findings = %d, want the superseded record retained and one current one", len(all))
	}

	var superseded, current *FindingRecord
	for i := range all {
		switch all[i].CheckVersion {
		case "1.0.0":
			superseded = &all[i]
		case "2.0.0":
			current = &all[i]
		}
	}
	if superseded == nil || current == nil {
		t.Fatalf("expected one finding per rule version, got %+v", all)
	}
	if superseded.Lifecycle != assessment.LifecycleSuperseded {
		t.Fatalf("retired rule instance lifecycle = %q, want superseded", superseded.Lifecycle)
	}
	if superseded.Fingerprint != old.Fingerprint {
		t.Fatal("the retired finding was replaced rather than transitioned")
	}
	if current.Lifecycle != assessment.LifecycleNew {
		t.Fatalf("current rule instance lifecycle = %q, want new", current.Lifecycle)
	}

	// Exactly one finding may still be tracking the weakness.
	summary, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenFindings != 1 {
		t.Fatalf("open findings = %d, want 1", summary.OpenFindings)
	}
	// Supersession must be auditable and must not read as a fix.
	details := strings.Join(historyDetails(t, s, old.Fingerprint), "\n")
	if !strings.Contains(details, "superseded") {
		t.Fatalf("history = %q, want an explicit supersession entry", details)
	}
	if strings.Contains(details, string(assessment.LifecycleResolved)) {
		t.Fatalf("history = %q, a superseded finding must never read as resolved", details)
	}
	// The current rule's finding keeps its own history.
	if len(historyDetails(t, s, current.Fingerprint)) == 0 {
		t.Fatal("the current rule instance has no history")
	}
	if run1 == 0 || run2 == 0 {
		t.Fatal("runs must be recorded")
	}
}

// TestRuleVersionFailThenPassResolvesIt proves a revised rule that verifies the
// weakness as fixed resolves the earlier rule's finding - that is remediation
// evidence from the rule now in use, not a silent resolve.
func TestRuleVersionFailThenPassResolvesIt(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "2.0.0", assessment.Pass)

	all := findingsFor(t, s, "ssh_permit_empty_passwords_disabled")
	if len(all) != 1 {
		t.Fatalf("findings = %d, want only the original, now resolved", len(all))
	}
	if all[0].Lifecycle != assessment.LifecycleResolved {
		t.Fatalf("lifecycle = %q, want resolved", all[0].Lifecycle)
	}
	if all[0].ResolvedAt == nil {
		t.Fatal("a resolved finding must carry its resolution timestamp")
	}
	details := strings.Join(historyDetails(t, s, all[0].Fingerprint), "\n")
	if !strings.Contains(details, "2.0.0") {
		t.Fatalf("history = %q, want it to name the rule version that verified the fix", details)
	}
	summary, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenFindings != 0 {
		t.Fatalf("open findings = %d, want 0", summary.OpenFindings)
	}
}

// TestRuleVersionWithInconclusiveEvidenceLeavesTheFindingOpen is the honesty
// case: a rule that could not evaluate the check is not evidence that the
// weakness is gone, so the earlier rule's finding stays open.
func TestRuleVersionWithInconclusiveEvidenceLeavesTheFindingOpen(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "ssh_permit_empty_passwords_disabled", "2.0.0", assessment.Unknown)

	all := findingsFor(t, s, "ssh_permit_empty_passwords_disabled")
	if len(all) != 1 {
		t.Fatalf("findings = %d, want 1 (inconclusive results never create one)", len(all))
	}
	if !all[0].Lifecycle.IsOpen() {
		t.Fatalf("lifecycle = %q, want it left open", all[0].Lifecycle)
	}
	details := strings.Join(historyDetails(t, s, all[0].Fingerprint), "\n")
	if !strings.Contains(details, "not verified as fixed") {
		t.Fatalf("history = %q, want the inconclusive run recorded", details)
	}
	if strings.Contains(details, string(assessment.LifecycleResolved)) {
		t.Fatalf("history = %q, an inconclusive run must not resolve the finding", details)
	}
	summary, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenFindings != 1 {
		t.Fatalf("open findings = %d, want 1", summary.OpenFindings)
	}
}

// TestResolvedFindingReopensAcrossRuleVersions covers the reopening path and
// proves a resolved finding is never disturbed by a rule that later reports the
// weakness again.
func TestResolvedFindingReopensAcrossRuleVersions(t *testing.T) {
	s, _ := openStore(t, nil)

	applyVersionedRun(t, s, "disk_encryption_present", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "disk_encryption_present", "1.0.0", assessment.Pass)
	resolved := findingsFor(t, s, "disk_encryption_present")
	if len(resolved) != 1 || resolved[0].Lifecycle != assessment.LifecycleResolved {
		t.Fatalf("after the pass findings = %+v, want a single resolved finding", resolved)
	}

	applyVersionedRun(t, s, "disk_encryption_present", "2.0.0", assessment.Fail)
	all := findingsFor(t, s, "disk_encryption_present")
	if len(all) != 2 {
		t.Fatalf("findings = %d, want the resolved history plus the current rule's finding", len(all))
	}
	for _, f := range all {
		switch f.CheckVersion {
		case "1.0.0":
			if f.Lifecycle != assessment.LifecycleResolved {
				t.Fatalf("the resolved finding was disturbed: lifecycle = %q", f.Lifecycle)
			}
			if f.ResolvedAt == nil {
				t.Fatal("the resolution timestamp was lost")
			}
		case "2.0.0":
			if f.Lifecycle != assessment.LifecycleNew {
				t.Fatalf("the current rule's finding = %q, want new", f.Lifecycle)
			}
		}
	}
	summary, err := s.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenFindings != 1 {
		t.Fatalf("open findings = %d, want 1", summary.OpenFindings)
	}
}

// TestSupersededRuleInstanceReopensWhenItReturns proves supersession is
// reversible rather than a dead end: if the earlier rule comes back into use
// and confirms the weakness, it tracks it again.
func TestSupersededRuleInstanceReopensWhenItReturns(t *testing.T) {
	s, _ := openStore(t, nil)

	applyVersionedRun(t, s, "apparmor_enabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "apparmor_enabled", "2.0.0", assessment.Fail)
	applyVersionedRun(t, s, "apparmor_enabled", "1.0.0", assessment.Fail)

	all := findingsFor(t, s, "apparmor_enabled")
	if len(all) != 2 {
		t.Fatalf("findings = %d, want one per rule version", len(all))
	}
	for _, f := range all {
		if f.CheckVersion == "1.0.0" && f.Lifecycle != assessment.LifecycleReopened {
			t.Fatalf("the revived rule instance = %q, want reopened", f.Lifecycle)
		}
	}
	summary, err := s.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenFindings != 2 {
		t.Fatalf("open findings = %d, want both rule instances tracking the weakness again", summary.OpenFindings)
	}
}

// TestFingerprintIsStableForTheSameRule guards the identity contract the whole
// lifecycle rests on: the same rule evaluating the same check must land on the
// same finding rather than minting a new one per scan.
func TestFingerprintIsStableForTheSameRule(t *testing.T) {
	s, _ := openStore(t, nil)

	applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)

	all := findingsFor(t, s, "firewall_enabled")
	if len(all) != 1 {
		t.Fatalf("findings = %d, want 1 for three runs of the same rule", len(all))
	}
	if all[0].Lifecycle != assessment.LifecycleContinuing {
		t.Fatalf("lifecycle = %q, want continuing", all[0].Lifecycle)
	}
}

// TestResolutionTimestampsSurviveLaterTransitions is the finding-resolution
// timestamp defect: the update used to write NULL over resolved_ns every time
// the lifecycle moved on, so a finding that had been resolved and then reopened
// lost the record that it had ever been fixed.
func TestResolutionTimestampsSurviveLaterTransitions(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	runID := applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)
	r := versionedResult("firewall_enabled", "1.0.0", assessment.Pass)
	if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{r}); err != nil {
		t.Fatal(err)
	}
	resolved := findingsFor(t, s, "firewall_enabled")
	if len(resolved) != 1 || resolved[0].ResolvedAt == nil {
		t.Fatalf("after a passing run findings = %+v, want one resolved finding with a timestamp", resolved)
	}
	firstResolution := *resolved[0].ResolvedAt

	runID = applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)
	reopened := findingsFor(t, s, "firewall_enabled")
	if len(reopened) != 1 || reopened[0].Lifecycle != assessment.LifecycleReopened {
		t.Fatalf("after the failure findings = %+v, want one reopened finding", reopened)
	}
	if reopened[0].ReopenedAt == nil {
		t.Fatal("a reopened finding must carry its reopening timestamp")
	}
	if reopened[0].ResolvedAt == nil {
		t.Fatal("the earlier resolution timestamp was erased when the finding reopened")
	}
	if !reopened[0].ResolvedAt.Equal(firstResolution) {
		t.Fatalf("resolution timestamp changed from %v to %v", firstResolution, reopened[0].ResolvedAt)
	}
	_ = runID
}

// TestSupersededFilterIsQueryable keeps the new state reachable through the same
// bounded query surface as the others.
func TestSupersededFilterIsQueryable(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	applyVersionedRun(t, s, "firewall_enabled", "1.0.0", assessment.Fail)
	applyVersionedRun(t, s, "firewall_enabled", "2.0.0", assessment.Fail)

	superseded, err := s.Findings(ctx, "superseded", 0, 10)
	if err != nil {
		t.Fatalf("the superseded filter must be accepted: %v", err)
	}
	if len(superseded) != 1 || superseded[0].CheckVersion != "1.0.0" {
		t.Fatalf("superseded page = %+v, want the retired rule instance", superseded)
	}
	if _, err := s.Findings(ctx, "retired", 0, 10); err == nil {
		t.Fatal("an unknown lifecycle filter must still be rejected")
	}
	// The history the dashboard renders for a superseded finding must be
	// reachable by its fingerprint.
	if _, err := s.Finding(ctx, superseded[0].Fingerprint); err != nil {
		t.Fatalf("the superseded finding is not retrievable: %v", err)
	}
}
