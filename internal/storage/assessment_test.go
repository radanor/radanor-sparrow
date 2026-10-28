package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

func checkResult(id string, result assessment.Result, values ...assessment.Value) assessment.CheckResult {
	return assessment.CheckResult{
		CheckID: id, CheckVersion: "1.0.0", Category: "test", Title: id, Platforms: []string{"ubuntu"},
		Result: result, Severity: assessment.SeverityHigh, Detail: "detail for " + id,
		Explanation: "explanation", Action: "action", VerifyMethod: "verify",
		Observation: assessment.Observation{
			ID: id, Collector: id, Platform: "ubuntu", CollectedAt: time.Now(), Status: assessment.Collected, Source: "test", Values: values,
		},
		CollectedAt: time.Now(), EvaluatedAt: time.Now(), Fresh: true,
	}
}

// v1 schema creation, used to prove the CE-5 migration preserves CE-4 data.
func createV1Database(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE events(id INTEGER PRIMARY KEY, event_id TEXT NOT NULL, source TEXT NOT NULL, event_type TEXT NOT NULL, platform TEXT NOT NULL, occurred_ns INTEGER NOT NULL, observed_ns INTEGER NOT NULL, severity TEXT NOT NULL, evidence TEXT NOT NULL, schema_version TEXT NOT NULL, source_sequence TEXT NOT NULL, metadata_json BLOB NOT NULL, UNIQUE(source,event_id))`,
		`CREATE INDEX events_observed_idx ON events(observed_ns DESC,id DESC)`,
		`CREATE TABLE checkpoints(source TEXT PRIMARY KEY, value BLOB NOT NULL CHECK(length(value)<=32768), updated_ns INTEGER NOT NULL)`,
		`CREATE TABLE monitoring_gaps(id INTEGER PRIMARY KEY, source TEXT NOT NULL, start_ns INTEGER NOT NULL, end_ns INTEGER NOT NULL, reason TEXT NOT NULL CHECK(length(reason)<=256), recorded_ns INTEGER NOT NULL)`,
		`CREATE INDEX gaps_recorded_idx ON monitoring_gaps(recorded_ns DESC,id DESC)`,
		`PRAGMA user_version=1`,
	}
	for _, q := range statements {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("v1 setup %q: %v", q, err)
		}
	}
	now := time.Now().UnixNano()
	if _, err := db.Exec(`INSERT INTO events(event_id,source,event_type,platform,occurred_ns,observed_ns,severity,evidence,schema_version,source_sequence,metadata_json) VALUES('e1','native.file_integrity','file.modified','linux',?,?,?,?,?,?,?)`,
		now, now, "info", "telemetry", "shield.event/v1", "seq-1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO checkpoints(source,value,updated_ns) VALUES('native.file_integrity',?,?)`, []byte("cp-1"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO monitoring_gaps(source,start_ns,end_ns,reason,recorded_ns) VALUES('native.file_integrity',?,?,?,?)`, now, now, "overflow", now); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationPreservesMonitoringEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	createV1Database(t, path)

	s, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("open after v1: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 1 {
		t.Fatalf("events = %d, want 1 (migration must not touch monitoring data)", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM checkpoints"); n != 1 {
		t.Fatalf("checkpoints = %d, want 1", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM monitoring_gaps"); n != 1 {
		t.Fatalf("gaps = %d, want 1", n)
	}
	if err := s.Integrity(ctx); err != nil {
		t.Fatalf("integrity: %v", err)
	}
	// Re-opening must be idempotent.
	s.Close()
	again, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if n := countRows(t, again, "SELECT count(*) FROM assessment_runs"); n != 0 {
		t.Fatalf("assessment_runs = %d after a fresh migration, want 0", n)
	}
}

func TestLatestObservationUsesNewestCompletedRun(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	save := func(status string, when time.Time, pretty string, includeObservation bool) int64 {
		t.Helper()
		id, err := s.StartRun(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		if includeObservation {
			obs := assessment.Observation{
				ID: "platform", Collector: "platform", Platform: "ubuntu", CollectedAt: when,
				Status: assessment.Collected, Source: "/etc/os-release",
				Values: []assessment.Value{{Key: "pretty_name", Kind: assessment.ValueString, Text: pretty}},
			}
			if err := s.SaveObservations(ctx, id, []assessment.Observation{obs}); err != nil {
				t.Fatal(err)
			}
		}
		if status != "running" {
			if err := s.FinishRun(ctx, id, assessment.ScanOutcome{Status: status, StartedAt: when, FinishedAt: when.Add(time.Second)}); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	save("completed", time.Now().Add(-time.Minute), "Ubuntu completed", true)
	save("running", time.Now(), "Ubuntu unfinished", true)

	snapshot, err := s.LatestObservation(ctx, "platform")
	if err != nil || !snapshot.Found || !snapshot.CompletedAssessment {
		t.Fatalf("LatestObservation = %+v, %v", snapshot, err)
	}
	if len(snapshot.Observation.Values) != 1 || snapshot.Observation.Values[0].Text != "Ubuntu completed" {
		t.Fatalf("latest completed observation = %+v", snapshot.Observation)
	}

	// The newest completed run is the evidence boundary. It must not borrow a
	// fresh platform observation from an older completed assessment, and the
	// caller must still be able to tell "recorded nothing" from "nothing ran".
	save("completed", time.Now(), "", false)
	snapshot, err = s.LatestObservation(ctx, "platform")
	if err != nil || snapshot.Found || !snapshot.CompletedAssessment {
		t.Fatalf("newest completed run without platform = %+v err=%v", snapshot, err)
	}

	// Cancelled, failed and unfinished runs never become the evidence boundary.
	save("cancelled", time.Now(), "Ubuntu cancelled", true)
	save("failed", time.Now(), "Ubuntu failed", true)
	save("running", time.Now(), "Ubuntu running", true)
	snapshot, err = s.LatestObservation(ctx, "platform")
	if err != nil || snapshot.Found || !snapshot.CompletedAssessment {
		t.Fatalf("non-completed observations = %+v err=%v", snapshot, err)
	}
	snapshot, err = s.LatestObservation(ctx, "missing")
	if err != nil || snapshot.Found || !snapshot.CompletedAssessment {
		t.Fatalf("missing observation = %+v err=%v", snapshot, err)
	}
	if _, err := s.LatestObservation(ctx, ""); err == nil {
		t.Fatal("empty collector accepted")
	}
}

func TestLatestObservationDistinguishesAbsentEvidenceFromAbsentHistory(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	// Nothing has ever run: there is no evidence boundary at all.
	empty, err := s.LatestObservation(ctx, "platform")
	if err != nil || empty.Found || empty.CompletedAssessment {
		t.Fatalf("empty history = %+v err=%v", empty, err)
	}

	// An assessment that is still running is not an evidence boundary either.
	id, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.LatestObservation(ctx, "platform")
	if err != nil || running.Found || running.CompletedAssessment {
		t.Fatalf("running-only history = %+v err=%v", running, err)
	}

	// A completed assessment that recorded no platform observation is a
	// different answer: the boundary exists, the evidence does not.
	when := time.Now()
	if err := s.FinishRun(ctx, id, assessment.ScanOutcome{Status: "completed", StartedAt: when, FinishedAt: when.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	none, err := s.LatestObservation(ctx, "platform")
	if err != nil || none.Found || !none.CompletedAssessment {
		t.Fatalf("completed run without the collector = %+v err=%v", none, err)
	}
	if none.Observation.Collector != "" || len(none.Observation.Values) != 0 {
		t.Fatalf("absent evidence was invented: %+v", none.Observation)
	}
}

func TestLatestObservationRejectsMalformedStoredObservation(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	id, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO observations(run_id,collector,platform,collected_ns,status,source,values_json,error) VALUES(?,?,?,?,?,?,?,?)`, id, "platform", "ubuntu", when.UnixNano(), "collected", "legacy", []byte(`{"broken"`), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, id, assessment.ScanOutcome{Status: "completed", StartedAt: when, FinishedAt: when.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestObservation(ctx, "platform"); err == nil {
		t.Fatal("malformed stored observation was accepted")
	}
}

func TestFindingsLifecycleAcrossScans(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	run := func(results ...assessment.CheckResult) int64 {
		t.Helper()
		id, err := s.StartRun(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveObservations(ctx, id, []assessment.Observation{results[0].Observation}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveCheckResults(ctx, id, results); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishRun(ctx, id, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		return id
	}

	failing := checkResult("firewall_enabled", assessment.Fail, assessment.Value{Key: "tools_active", Kind: assessment.ValueInt, Int: int64Ptr(0)})
	run1 := run(failing)
	newCount, openCount, err := s.ApplyFindings(ctx, run1, []assessment.CheckResult{failing})
	if err != nil {
		t.Fatal(err)
	}
	if newCount != 1 || openCount != 1 {
		t.Fatalf("first run: new=%d open=%d, want 1/1", newCount, openCount)
	}
	found, err := s.Findings(ctx, "", 0, 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("findings = %d (%v), want 1", len(found), err)
	}
	if found[0].Lifecycle != assessment.LifecycleNew {
		t.Fatalf("lifecycle = %q, want new", found[0].Lifecycle)
	}
	fingerprint := found[0].Fingerprint

	// Second scan, same failure: continuing, and not a new finding.
	run2 := run(failing)
	if n, open, _ := s.ApplyFindings(ctx, run2, []assessment.CheckResult{failing}); n != 0 || open != 1 {
		t.Fatalf("second run: new=%d open=%d, want 0/1", n, open)
	}
	if f, _ := s.Finding(ctx, fingerprint); f.Lifecycle != assessment.LifecycleContinuing {
		t.Fatalf("lifecycle = %q, want continuing", f.Lifecycle)
	}

	// Third scan, UNKNOWN: the finding must not be resolved.
	unknown := checkResult("firewall_enabled", assessment.Unknown)
	unknown.Observation.Status = assessment.PermissionDenied
	run3 := run(unknown)
	if _, _, err := s.ApplyFindings(ctx, run3, []assessment.CheckResult{unknown}); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Finding(ctx, fingerprint); f.Lifecycle != assessment.LifecycleContinuing {
		t.Fatalf("an unknown result resolved the finding; lifecycle = %q", f.Lifecycle)
	}

	// Fourth scan, fresh PASS: resolved, with the resolving evidence recorded.
	passing := checkResult("firewall_enabled", assessment.Pass, assessment.Value{Key: "tools_active", Kind: assessment.ValueInt, Int: int64Ptr(1)})
	run4 := run(passing)
	if _, open, _ := s.ApplyFindings(ctx, run4, []assessment.CheckResult{passing}); open != 0 {
		t.Fatalf("open findings after a pass = %d, want 0", open)
	}
	resolved, _ := s.Finding(ctx, fingerprint)
	if resolved.Lifecycle != assessment.LifecycleResolved || resolved.ResolvedAt == nil {
		t.Fatalf("lifecycle = %q resolved_at=%v, want resolved with a timestamp", resolved.Lifecycle, resolved.ResolvedAt)
	}

	// Fifth scan, failure again: reopened, not duplicated.
	run5 := run(failing)
	if n, open, _ := s.ApplyFindings(ctx, run5, []assessment.CheckResult{failing}); n != 0 || open != 1 {
		t.Fatalf("reopen run: new=%d open=%d, want 0/1", n, open)
	}
	reopened, _ := s.Finding(ctx, fingerprint)
	if reopened.Lifecycle != assessment.LifecycleReopened || reopened.ReopenedAt == nil {
		t.Fatalf("lifecycle = %q reopened_at=%v, want reopened", reopened.Lifecycle, reopened.ReopenedAt)
	}
	if n := countRows(t, s, "SELECT count(*) FROM findings"); n != 1 {
		t.Fatalf("findings = %d, want exactly one fingerprint across all scans", n)
	}

	history, err := s.FindingHistory(ctx, fingerprint, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 4 {
		t.Fatalf("history entries = %d, want at least 4 transitions", len(history))
	}
}

func TestCheckVersionChangeDoesNotResolveAnExistingFinding(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	failing := checkResult("c", assessment.Fail)
	runID, _ := s.StartRun(ctx, "manual")
	if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{failing}); err != nil {
		t.Fatal(err)
	}
	found, _ := s.Findings(ctx, "", 0, 10)
	fingerprint := found[0].Fingerprint

	newVersion := failing
	newVersion.CheckVersion = "2.0.0"
	// A changed rule produces a different fingerprint and must not resolve the
	// finding from the older rule.
	if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{newVersion}); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Finding(ctx, fingerprint); f.Lifecycle == assessment.LifecycleResolved {
		t.Fatal("a rule version change resolved a finding it did not re-evaluate")
	}
}

func TestReconcileInterruptedRuns(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	runID, err := s.StartRun(ctx, "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.ReconcileInterruptedRuns(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reconciled = %d (%v), want 1", n, err)
	}
	if active, _ := s.ActiveRun(ctx); active != nil {
		t.Fatal("an interrupted run is still reported as active")
	}
	runs, err := s.Runs(ctx, 0, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %d (%v), want 1", len(runs), err)
	}
	if runs[0].Status != "interrupted" || runs[0].ID != runID {
		t.Fatalf("run status = %q id=%d, want interrupted/%d", runs[0].Status, runs[0].ID, runID)
	}
}

func TestAssessmentQueriesAndRetention(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id, _ := s.StartRun(ctx, "manual")
		result := checkResult("c", assessment.Fail)
		if err := s.SaveObservations(ctx, id, []assessment.Observation{result.Observation}); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveCheckResults(ctx, id, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
		outcome := assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now(), ChecksTotal: 1, Failed: 1}
		if err := s.FinishRun(ctx, id, outcome); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ApplyFindings(ctx, id, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.Runs(ctx, 0, 3)
	if err != nil || len(runs) != 3 {
		t.Fatalf("page = %d (%v), want 3", len(runs), err)
	}
	next, err := s.Runs(ctx, runs[len(runs)-1].ID, 3)
	if err != nil || len(next) != 2 {
		t.Fatalf("second page = %d (%v), want 2", len(next), err)
	}
	results, runID, err := s.LatestResults(ctx, 50)
	if err != nil || len(results) != 1 || runID != runs[0].ID {
		t.Fatalf("latest results = %d run=%d (%v)", len(results), runID, err)
	}
	if _, err := s.Runs(ctx, -1, 3); err == nil {
		t.Fatal("a negative cursor must be rejected")
	}
	if _, err := s.Runs(ctx, 0, 9999); err == nil {
		t.Fatal("an oversized page must be rejected")
	}
	summary, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.HasRun || summary.OpenFindings != 1 || summary.FailedChecks != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	// Retention keeps the newest runs and cascades their children.
	if err := s.RetainAssessments(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM assessment_runs"); n != 2 {
		t.Fatalf("runs after retention = %d, want 2", n)
	}
	if n := countRows(t, s, "SELECT count(*) FROM check_results"); n != 2 {
		t.Fatalf("check_results after retention = %d, want 2 (cascade)", n)
	}
	// Monitoring evidence is never touched by assessment retention.
	if n := countRows(t, s, "SELECT count(*) FROM events"); n != 0 {
		t.Fatalf("events = %d, want 0", n)
	}
}

func TestAssessmentInputsAreBoundedAndParameterized(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	// A SQL-injection attempt in the lifecycle filter must be rejected, not
	// executed.
	if _, err := s.Findings(ctx, "resolved' OR 1=1 --", 0, 10); err == nil {
		t.Fatal("an unknown lifecycle filter must be rejected")
	}
	if _, err := s.Finding(ctx, "not-a-fingerprint"); err == nil {
		t.Fatal("a malformed fingerprint must be rejected")
	}
	if _, err := s.Findings(ctx, "", 0, 100000); err == nil {
		t.Fatal("an oversized page must be rejected")
	}
	// A legitimate filter still works.
	if _, err := s.Findings(ctx, "resolved", 0, 10); err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
}

func TestMonitoringWritesRemainUsableDuringAssessmentWrites(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	runID, err := s.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	result := checkResult("c", assessment.Fail)
	// Interleave monitoring ingestion with assessment writes; both must succeed
	// and neither may deadlock or discard evidence.
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 25; i++ {
			ev := testEvent(string(rune('a'+i%26))+"-"+time.Now().Format("150405.000000000"), "native.file_integrity", []byte("cp"))
			if err := s.Process(context.Background(), ev); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 5; i++ {
		if err := s.SaveCheckResults(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ApplyFindings(ctx, runID, []assessment.CheckResult{result}); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("monitoring write failed during assessment: %v", err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM events"); n == 0 {
		t.Fatal("monitoring events were discarded during an assessment")
	}
}

func int64Ptr(v int64) *int64 { return &v }
