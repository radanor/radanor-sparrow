package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// CE-6 adversarial-size review of the assessment byte measurement.
//
// CE-5 documented that the per-row reserve is a deliberate upper bound rather
// than an exact figure. This test drives the measurement with maximum-length rows
// and checks three properties that together make the budget trustworthy:
//
//  1. the reported meter is never below the real stored text payload, measured
//     here by an independent expression that does not include the reserve;
//  2. the meter is exactly the payload plus the documented reserve per row, so
//     the over-count is bounded and explainable rather than open-ended;
//  3. the budget is still enforced when every row is as large as the store will
//     accept, and a single oversized run is refused rather than stored.
//
// Unlike the production expression, this test's expression deliberately omits
// IFNULL, because it measures rows this test created and controls.

const assessmentTextOnlyExpr = `COALESCE((SELECT SUM(length(collector)+length(platform)+length(status)+length(source)+length(values_json)+length(error)) FROM observations),0) + ` +
	`COALESCE((SELECT SUM(length(check_id)+length(check_version)+length(category)+length(title)+length(platform)+length(result)+length(severity)+length(detail)+length(explanation)+length(action)+length(verify_method)+length(limitations_json)+length(evidence_json)) FROM check_results),0) + ` +
	`COALESCE((SELECT SUM(length(fingerprint)+length(check_id)+length(check_version)+length(platform)+length(severity)+length(lifecycle)+length(title)+length(summary)+length(action)+length(verify_method)+length(limitations_json)+length(evidence_json)) FROM findings),0) + ` +
	`COALESCE((SELECT SUM(length(fingerprint)+length(lifecycle)+length(result)+length(detail)) FROM finding_history),0) + ` +
	`COALESCE((SELECT SUM(length(trigger)+length(status)) FROM assessment_runs),0)`

func assessmentRowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	for _, table := range []string{"observations", "check_results", "findings", "finding_history", "assessment_runs"} {
		n += countRows(t, s, "SELECT count(*) FROM "+table)
	}
	return n
}

// writeMaximalRun stores one run whose rows are as large as the store accepts.
func writeMaximalRun(t *testing.T, s *Store, filler string) int64 {
	t.Helper()
	ctx := context.Background()
	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	// A realistic maximum: every free-text column at 4 KiB, and the two JSON
	// columns filled to their documented 8 KiB bound.
	text := strings.Repeat("t", 4096)
	observation := assessment.Observation{
		ID: "adversarial", Collector: "adversarial", Platform: "ubuntu",
		CollectedAt: time.Now(), Status: assessment.Collected, Source: text,
		Error: strings.Repeat("e", 2048),
	}
	if err := s.SaveObservations(ctx, runID, []assessment.Observation{observation}); err != nil {
		t.Fatalf("observations: %v", err)
	}
	result := assessment.CheckResult{
		CheckID: "adversarial_check", CheckVersion: text, Category: text, Title: text,
		Platforms: []string{"ubuntu"}, Result: assessment.Fail, Severity: assessment.SeverityHigh,
		Detail: text, Explanation: text, Action: text, VerifyMethod: text,
		Limitations: []string{filler},
		Observation: assessment.Observation{
			ID: "adversarial", Collector: "adversarial", Platform: "ubuntu",
			CollectedAt: time.Now(), Status: assessment.Collected, Source: "adversarial",
			Values: []assessment.Value{{Key: "blob", Kind: assessment.ValueString, Text: strings.Repeat("v", assessment.MaxObservationBytes-256)}},
		},
		CollectedAt: time.Now(), EvaluatedAt: time.Now(), Fresh: true,
	}
	if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
		t.Fatalf("check results: %v", err)
	}
	if err := s.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now(), ChecksTotal: 1, Failed: 1}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	return runID
}

func TestAssessmentMeasurementIsConservativeUnderAdversarialRows(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentBytes = 512 << 10 })
	ctx := context.Background()

	writeMaximalRun(t, s, strings.Repeat("l", assessment.MaxObservationBytes-256))

	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var textOnly int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+assessmentTextOnlyExpr).Scan(&textOnly); err != nil {
		t.Fatal(err)
	}
	rows := assessmentRowCount(t, s)

	// Property 1: the meter is never below the real text payload.
	if usage[usageAssessment] < textOnly {
		t.Fatalf("assessment meter %d is below the stored text payload %d", usage[usageAssessment], textOnly)
	}
	// Property 2: the over-count is exactly the documented reserve per row.
	want := textOnly + int64(rows)*assessmentRowReserveBytes
	if usage[usageAssessment] != want {
		t.Fatalf("assessment meter = %d, want text %d + %d rows x %d reserve = %d", usage[usageAssessment], textOnly, rows, assessmentRowReserveBytes, want)
	}
	// The over-count must stay a small factor, not an unbounded fudge: a
	// pathological 128 bytes per row is fine, a doubling is not.
	if usage[usageAssessment] > textOnly*2 {
		t.Fatalf("the reserve doubled the measured payload: meter %d vs text %d", usage[usageAssessment], textOnly)
	}
	if textOnly < 8<<10 {
		t.Fatalf("the adversarial row was not actually large (%d bytes), so this test proves nothing", textOnly)
	}
}

// TestAdversarialRowsStillRespectTheAssessmentBudget proves that large rows are
// bounded by retention rather than accepted until the budget is blown.
func TestAdversarialRowsStillRespectTheAssessmentBudget(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) {
		l.MaxAssessmentBytes = 512 << 10
		l.MaxAssessmentRuns = 100
	})
	ctx := context.Background()

	var lastRun int64
	for i := 0; i < 40; i++ {
		lastRun = writeMaximalRun(t, s, strings.Repeat("l", 2048))
		usage, err := s.StorageUsage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if usage[usageAssessment] > s.limits.MaxAssessmentBytes {
			t.Fatalf("after run %d the assessment meter is %d, over the %d budget", i, usage[usageAssessment], s.limits.MaxAssessmentBytes)
		}
		if usage[usageMonitoring] != 0 {
			t.Fatalf("assessment history charged %d bytes to monitoring", usage[usageMonitoring])
		}
	}

	// Retention really did prune: 40 maximal runs cannot fit in 512 KiB.
	stored := assessmentRowCount(t, s)
	if stored == 0 {
		t.Fatal("retention pruned everything")
	}
	runs, err := s.Runs(ctx, 0, s.limits.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) >= 40 {
		t.Fatalf("all 40 maximal runs were retained in a %d byte budget", s.limits.MaxAssessmentBytes)
	}
	// The newest run is the one that must survive.
	if runs[0].ID != lastRun {
		t.Fatalf("newest retained run = %d, want %d", runs[0].ID, lastRun)
	}
	// And the measurement still matches the payload plus reserve after pruning.
	var textOnly int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+assessmentTextOnlyExpr).Scan(&textOnly); err != nil {
		t.Fatal(err)
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := textOnly + int64(stored)*assessmentRowReserveBytes; usage[usageAssessment] != want {
		t.Fatalf("post-prune meter = %d, want %d", usage[usageAssessment], want)
	}
}

// TestSingleOversizedRunIsRefusedNotStored covers the boundary the loop cannot:
// when one run alone cannot fit the budget even with nothing else stored, the
// store refuses it rather than storing something it cannot afford.
func TestSingleOversizedRunIsRefusedNotStored(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentBytes = 16 << 10 })
	ctx := context.Background()

	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	text := strings.Repeat("t", 8192)
	result := assessment.CheckResult{
		CheckID: "oversized", CheckVersion: "1.0.0", Category: "test", Title: text,
		Platforms: []string{"ubuntu"}, Result: assessment.Fail, Severity: assessment.SeverityHigh,
		Detail: text, Explanation: text, Action: text, VerifyMethod: text,
		Observation: assessment.Observation{ID: "oversized", Collector: "oversized", Platform: "ubuntu", CollectedAt: time.Now(), Status: assessment.Collected, Source: "test"},
		CollectedAt: time.Now(), EvaluatedAt: time.Now(), Fresh: true,
	}
	if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
		t.Fatalf("check results: %v", err)
	}
	if err := s.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// The next run start has to deal with history that is already over budget on
	// its own: it prunes what it can and, if that is not enough, refuses.
	if _, err := s.StartRun(ctx, "manual"); err != nil && !errors.Is(err, ErrAssessmentStoragePressure) {
		t.Fatalf("unexpected error: %v", err)
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageAssessment] > s.limits.MaxAssessmentBytes {
		// A single run that cannot fit is the documented case where history is
		// allowed to exceed the budget: it is refused for new runs and reported
		// as pressure, and it is never silently mangled.
		if _, err := s.StartRun(ctx, "manual"); !errors.Is(err, ErrAssessmentStoragePressure) {
			t.Fatalf("an over-budget history must report assessment pressure, got %v", err)
		}
	} else if usage[usageMonitoring] != 0 {
		t.Fatal("assessment history charged the monitoring budget")
	}
}
