package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/monitoring"
)

func testEvent(id, source string, checkpoint []byte) monitoring.Event {
	now := time.Now()
	return monitoring.Event{
		ID:            id,
		Source:        source,
		Type:          "file.modified",
		Platform:      "linux",
		OccurredAt:    now.Add(-time.Millisecond),
		ObservedAt:    now,
		Severity:      monitoring.SeverityInfo,
		Evidence:      monitoring.EvidenceTelemetry,
		SchemaVersion: monitoring.EventSchemaV1,
		Metadata:      map[string]string{"root": "ssh", "name": "known_hosts"},
		Checkpoint:    checkpoint,
	}
}

func testLimits(mutate func(*Limits)) Limits {
	l := DefaultLimits()
	l.EventAge = time.Hour
	l.MaxEvents = 100
	l.MaxBytes = 64 << 20
	l.MaxGaps = 10
	l.MaxPageSize = 50
	if mutate != nil {
		mutate(&l)
	}
	return l
}

func openStore(t *testing.T, mutate func(*Limits)) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "monitoring.db")
	s, err := Open(path, testLimits(mutate))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return n
}

func TestOpenAppliesVersionedSchemaAndPermissions(t *testing.T) {
	s, path := openStore(t, nil)
	var version int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, currentSchemaVersion)
	}
	if err := s.Integrity(context.Background()); err != nil {
		t.Fatalf("integrity: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Fatalf("database permissions = %o, want 600", perm)
		}
		if _, err := os.Stat(path + "-wal"); err == nil {
			wal, err := os.Stat(path + "-wal")
			if err != nil {
				t.Fatal(err)
			}
			if perm := wal.Mode().Perm(); perm != 0600 {
				t.Fatalf("wal permissions = %o, want 600", perm)
			}
		}
	}
}

func TestOpenRejectsInvalidLimits(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "db"), Limits{}); err == nil {
		t.Fatal("zero limits accepted")
	}
}

// The event row and its checkpoint commit together: a checkpoint that cannot be
// written must roll the whole event back, so a replay-capable source never has
// an event without its replay position (or the reverse).
func TestEventAndCheckpointCommitAtomically(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	huge := testEvent("e1", "a", make([]byte, 32769))
	if err := s.Process(ctx, huge); err == nil {
		t.Fatal("oversized checkpoint accepted")
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 0 {
		t.Fatalf("rolled-back event persisted: %d rows", got)
	}
	if _, err := s.Load(ctx, "a"); !errors.Is(err, monitoring.ErrCheckpointNotFound) {
		t.Fatalf("checkpoint survived rollback: %v", err)
	}
	ok := testEvent("e1", "a", []byte("bookmark-1"))
	if err := s.Process(ctx, ok); err != nil {
		t.Fatalf("process: %v", err)
	}
	value, err := s.Load(ctx, "a")
	if err != nil || string(value) != "bookmark-1" {
		t.Fatalf("checkpoint = %q, %v", value, err)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
}

func TestMetadataTooLargeRejectedBeforeWrite(t *testing.T) {
	s, _ := openStore(t, nil)
	e := testEvent("big", "a", nil)
	e.Metadata = map[string]string{"blob": strings.Repeat("x", monitoring.MaxEventBytes)}
	if err := s.Process(context.Background(), e); err == nil {
		t.Fatal("oversized metadata accepted")
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 0 {
		t.Fatalf("event persisted after metadata rejection: %d", got)
	}
}

// Replay of an already-stored event must not create a second row but must keep
// advancing the checkpoint to the latest durably handled position.
//
// A replay is the *same observation arriving again*, so it is built from the
// event that was stored rather than by sampling the clock a second time. That
// distinction is what separates a replay from a colliding reuse of the identity,
// which the store now reports as an error instead of as a repeat.
func TestReplayDuplicateIgnoredCheckpointAdvances(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	first := testEvent("r1", "a", []byte("cp-1"))
	if err := s.Process(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testEvent("r2", "a", []byte("cp-2"))
	if err := s.Process(ctx, second); err != nil {
		t.Fatal(err)
	}
	outcome, err := s.ProcessEvent(ctx, second)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if outcome != IngestDuplicate {
		t.Fatalf("replay = %q, want duplicate", outcome)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 2 {
		t.Fatalf("replay duplicated rows: %d", got)
	}
	value, _ := s.Load(ctx, "a")
	if string(value) != "cp-2" {
		t.Fatalf("checkpoint = %q, want cp-2", value)
	}
}

// TestIngestOutcomeDistinguishesInsertFromDuplicate is the CE-6 M-2 guard. The
// defect it pins: storing an event and ignoring a duplicate both returned a nil
// error, so nothing downstream could tell new evidence from a repeat — a
// duplicate was reported as a successful ingestion.
func TestIngestOutcomeDistinguishesInsertFromDuplicate(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	first := testEvent("d1", "a", []byte("cp-1"))
	outcome, err := s.ProcessEvent(ctx, first)
	if err != nil || outcome != IngestInserted {
		t.Fatalf("first event = (%q, %v), want inserted", outcome, err)
	}
	before := monitoringBytes(t, s)

	// A genuine replay is the same observation arriving twice, so it carries the
	// occurrence time of the original rather than a freshly sampled one.
	replay := first
	outcome, err = s.ProcessEvent(ctx, replay)
	if err != nil {
		t.Fatalf("a replay must not be an error: %v", err)
	}
	if outcome != IngestDuplicate {
		t.Fatalf("replay = %q, want duplicate", outcome)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 1 {
		t.Fatalf("rows = %d, want the replay ignored", got)
	}
	// An ignored duplicate is not new evidence and must not be charged for.
	if after := monitoringBytes(t, s); after != before {
		t.Fatalf("monitoring meter moved from %d to %d on a replay", before, after)
	}

	stats := s.IngestStats()
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one source", stats)
	}
	if stats[0].Source != "a" || stats[0].Inserted != 1 || stats[0].Duplicates != 1 {
		t.Fatalf("counters = %+v, want a: one inserted and one duplicate", stats[0])
	}
	if stats[0].Collisions != 0 {
		t.Fatalf("a replay was counted as a collision: %+v", stats[0])
	}
}

// TestReplayDoesNotRewindTheCheckpoint covers the rule that an ignored insert
// never moves the source's position. A replay normally carries the checkpoint its
// original insert already recorded, but a source that replays from an older
// position would otherwise pull the durable checkpoint backwards.
func TestReplayDoesNotRewindTheCheckpoint(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.Process(ctx, testEvent("e1", "a", []byte("cp-1"))); err != nil {
		t.Fatal(err)
	}
	second := testEvent("e2", "a", []byte("cp-2"))
	if err := s.Process(ctx, second); err != nil {
		t.Fatal(err)
	}
	// The same observation, redelivered with an older checkpoint value.
	older := second
	older.Checkpoint = []byte("cp-1")
	outcome, err := s.ProcessEvent(ctx, older)
	if err != nil || outcome != IngestDuplicate {
		t.Fatalf("replay = (%q, %v), want duplicate", outcome, err)
	}
	value, err := s.Load(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "cp-2" {
		t.Fatalf("checkpoint = %q, want cp-2: an ignored insert must not move the source's position", value)
	}
}

// TestIdentityCollisionIsReportedRatherThanCalledAHarmlessRepeat pins the rule
// that "already stored" is only reassuring when the stored row is the same
// evidence. A reused (source, event id) with different content means one of the
// two events cannot be stored, and that must be diagnosable.
func TestIdentityCollisionIsReportedRatherThanCalledAHarmlessRepeat(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	original := testEvent("c1", "a", []byte("cp-1"))
	if err := s.Process(ctx, original); err != nil {
		t.Fatal(err)
	}

	colliding := testEvent("c1", "a", []byte("cp-2"))
	colliding.Type = "file.deleted"
	colliding.OccurredAt = original.OccurredAt.Add(time.Second)
	outcome, err := s.ProcessEvent(ctx, colliding)
	if outcome != IngestCollision {
		t.Fatalf("outcome = %q, want collision", outcome)
	}
	if !errors.Is(err, ErrEventIdentityCollision) {
		t.Fatalf("error = %v, want ErrEventIdentityCollision", err)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 1 {
		t.Fatalf("rows = %d, want the first event only", got)
	}
	// The colliding event was not stored, so its checkpoint must not be adopted:
	// advancing to it would skip evidence past a position that holds nothing.
	value, err := s.Load(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "cp-1" {
		t.Fatalf("checkpoint = %q, want cp-1 frozen at the last stored event", value)
	}
	stats := s.IngestStats()
	if len(stats) != 1 || stats[0].Collisions != 1 || stats[0].Duplicates != 0 {
		t.Fatalf("counters = %+v, want one collision and no duplicates", stats)
	}
}

// TestPressureAndProcessingErrorsAreDistinguishable keeps the four ingestion
// states apart: an inserted event, a duplicate, a pressure rejection and a
// processing failure are different facts and must not collapse into "an error".
func TestPressureAndProcessingErrorsAreDistinguishable(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxBytes = 256 })
	ctx := context.Background()
	big := testEvent("p1", "a", []byte("cp-1"))
	big.Evidence = monitoring.EvidenceTelemetry
	big.Metadata = map[string]string{"blob": strings.Repeat("x", 4096)}
	outcome, err := s.ProcessEvent(ctx, big)
	if outcome != IngestPressureRejected {
		t.Fatalf("outcome = %q, want pressure_rejected", outcome)
	}
	if !errors.Is(err, ErrStoragePressure) {
		t.Fatalf("error = %v, want ErrStoragePressure", err)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 0 {
		t.Fatalf("rows = %d, want nothing stored under pressure", got)
	}

	// A metadata payload beyond the documented bound is a processing error, not
	// a pressure rejection, and stores nothing either.
	over := testEvent("p2", "a", []byte("cp-1"))
	over.Metadata = map[string]string{"blob": strings.Repeat("y", monitoring.MaxEventBytes+1)}
	outcome, err = s.ProcessEvent(ctx, over)
	if outcome != IngestProcessingError {
		t.Fatalf("outcome = %q, want processing_error", outcome)
	}
	if err == nil {
		t.Fatal("an oversize metadata payload must report an error")
	}
	if errors.Is(err, ErrStoragePressure) {
		t.Fatal("a processing error must not be reported as storage pressure")
	}

	stats := s.IngestStats()
	if len(stats) != 1 || stats[0].Rejected != 1 || stats[0].Failed != 1 {
		t.Fatalf("counters = %+v, want one rejected and one failed", stats)
	}
}

// TestIngestStatsArePerSourceAndBounded proves the diagnostics cannot grow with
// the number of source names seen: sources beyond the bound are folded into one
// aggregate bucket, and the bucket is only reported when it was used.
func TestIngestStatsArePerSourceAndBounded(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	for i := 0; i < maxTrackedIngestSources+5; i++ {
		source := fmt.Sprintf("src-%03d", i)
		if _, err := s.ProcessEvent(ctx, testEvent("x", source, nil)); err != nil {
			t.Fatal(err)
		}
	}
	stats := s.IngestStats()
	if len(stats) != maxTrackedIngestSources+1 {
		t.Fatalf("stats rows = %d, want %d tracked sources plus one aggregate bucket", len(stats), maxTrackedIngestSources+1)
	}
	var aggregate *IngestCounters
	for i := range stats {
		if stats[i].Source == otherIngestSourcesLabel {
			aggregate = &stats[i]
		}
	}
	if aggregate == nil || aggregate.Inserted != 5 {
		t.Fatalf("aggregate bucket = %+v, want 5 folded insertions", aggregate)
	}
	for i := 1; i < len(stats); i++ {
		if stats[i-1].Source >= stats[i].Source {
			t.Fatalf("stats are not ordered by source: %+v", stats)
		}
	}
}

// TestDuplicateIdentitySurvivesARestart is the restart-identity case: the
// deduplication decision is the events table's uniqueness constraint, so a
// replay after a restart is still recognized as a duplicate even though the
// per-process counters start again. The counters are diagnostics, never the
// source of that decision.
func TestDuplicateIdentitySurvivesARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	first, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent("s1", "a", []byte("cp-1"))
	if _, err := first.ProcessEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if stats := second.IngestStats(); len(stats) != 0 {
		t.Fatalf("counters survived a restart: %+v", stats)
	}
	outcome, err := second.ProcessEvent(ctx, event)
	if err != nil || outcome != IngestDuplicate {
		t.Fatalf("replay after restart = (%q, %v), want duplicate", outcome, err)
	}
	if got := countRows(t, second, "SELECT count(*) FROM events"); got != 1 {
		t.Fatalf("rows = %d, want the stored event only", got)
	}
	if stats := second.IngestStats(); len(stats) != 1 || stats[0].Duplicates != 1 || stats[0].Inserted != 0 {
		t.Fatalf("counters after restart = %+v, want one duplicate and no insertions", stats)
	}
}

// monitoringBytes reads the monitoring scope's byte meter, which is what the
// ingestion budget is charged against.
func monitoringBytes(t *testing.T, s *Store) int64 {
	t.Helper()
	usage, err := s.StorageUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return usage[usageMonitoring]
}

// Once a source's ordering is broken, later durable events may still be stored,
// but the checkpoint must never advance past the failure.
func TestBlockCheckpointsFreezesCheckpoint(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.Process(ctx, testEvent("e1", "a", []byte("cp-1"))); err != nil {
		t.Fatal(err)
	}
	s.BlockCheckpoints("a")
	if err := s.Process(ctx, testEvent("e2", "a", []byte("cp-2"))); err != nil {
		t.Fatal(err)
	}
	value, _ := s.Load(ctx, "a")
	if string(value) != "cp-1" {
		t.Fatalf("checkpoint advanced past a blocked failure: %q", value)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 2 {
		t.Fatalf("events after block = %d, want 2", got)
	}
	// Blocking one source must not freeze others.
	if err := s.Process(ctx, testEvent("e1", "b", []byte("cp-b"))); err != nil {
		t.Fatal(err)
	}
	if value, _ := s.Load(ctx, "b"); string(value) != "cp-b" {
		t.Fatalf("unrelated source frozen: %q", value)
	}
}

func TestCheckpointInvalidationAndLoad(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if _, err := s.Load(ctx, "missing"); !errors.Is(err, monitoring.ErrCheckpointNotFound) {
		t.Fatalf("missing checkpoint error = %v", err)
	}
	if err := s.Save(ctx, "a", []byte("cp")); err != nil {
		t.Fatal(err)
	}
	if err := s.Invalidate(ctx, "a", []byte("cp")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "a"); !errors.Is(err, monitoring.ErrCheckpointNotFound) {
		t.Fatalf("invalidated checkpoint still loadable: %v", err)
	}
	if err := s.Save(ctx, "a", make([]byte, 32769)); err == nil {
		t.Fatal("oversized checkpoint accepted")
	}
	if err := s.Save(ctx, "a", nil); err != nil {
		t.Fatalf("empty save must be a no-op: %v", err)
	}
}

func TestRetentionByAgeAndCount(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	old := testEvent("old", "a", nil)
	old.ObservedAt = time.Now().Add(-2 * time.Hour)
	if err := s.Process(ctx, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := s.Process(ctx, testEvent(fmt.Sprintf("n%d", i), "a", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Retain(ctx); err != nil {
		t.Fatal(err)
	}
	total := countRows(t, s, "SELECT count(*) FROM events")
	if total > 100 {
		t.Fatalf("retention kept %d events, limit is 100", total)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events WHERE event_id='old'"); got != 0 {
		t.Fatalf("age-expired event survived retention")
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.EventCount != int64(total) || info.MaxEvents != 100 || info.Encrypted {
		t.Fatalf("info = %+v, total %d", info, total)
	}
}

func TestGapPersistenceOrderingAndBound(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if err := s.RecordGap(ctx, monitoring.Gap{Source: "a", Start: time.Now(), End: time.Now(), Reason: fmt.Sprintf("gap-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := countRows(t, s, "SELECT count(*) FROM monitoring_gaps"); got != 10 {
		t.Fatalf("gap rows = %d, want bound 10", got)
	}
	gaps, err := s.Gaps(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 5 || gaps[0].Reason != "gap-24" {
		t.Fatalf("gaps not newest-first: %+v", gaps)
	}
	if _, err := s.Gaps(ctx, 500); err == nil {
		t.Fatal("page beyond max accepted")
	}
}

func TestStoragePressureRefusesWrites(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxBytes = 1 })
	if err := s.Process(context.Background(), testEvent("e", "a", nil)); !errors.Is(err, ErrStoragePressure) {
		t.Fatalf("pressure error = %v, want ErrStoragePressure", err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.MaxDatabaseBytes != 1 || info.DatabaseBytes < 1 {
		t.Fatalf("info under pressure = %+v", info)
	}
}

func TestCorruptDatabaseRefusedWithoutErasing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, testLimits(nil)); err == nil {
		t.Fatal("corrupt database opened")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "this is not a sqlite database" {
		t.Fatal("corrupt database was modified or erased")
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	s, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", currentSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path, testLimits(nil)); err == nil {
		t.Fatal("newer schema opened")
	}
}

// Simulated crash/restart: a closed store behaves like a stopped process; data
// and checkpoints must survive without manual recovery steps.
func TestRestartRecoveryPreservesEventsAndCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.db")
	first, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Process(context.Background(), testEvent("e1", "a", []byte("cp-1"))); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path, testLimits(nil))
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer second.Close()
	value, err := second.Load(context.Background(), "a")
	if err != nil || string(value) != "cp-1" {
		t.Fatalf("checkpoint after restart = %q, %v", value, err)
	}
	events, err := second.Recent(context.Background(), Query{Limit: 10})
	if err != nil || len(events) != 1 || events[0].EventID != "e1" {
		t.Fatalf("events after restart = %+v, %v", events, err)
	}
	if events[0].Metadata["name"] != "known_hosts" {
		t.Fatalf("metadata lost: %+v", events[0].Metadata)
	}
}

func TestRecentPaginationFiltersAndBounds(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		e := testEvent(fmt.Sprintf("e%d", i), "a", nil)
		if i%2 == 0 {
			e.Source = "b"
			e.Type = "file.deleted"
		}
		if err := s.Process(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Recent(ctx, Query{Limit: 5})
	if err != nil || len(page) != 5 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if page[0].EventID != "e9" {
		t.Fatalf("newest first violated: %+v", page[0])
	}
	next, err := s.Recent(ctx, Query{Limit: 5, Before: page[len(page)-1].ID})
	if err != nil || len(next) != 5 || next[0].EventID != "e4" {
		t.Fatalf("pagination = %+v, %v", next, err)
	}
	onlyA, err := s.Recent(ctx, Query{Limit: 10, Source: "a"})
	if err != nil || len(onlyA) != 5 {
		t.Fatalf("source filter = %+v, %v", onlyA, err)
	}
	deleted, err := s.Recent(ctx, Query{Limit: 10, Type: "file.deleted"})
	if err != nil || len(deleted) != 5 {
		t.Fatalf("type filter = %+v, %v", deleted, err)
	}
	if _, err := s.Recent(ctx, Query{Limit: 500}); err == nil {
		t.Fatal("oversized page accepted")
	}
	// Injection attempts are parameter-bound values, not SQL.
	injected, err := s.Recent(ctx, Query{Limit: 10, Source: "a'; DROP TABLE events;--"})
	if err != nil || len(injected) != 0 {
		t.Fatalf("injection filter = %+v, %v", injected, err)
	}
	if got := countRows(t, s, "SELECT count(*) FROM events"); got != 10 {
		t.Fatalf("table damaged by injected filter value: %d rows", got)
	}
}

func TestBackupIsCopyNotMove(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.Process(ctx, testEvent("e1", "a", []byte("cp"))); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, target); err == nil {
		t.Fatal("overwrote existing backup")
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup content = %d, %v", n, err)
	}
	if events, err := s.Recent(ctx, Query{Limit: 10}); err != nil || len(events) != 1 {
		t.Fatalf("backup disturbed the live store: %+v, %v", events, err)
	}
}

func TestLimitsFromEnvironment(t *testing.T) {
	t.Setenv("RADANOR_SPARROW_MAX_EVENTS", "123")
	t.Setenv("RADANOR_SPARROW_MAX_GAPS", "7")
	t.Setenv("RADANOR_SPARROW_RETENTION_DAYS", "3")
	t.Setenv("RADANOR_SPARROW_MAX_DB_MIB", "9")
	l, err := LimitsFromEnvironment()
	if err != nil || l.MaxEvents != 123 || l.MaxGaps != 7 || l.EventAge != 3*24*time.Hour || l.MaxBytes != 9<<20 {
		t.Fatalf("limits = %+v, %v", l, err)
	}
	for key, value := range map[string]string{"RADANOR_SPARROW_MAX_EVENTS": "0", "RADANOR_SPARROW_MAX_GAPS": "x", "RADANOR_SPARROW_RETENTION_DAYS": "-1", "RADANOR_SPARROW_MAX_DB_MIB": "many"} {
		t.Setenv(key, value)
		if _, err := LimitsFromEnvironment(); err == nil {
			t.Fatalf("%s=%s accepted", key, value)
		}
	}
}

// Synthetic benchmark of the durable event path (SQLite WAL, synchronous=FULL).
// This is a development-host microbenchmark, not a capacity promise.
func BenchmarkProcessEvent(b *testing.B) {
	path := filepath.Join(b.TempDir(), "monitoring.db")
	s, err := Open(path, DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Process(ctx, testEvent(fmt.Sprintf("b%d", i), "a", []byte("cp"))); err != nil {
			b.Fatal(err)
		}
	}
}
