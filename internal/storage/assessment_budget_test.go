package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These tests pin the CE-5 review's second high-severity finding. Ingestion
// used to be gated on the size of the whole database file, which also holds
// posture-assessment history, and a SQLite file does not shrink when rows are
// deleted. Assessment history could therefore push the file past the monitoring
// budget and leave every later event refused - permanently, because retention
// freed rows but never shrank the file.
//
// bigResult builds a check result whose evidence is close to the per-record
// limit, so a handful of runs can exceed a small assessment budget without
// exceeding any single-record bound.
func bigResult(id string) assessment.CheckResult {
	r := checkResult(id, assessment.Fail)
	for i := 0; i < 12; i++ {
		r.Observation.Values = append(r.Observation.Values,
			assessment.Value{Key: fmt.Sprintf("pad%d", i), Kind: assessment.ValueString, Text: strings.Repeat("x", 500)})
	}
	return r
}

// TestAssessmentHistoryCannotExhaustTheMonitoringBudget is the direct
// reproduction of the reported failure: the total database file is larger than
// the monitoring budget solely because of assessment history, and monitoring
// ingestion plus checkpoint advancement must still work.
func TestAssessmentHistoryCannotExhaustTheMonitoringBudget(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) {
		// A monitoring budget far smaller than the assessment history we are
		// about to write, so the old whole-file gate would refuse everything.
		l.MaxBytes = 8 << 10
		l.MaxAssessmentBytes = 1 << 20
		l.MaxAssessmentRuns = 500
	})
	ctx := context.Background()

	// Fill the store with genuine assessment history: runs, observations,
	// results and findings.
	for i := 0; i < 20; i++ {
		runID, err := s.StartRun(ctx, "manual")
		if err != nil {
			t.Fatalf("run %d refused: %v", i, err)
		}
		result := bigResult(fmt.Sprintf("check_%02d", i))
		if err := s.SaveObservations(ctx, runID, []assessment.Observation{result.Observation}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
		outcome := assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now(), ChecksTotal: 1, Failed: 1}
		if err := s.FinishRun(ctx, runID, outcome); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
	}

	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.DatabaseBytes <= info.MaxDatabaseBytes {
		t.Fatalf("reproduction precondition not met: file %d bytes must exceed the monitoring budget %d",
			info.DatabaseBytes, info.MaxDatabaseBytes)
	}
	used, err := s.assessmentBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if used == 0 {
		t.Fatal("assessment byte meter reported no usage after writing history")
	}

	// Monitoring ingestion must continue, and checkpoints must still advance.
	for i := 0; i < 25; i++ {
		ev := testEvent(fmt.Sprintf("m%d", i), "native.file_integrity", []byte(fmt.Sprintf("cp-%03d", i)))
		if err := s.Process(ctx, ev); err != nil {
			t.Fatalf("monitoring event %d refused while assessment history was present: %v", i, err)
		}
	}
	events, err := s.Recent(ctx, Query{Limit: 50})
	if err != nil || len(events) != 25 {
		t.Fatalf("stored monitoring events = %d (%v), want 25", len(events), err)
	}
	// The checkpoint written alongside the last event must be the latest one:
	// assessment pressure must not freeze checkpoint advancement.
	value, err := s.Load(ctx, "native.file_integrity")
	if err != nil || string(value) != "cp-024" {
		t.Fatalf("checkpoint = %q (%v), want cp-024", value, err)
	}
	// Monitoring evidence really is charged against its own budget, so the gate
	// was relocated rather than removed.
	monitored, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil || monitored == 0 {
		t.Fatalf("monitoring meter = %d (%v), want a positive charge", monitored, err)
	}
}

// TestMonitoringBudgetIsStillEnforced proves the correction did not simply drop
// the ingestion gate: a genuinely exhausted monitoring budget still refuses
// writes.
func TestMonitoringBudgetIsStillEnforced(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxBytes = 1; l.MaxAssessmentBytes = 1 << 20 })
	if err := s.Process(context.Background(), testEvent("e", "a", nil)); !errors.Is(err, ErrStoragePressure) {
		t.Fatalf("pressure error = %v, want ErrStoragePressure", err)
	}
}

// TestAssessmentPressureIsScopedAndReported proves the two pressures are
// distinguishable, that assessment pressure never masquerades as monitoring
// pressure, that monitoring keeps working while it holds, and that a confirmed
// weakness is not silently forgotten because the rule that found it is no
// longer in the retained window.
func TestAssessmentPressureIsScopedAndReported(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentBytes = 1; l.MaxAssessmentRuns = 500 })
	ctx := context.Background()

	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatalf("first run refused: %v", err)
	}
	result := checkResult("ssh_permit_empty_passwords_disabled", assessment.Fail)
	if err := s.SaveObservations(ctx, runID, []assessment.Observation{result.Observation}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{result}); err != nil {
		t.Fatal(err)
	}

	_, err = s.StartRun(ctx, "manual")
	if err == nil {
		t.Fatal("a second run was accepted although the assessment budget is exhausted")
	}
	if !errors.Is(err, ErrAssessmentStoragePressure) {
		t.Fatalf("error = %v, want ErrAssessmentStoragePressure", err)
	}
	if errors.Is(err, ErrStoragePressure) {
		t.Fatal("assessment pressure leaked out as monitoring pressure")
	}

	// Monitoring is entirely unaffected while assessment pressure holds.
	for i := 0; i < 5; i++ {
		if err := s.Process(ctx, testEvent(fmt.Sprintf("keep%d", i), "a", []byte("cp"))); err != nil {
			t.Fatalf("monitoring event %d refused under assessment pressure: %v", i, err)
		}
	}

	// The confirmed weakness survives: refusing history is not the same as
	// resolving it.
	findings, err := s.Findings(ctx, "", 0, 10)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings = %d (%v), want the weakness retained", len(findings), err)
	}
	if findings[0].Lifecycle != assessment.LifecycleNew {
		t.Fatalf("finding lifecycle = %q, want it left open", findings[0].Lifecycle)
	}

	// The degradation is surfaced rather than silently returning an empty page.
	summary, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.StoragePressure {
		t.Fatalf("summary did not report assessment storage pressure: %+v", summary)
	}
	if summary.StorageMaxBytes != 1 {
		t.Fatalf("summary storage max = %d, want 1", summary.StorageMaxBytes)
	}

	// Growing the budget lets the next run through: the state is recoverable.
	s.limits.MaxAssessmentBytes = 1 << 20
	if _, err := s.StartRun(ctx, "manual"); err != nil {
		t.Fatalf("a run was still refused after the budget was raised: %v", err)
	}
}

// TestRetentionReleasesAssessmentBytesAndTruncatesTheWAL covers the growth and
// cleanup behaviour the review asked for: retained history is really released
// from the budget, and the WAL sidecar that absorbed the burst is truncated
// instead of holding the database at its peak size.
func TestRetentionReleasesAssessmentBytesAndTruncatesTheWAL(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentBytes = 1 << 20; l.MaxAssessmentRuns = 500 })
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		runID, err := s.StartRun(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		result := bigResult(fmt.Sprintf("c%d", i))
		if err := s.SaveObservations(ctx, runID, []assessment.Observation{result.Observation}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.assessmentBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	walBefore := fileSize(t, s.path+"-wal")

	if err := s.RetainAssessments(ctx, 2); err != nil {
		t.Fatal(err)
	}
	after, err := s.assessmentBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after >= before {
		t.Fatalf("assessment bytes after retention = %d, want less than %d", after, before)
	}
	if n := countRows(t, s, "SELECT count(*) FROM assessment_runs"); n != 2 {
		t.Fatalf("runs after retention = %d, want 2", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM observations"); n != 2 {
		t.Fatalf("observations after retention = %d, want 2", n)
	}
	// Monitoring evidence is never touched by assessment retention.
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 0 {
		t.Fatalf("events after assessment retention = %d, want 0", n)
	}
	if walAfter := fileSize(t, s.path+"-wal"); walAfter > walBefore {
		t.Fatalf("WAL grew during retention: %d -> %d", walBefore, walAfter)
	}
	if err := s.Integrity(ctx); err != nil {
		t.Fatalf("integrity after retention: %v", err)
	}
}

// TestMonitoringRetentionReleasesItsOwnBudget proves the meter is decremented
// when monitoring data is retained, so ingestion recovers instead of staying
// wedged at the budget ceiling.
func TestMonitoringRetentionReleasesItsOwnBudget(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxBytes = 8 << 10; l.MaxAssessmentBytes = 1 << 20 })
	ctx := context.Background()
	written := 0
	for i := 0; i < 200; i++ {
		ev := testEvent(fmt.Sprintf("e%d", i), "a", nil)
		if err := s.Process(ctx, ev); err != nil {
			break
		}
		written++
	}
	if written == 0 {
		t.Fatal("no monitoring events were accepted")
	}
	if err := s.Process(ctx, testEvent("one-too-many", "a", nil)); !errors.Is(err, ErrStoragePressure) {
		t.Fatalf("expected the monitoring budget to be reached, got %v", err)
	}
	meterFull, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil || meterFull == 0 {
		t.Fatalf("monitoring meter = %d (%v)", meterFull, err)
	}
	if err := s.Retain(ctx); err != nil {
		t.Fatal(err)
	}
	meterAfter, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil {
		t.Fatal(err)
	}
	if meterAfter >= meterFull {
		t.Fatalf("monitoring meter = %d after retention, want less than %d", meterAfter, meterFull)
	}
	// Ingestion recovers once room has been made.
	if err := s.Process(ctx, testEvent("after-retain", "a", nil)); err != nil {
		t.Fatalf("monitoring ingestion did not recover after retention: %v", err)
	}
}

// TestMigratedDatabaseSeedsItsMeters proves an upgraded database charges its
// existing content rather than starting from zero and under-enforcing.
func TestMigratedDatabaseSeedsItsMeters(t *testing.T) {
	path := t.TempDir() + "/monitoring.db"
	createV1Database(t, path)
	s, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	monitored, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil {
		t.Fatal(err)
	}
	// The v1 fixture stores an event whose evidence is "telemetry" and whose
	// metadata is "{}".
	want := int64(len("telemetry") + len(`{}`))
	if monitored != want {
		t.Fatalf("seeded monitoring meter = %d, want %d", monitored, want)
	}
	if assessed, err := usageBytes(ctx, s.db, usageAssessment); err != nil || assessed != 0 {
		t.Fatalf("seeded assessment meter = %d (%v), want 0", assessed, err)
	}
}

// TestReplayedEventIsNotChargedTwice guards the meter against a replay storm
// inflating the budget and refusing later evidence. It also pins the CE-6 M-2
// distinction: a genuine replay is ignored without charge, while a second event
// that reuses an identity with different content is refused as a collision and
// reported rather than being absorbed as a harmless repeat.
func TestReplayedEventIsNotChargedTwice(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentBytes = 1 << 20 })
	ctx := context.Background()
	ev := testEvent("replay", "a", []byte("cp-1"))
	for i := 0; i < 5; i++ {
		// The same observation arriving again: only the first stores a row, so
		// only the first may be charged to the budget.
		if _, err := s.ProcessEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	first, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 {
		t.Fatal("the stored event was not charged at all")
	}
	// A second event with the same identity but different content is not a
	// replay. It is refused, reported as a collision, and must not grow the meter
	// any more than a replay would.
	colliding := ev
	colliding.Checkpoint = []byte("cp-2")
	colliding.Metadata = map[string]string{"root": "ssh", "name": "authorized_keys_replaced"}
	outcome, err := s.ProcessEvent(ctx, colliding)
	if outcome != IngestCollision || !errors.Is(err, ErrEventIdentityCollision) {
		t.Fatalf("colliding event = (%q, %v), want a reported identity collision", outcome, err)
	}
	second, err := usageBytes(ctx, s.db, usageMonitoring)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("meter after a refused collision = %d, want it unchanged at %d", second, first)
	}
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	// The checkpoint still names the event that was actually stored.
	if value, lerr := s.Load(ctx, "a"); lerr != nil || string(value) != "cp-1" {
		t.Fatalf("checkpoint = %q (%v), want cp-1 of the stored event", value, lerr)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
