package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/privatefs"
	_ "modernc.org/sqlite"
)

// currentSchemaVersion is the schema version this build writes. Version 1 is
// the CE-4 monitoring schema, version 2 adds the CE-5 assessment tables,
// version 3 adds the independent storage byte meters, and version 4 adds the
// CE-6 inventory snapshot tables, the settings table and the third storage
// scope.
const currentSchemaVersion = 4

var (
	// ErrStoragePressure reports that the monitoring event budget is exhausted.
	// Only monitoring-owned data is counted against it.
	ErrStoragePressure = errors.New("storage limit reached")
	// ErrAssessmentStoragePressure reports that posture-assessment history has
	// exhausted its own, separate budget. It is deliberately distinct from
	// ErrStoragePressure: assessment pressure must never be mistaken for
	// monitoring pressure, and must never stop monitoring ingestion.
	ErrAssessmentStoragePressure = errors.New("assessment storage limit reached")
)

// ErrEventIdentityCollision reports that an event reused an existing
// (source, event id) pair while carrying different content. It is deliberately
// distinct from a replay: a replay is the same evidence arriving twice, while a
// collision means one of the two events is being dropped. The insert is ignored
// either way — the store does not overwrite stored evidence — but a collision is
// an error rather than a success, because treating it as a harmless repeat is
// exactly how a real event would disappear without anyone noticing.
var ErrEventIdentityCollision = errors.New("event identity collision")

// IngestOutcome says what one call to ProcessEvent did with an event.
//
// It exists because an error alone could not answer the question. Storing an
// event and ignoring a duplicate both returned a nil error, so nothing
// downstream could tell new evidence from a repeat, and nothing could tell a
// repeat from a collision.
type IngestOutcome string

const (
	// IngestInserted means new evidence was stored and charged to the budget.
	IngestInserted IngestOutcome = "inserted"
	// IngestDuplicate means the event was already stored, byte for byte, so the
	// insert was ignored and nothing was charged twice.
	IngestDuplicate IngestOutcome = "duplicate"
	// IngestCollision means the event's (source, event id) was already stored
	// with different content, so this event was not stored.
	IngestCollision IngestOutcome = "collision"
	// IngestPressureRejected means the monitoring byte budget is exhausted, so
	// the event was refused without being stored.
	IngestPressureRejected IngestOutcome = "pressure_rejected"
	// IngestProcessingError means the event could not be processed: nothing was
	// stored and no checkpoint was advanced.
	IngestProcessingError IngestOutcome = "processing_error"
)

// maxTrackedIngestSources bounds the per-source ingestion counters. Sources
// beyond it are folded into one aggregate bucket, so the diagnostics cannot grow
// with the number of sources an operator or a source names.
const maxTrackedIngestSources = 64

// otherIngestSourcesLabel names the aggregate bucket for sources beyond the
// tracking bound.
const otherIngestSourcesLabel = "other sources"

// IngestCounters is what ingestion actually did for one source since this core
// process started. The counters are diagnostics, not a durable record: the
// deduplication decision itself is durable, because it is the UNIQUE constraint
// on the events table rather than anything held in memory.
type IngestCounters struct {
	Source string `json:"source"`
	// Inserted counts events stored as new evidence.
	Inserted int64 `json:"inserted"`
	// Duplicates counts events ignored as an exact replay of a stored event.
	Duplicates int64 `json:"duplicates"`
	// Collisions counts events ignored because their identity was already used
	// by different content. Any non-zero value here needs investigating: it means
	// an event was not stored.
	Collisions int64 `json:"collisions"`
	// Rejected counts events refused by the storage budget.
	Rejected int64 `json:"rejected"`
	// Failed counts events that could not be processed at all.
	Failed int64 `json:"failed"`
}

// Empty reports whether nothing has been recorded for this bucket yet.
func (c IngestCounters) Empty() bool {
	return c.Inserted == 0 && c.Duplicates == 0 && c.Collisions == 0 && c.Rejected == 0 && c.Failed == 0
}

// Storage scopes in the storage_usage table. Each has its own byte budget, so
// one kind of data cannot consume the room reserved for another.
const (
	usageMonitoring = "monitoring"
	usageAssessment = "assessment"
	// usageInventory is the software/network inventory snapshot. Inventory is a
	// current-state view rather than history, so it is bounded by its own budget
	// and by per-kind item caps, and it can never charge the monitoring scope.
	usageInventory = "inventory"
)

// assessmentRowReserveBytes covers everything about an assessment row that is
// not a variable-length text column: the fixed-width INTEGER columns (SQLite
// stores an INTEGER in at most nine bytes, a 64-bit varint), the per-column
// serial-type header, the row header, and the index entries the row adds.
//
// It is a deliberate, documented *upper bound* rather than a precise figure.
// The budget must over-count: under-counting is what would let posture history
// silently exceed its own ceiling.
const assessmentRowReserveBytes = 128

// sumTextExpr builds a per-table byte measurement that counts every stored text
// column plus the per-row reserve. IFNULL guards each column so a future
// nullable column can never poison the SUM into NULL and silently collapse the
// measurement to zero.
func sumTextExpr(table string, reserve int, columns ...string) string {
	parts := make([]string, 0, len(columns)+1)
	for _, column := range columns {
		parts = append(parts, "IFNULL(length("+column+"),0)")
	}
	parts = append(parts, strconv.Itoa(reserve))
	return "COALESCE((SELECT SUM(" + strings.Join(parts, "+") + ") FROM " + table + "),0)"
}

// assessmentBytesExpr measures every byte posture-assessment history owns.
//
// Monitoring tables (events, checkpoints, monitoring_gaps) are never included,
// so posture history cannot exhaust the reserved monitoring-event budget.
//
// Every assessment table is counted in full. The CE-5 expression omitted the
// non-evidence text columns of check_results and findings (title, explanation,
// action, verify method, summary, fingerprint, detail), so a run whose detail
// text grew could store substantially more than the meter charged for. Those
// columns are now measured, and each row carries the documented reserve above.
var assessmentBytesExpr = strings.Join([]string{
	sumTextExpr("observations", assessmentRowReserveBytes, "collector", "platform", "status", "source", "values_json", "error"),
	sumTextExpr("check_results", assessmentRowReserveBytes, "check_id", "check_version", "category", "title", "platform", "result", "severity", "detail", "explanation", "action", "verify_method", "limitations_json", "evidence_json"),
	sumTextExpr("findings", assessmentRowReserveBytes, "fingerprint", "check_id", "check_version", "platform", "severity", "lifecycle", "title", "summary", "action", "verify_method", "limitations_json", "evidence_json"),
	sumTextExpr("finding_history", assessmentRowReserveBytes, "fingerprint", "lifecycle", "result", "detail"),
	sumTextExpr("assessment_runs", assessmentRowReserveBytes, "trigger", "status"),
}, " +\n\t")

// usageQuerier and usageExecer let the byte meter be updated through either the
// shared connection or an open transaction, so a meter change always commits
// with the data it describes.
type usageQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
type usageExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func usageBytes(ctx context.Context, q usageQuerier, scope string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT bytes FROM storage_usage WHERE scope=?`, scope).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// addUsage moves one scope's meter by delta, clamped at zero so a rounding or
// double-count can never leave a negative meter behind.
//
// The decrement is done with an UPDATE rather than an upsert: SQLite checks the
// proposed row of an INSERT ... ON CONFLICT against the table's CHECK
// constraints before it resolves the conflict, so passing a negative delta as
// the VALUES argument would be rejected even though the clamped update would
// have been legal.
func addUsage(ctx context.Context, ex usageExecer, scope string, delta int64) error {
	if delta == 0 {
		return nil
	}
	res, err := ex.ExecContext(ctx, `UPDATE storage_usage SET bytes=MAX(0,bytes+?) WHERE scope=?`, delta, scope)
	if err != nil {
		return err
	}
	if updated, aerr := res.RowsAffected(); aerr == nil && updated == 0 && delta > 0 {
		if _, err := ex.ExecContext(ctx, `INSERT INTO storage_usage(scope,bytes) VALUES(?,?) ON CONFLICT(scope) DO NOTHING`, scope, delta); err != nil {
			return err
		}
	}
	return nil
}

type Limits struct {
	EventAge  time.Duration
	MaxEvents int
	// MaxBytes is the monitoring event budget. It is charged against stored
	// monitoring evidence only.
	MaxBytes int64
	MaxGaps  int
	// MaxAssessmentBytes is the independent posture-assessment budget. Exceeding
	// it prunes assessment history and, if that is not enough, refuses a new run
	// with ErrAssessmentStoragePressure. Monitoring is unaffected.
	MaxAssessmentBytes int64
	// MaxAssessmentRuns bounds how many posture runs are retained.
	MaxAssessmentRuns int
	// MaxInventoryBytes is the independent software/network inventory budget.
	// Inventory is a current-state snapshot, so it is bounded by this budget and
	// by per-kind item caps; it can never charge the monitoring scope.
	MaxInventoryBytes int64
	// MaxInventoryItems bounds how many items of one kind a snapshot may hold.
	MaxInventoryItems int
	MaxPageSize       int
}

func DefaultLimits() Limits {
	return Limits{
		EventAge: 7 * 24 * time.Hour, MaxEvents: 10000, MaxBytes: 64 << 20,
		MaxGaps: 1000, MaxPageSize: 50,
		MaxAssessmentBytes: 32 << 20, MaxAssessmentRuns: 200,
		MaxInventoryBytes: 8 << 20, MaxInventoryItems: 20000,
	}
}
func LimitsFromEnvironment() (Limits, error) {
	l := DefaultLimits()
	// Every storage budget is read through config.Env so an existing
	// installation's RADANOR_SHIELD_* tuning keeps applying after the rebrand.
	for suffix, target := range map[string]*int{"MAX_EVENTS": &l.MaxEvents, "MAX_GAPS": &l.MaxGaps} {
		if value := config.Env(suffix); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return l, fmt.Errorf("invalid %s", config.EnvPrefix+suffix)
			}
			*target = n
		}
	}
	if value := config.Env("RETENTION_DAYS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return l, errors.New("invalid retention days")
		}
		l.EventAge = time.Duration(n) * 24 * time.Hour
	}
	if value := config.Env("MAX_DB_MIB"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return l, errors.New("invalid database size")
		}
		l.MaxBytes = n << 20
	}
	if value := config.Env("MAX_ASSESS_MIB"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return l, errors.New("invalid assessment database size")
		}
		l.MaxAssessmentBytes = n << 20
	}
	if value := config.Env("MAX_ASSESS_RUNS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 || n > 5000 {
			return l, errors.New("invalid assessment run retention")
		}
		l.MaxAssessmentRuns = n
	}
	if value := config.Env("MAX_INVENTORY_MIB"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 {
			return l, errors.New("invalid inventory database size")
		}
		l.MaxInventoryBytes = n << 20
	}
	return l, nil
}

type Store struct {
	db     *sql.DB
	path   string
	limits Limits
	// runLimitOverride is a runtime assessment-run retention chosen through the
	// settings API. Zero means "use the configured limit".
	runLimitOverride atomic.Int64
	blockedMu        sync.RWMutex
	blocked          map[string]bool
	// ingestMu guards the bounded per-source ingestion counters. They are kept
	// in memory on purpose: they answer "what has this process done with what it
	// received", which is a question about the running core, and the durable part
	// of deduplication lives in the events table's uniqueness constraint.
	ingestMu       sync.Mutex
	ingestBySource map[string]*IngestCounters
	ingestOverflow IngestCounters
}

// SetAssessmentRunLimit overrides assessment-run retention at runtime so a
// settings change takes effect at the next prune without reopening the store.
// A non-positive value clears the override.
func (s *Store) SetAssessmentRunLimit(n int) { s.runLimitOverride.Store(int64(n)) }

// assessmentRunLimit resolves the effective retention: the runtime override when
// one is set, otherwise the configured limit.
func (s *Store) assessmentRunLimit() int {
	if n := int(s.runLimitOverride.Load()); n > 0 {
		return n
	}
	return s.limits.MaxAssessmentRuns
}

func Open(path string, limits Limits) (*Store, error) {
	if limits.MaxEvents <= 0 || limits.MaxBytes <= 0 || limits.MaxGaps <= 0 || limits.MaxPageSize <= 0 ||
		limits.MaxAssessmentBytes <= 0 || limits.MaxAssessmentRuns <= 0 ||
		limits.MaxInventoryBytes <= 0 || limits.MaxInventoryItems <= 0 {
		return nil, errors.New("invalid storage limits")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{
		db: db, path: path, limits: limits, blocked: map[string]bool{},
		ingestBySource: map[string]*IngestCounters{},
		ingestOverflow: IngestCounters{Source: otherIngestSourcesLabel},
	}
	if err = s.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = protectFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// protectFiles restricts the database and its WAL/SHM sidecars to the current user.
func protectFiles(path string) error {
	names := []string{path, path + "-wal", path + "-shm"}
	for _, name := range names {
		if _, err := os.Stat(name); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := privatefs.ProtectFile(name); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Close() error { return s.db.Close() }

// Limits returns a copy of the configured storage limits so the settings page
// can state the real budgets rather than a hard-coded figure.
func (s *Store) Limits() Limits { return s.limits }

// AssessmentRunLimit returns the effective assessment-run retention.
func (s *Store) AssessmentRunLimit() int { return s.assessmentRunLimit() }

// StorageUsage reports each storage scope's current byte charge. It is the only
// place the separate budgets are surfaced, so the UI can show local storage use
// truthfully and explain which scope is under pressure.
func (s *Store) StorageUsage(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	// The stored meters are read first and the result set is closed before the
	// live measurements below run: the pool holds a single connection, so an
	// open cursor would block them.
	err := func() error {
		rows, err := s.db.QueryContext(ctx, `SELECT scope,bytes FROM storage_usage ORDER BY scope`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var scope string
			var bytes int64
			if err := rows.Scan(&scope, &bytes); err != nil {
				return err
			}
			out[scope] = bytes
		}
		return rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	// The monitoring meter is maintained inside the same transaction as every
	// event it charges, so the stored value is authoritative for it. Posture
	// assessment and inventory are measured from their own tables instead, for
	// two reasons: they change through many separate write paths (run creation,
	// observations, check results, findings and their history, retention, and
	// snapshot replacement), so a meter updated in only some of them would
	// silently under-report; and both are bounded by the very budget reported
	// here, which keeps the measurement cheap. A stored value is never trusted
	// for these two scopes, so the reported figure cannot drift from reality.
	assessment, err := s.assessmentBytes(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := s.inventoryBytes(ctx)
	if err != nil {
		return nil, err
	}
	out[usageAssessment] = assessment
	out[usageInventory] = inventory
	return out, nil
}

// DatabaseSize reports the on-disk size of the database and its WAL sidecar.
// It is informational: the budget gates are per-scope byte meters, not this.
func (s *Store) DatabaseSize() int64 { return s.databaseBytes() }
func (s *Store) initialize(ctx context.Context) error {
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=3000"} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	var ok string
	if err := s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		return fmt.Errorf("database integrity check failed: %v", err)
	}
	return s.migrate(ctx)
}
func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return errors.New("database schema is newer than this application")
	}
	if version == 0 {
		statements := []string{
			`CREATE TABLE events(id INTEGER PRIMARY KEY, event_id TEXT NOT NULL, source TEXT NOT NULL, event_type TEXT NOT NULL, platform TEXT NOT NULL, occurred_ns INTEGER NOT NULL, observed_ns INTEGER NOT NULL, severity TEXT NOT NULL, evidence TEXT NOT NULL, schema_version TEXT NOT NULL, source_sequence TEXT NOT NULL, metadata_json BLOB NOT NULL, UNIQUE(source,event_id))`,
			`CREATE INDEX events_observed_idx ON events(observed_ns DESC,id DESC)`,
			`CREATE TABLE checkpoints(source TEXT PRIMARY KEY, value BLOB NOT NULL CHECK(length(value)<=32768), updated_ns INTEGER NOT NULL)`,
			`CREATE TABLE monitoring_gaps(id INTEGER PRIMARY KEY, source TEXT NOT NULL, start_ns INTEGER NOT NULL, end_ns INTEGER NOT NULL, reason TEXT NOT NULL CHECK(length(reason)<=256), recorded_ns INTEGER NOT NULL)`,
			`CREATE INDEX gaps_recorded_idx ON monitoring_gaps(recorded_ns DESC,id DESC)`,
			`PRAGMA user_version=1`}
		for _, q := range statements {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		version = 1
	}
	// CE-5 assessment schema. This only ever adds tables; the v1 monitoring
	// tables (events, checkpoints, monitoring_gaps) are never modified, so
	// existing evidence is preserved and the migration is idempotent.
	if version == 1 {
		statements := []string{
			`CREATE TABLE assessment_runs(id INTEGER PRIMARY KEY, trigger TEXT NOT NULL, status TEXT NOT NULL, started_ns INTEGER NOT NULL, finished_ns INTEGER NOT NULL, duration_ms INTEGER NOT NULL, checks_total INTEGER NOT NULL, passed INTEGER NOT NULL, failed INTEGER NOT NULL, unknown INTEGER NOT NULL, not_supported INTEGER NOT NULL, stale INTEGER NOT NULL, collectors_run INTEGER NOT NULL, collectors_failed INTEGER NOT NULL, new_findings INTEGER NOT NULL, open_findings INTEGER NOT NULL)`,
			`CREATE INDEX runs_started_idx ON assessment_runs(started_ns DESC, id DESC)`,
			`CREATE TABLE observations(id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES assessment_runs(id) ON DELETE CASCADE, collector TEXT NOT NULL, platform TEXT NOT NULL, collected_ns INTEGER NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, values_json BLOB NOT NULL CHECK(length(values_json)<=8192), error TEXT NOT NULL, UNIQUE(run_id, collector))`,
			`CREATE INDEX observations_run_idx ON observations(run_id)`,
			`CREATE TABLE check_results(id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES assessment_runs(id) ON DELETE CASCADE, check_id TEXT NOT NULL, check_version TEXT NOT NULL, category TEXT NOT NULL, title TEXT NOT NULL, platform TEXT NOT NULL, result TEXT NOT NULL, severity TEXT NOT NULL, detail TEXT NOT NULL, explanation TEXT NOT NULL, action TEXT NOT NULL, verify_method TEXT NOT NULL, limitations_json BLOB NOT NULL, collected_ns INTEGER NOT NULL, evaluated_ns INTEGER NOT NULL, fresh INTEGER NOT NULL, evidence_json BLOB NOT NULL CHECK(length(evidence_json)<=8192), UNIQUE(run_id, check_id))`,
			`CREATE INDEX check_results_run_idx ON check_results(run_id)`,
			`CREATE INDEX check_results_finding_idx ON check_results(check_id, result)`,
			`CREATE TABLE findings(id INTEGER PRIMARY KEY, fingerprint TEXT NOT NULL UNIQUE, check_id TEXT NOT NULL, check_version TEXT NOT NULL, platform TEXT NOT NULL, severity TEXT NOT NULL, lifecycle TEXT NOT NULL, title TEXT NOT NULL, summary TEXT NOT NULL, action TEXT NOT NULL, verify_method TEXT NOT NULL, limitations_json BLOB NOT NULL, evidence_json BLOB NOT NULL CHECK(length(evidence_json)<=8192), first_seen_ns INTEGER NOT NULL, last_seen_ns INTEGER NOT NULL, resolved_ns INTEGER, reopened_ns INTEGER, run_id INTEGER NOT NULL)`,
			`CREATE INDEX findings_lifecycle_idx ON findings(lifecycle, severity)`,
			`CREATE INDEX findings_last_seen_idx ON findings(last_seen_ns DESC)`,
			`CREATE TABLE finding_history(id INTEGER PRIMARY KEY, fingerprint TEXT NOT NULL, run_id INTEGER NOT NULL, lifecycle TEXT NOT NULL, result TEXT NOT NULL, detail TEXT NOT NULL, recorded_ns INTEGER NOT NULL)`,
			`CREATE INDEX finding_history_fingerprint_idx ON finding_history(fingerprint, recorded_ns DESC)`,
			`PRAGMA user_version=2`}
		for _, q := range statements {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		version = 2
	}
	// CE-5 correction. Monitoring ingestion used to be gated on the size of the
	// whole database file, which also holds posture-assessment history, and a
	// SQLite file does not shrink when rows are deleted. Assessment history could
	// therefore grow the file past the monitoring budget and leave every
	// subsequent event refused forever, even after retention freed the rows.
	// Version 3 replaces that gate with two independent byte meters so posture
	// history can never consume the room reserved for monitoring evidence.
	if version == 2 {
		statements := []string{
			`CREATE TABLE storage_usage(scope TEXT PRIMARY KEY, bytes INTEGER NOT NULL CHECK(bytes>=0))`,
			// Seed the meters from the data already present so an upgraded
			// database keeps enforcing its budgets against real content.
			`INSERT INTO storage_usage(scope,bytes) SELECT 'monitoring', COALESCE(SUM(length(evidence)+length(metadata_json)),0) FROM events`,
			`INSERT INTO storage_usage(scope,bytes) SELECT 'assessment', ` + assessmentBytesExpr,
			`PRAGMA user_version=3`}
		for _, q := range statements {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		version = 3
	}
	// CE-6 schema. Version 4 adds the locally observed software/network
	// inventory and the persisted user settings. Both are additive: no existing
	// table is altered, so monitoring evidence and assessment history survive the
	// upgrade untouched. Inventory gets its own storage scope so it can never
	// charge the monitoring budget, and the settings table holds only bounded
	// key/value text that the core validates against a fixed allowlist.
	if version == 3 {
		statements := []string{
			`CREATE TABLE inventory_items(kind TEXT NOT NULL, name TEXT NOT NULL, version TEXT NOT NULL, source TEXT NOT NULL, platform TEXT NOT NULL, fields_json BLOB NOT NULL, observed_ns INTEGER NOT NULL, PRIMARY KEY(kind,name,version,source))`,
			`CREATE INDEX inventory_kind_name_idx ON inventory_items(kind,name)`,
			`CREATE TABLE inventory_snapshots(kind TEXT PRIMARY KEY, observed_ns INTEGER NOT NULL, total INTEGER NOT NULL, stored INTEGER NOT NULL, source TEXT NOT NULL, note TEXT NOT NULL, truncated INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_ns INTEGER NOT NULL)`,
			`INSERT INTO storage_usage(scope,bytes) VALUES('inventory',0)`,
			`PRAGMA user_version=4`}
		for _, q := range statements {
			if _, err = tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// BlockCheckpoints permanently stops checkpoint advancement for a source, so a
// later durable event can never move the checkpoint past a failed earlier one.
func (s *Store) BlockCheckpoints(source string) {
	s.blockedMu.Lock()
	s.blocked[source] = true
	s.blockedMu.Unlock()
}
func (s *Store) checkpointBlocked(source string) bool {
	s.blockedMu.RLock()
	defer s.blockedMu.RUnlock()
	return s.blocked[source]
}

// databaseBytes sums the main database and its WAL sidecar, which is where WAL-mode writes accumulate.
// databaseBytes reports the on-disk size of the database and its WAL sidecar.
// It is informational only: it is no longer used to gate ingestion, because a
// SQLite file does not shrink when rows are deleted and it mixes monitoring and
// assessment data together.
func (s *Store) databaseBytes() int64 {
	var total int64
	for _, name := range []string{s.path, s.path + "-wal"} {
		if info, err := os.Stat(name); err == nil {
			total += info.Size()
		}
	}
	return total
}

// Process is the monitoring engine's Processor entry point. It reports only the
// error, but the outcome is still counted, so a duplicate or a collision is
// visible through IngestStats instead of being discarded with the error.
func (s *Store) Process(ctx context.Context, e monitoring.Event) error {
	_, err := s.ProcessEvent(ctx, e)
	return err
}

// ProcessEvent stores one event and reports what happened to it, so a caller can
// tell new evidence from an ignored duplicate, a colliding identity, a pressure
// rejection and a processing failure.
func (s *Store) ProcessEvent(ctx context.Context, e monitoring.Event) (IngestOutcome, error) {
	outcome, err := s.processEvent(ctx, e)
	s.recordIngest(e.Source, outcome)
	return outcome, err
}

// processEvent is the ingestion transaction itself.
//
// Two rules are load-bearing here.
//
//   - An ignored insert is never charged to the budget, so a replay cannot
//     inflate the storage meter and evict real evidence.
//   - An ignored insert never advances the checkpoint. A replay carries the
//     checkpoint the original event already recorded, and re-applying an older
//     one would move the source's position backwards; a collision's checkpoint
//     is not written at all, because the event it belongs to was not stored and
//     advancing past it would skip evidence that was never persisted.
func (s *Store) processEvent(ctx context.Context, e monitoring.Event) (IngestOutcome, error) {
	metadata, err := json.Marshal(e.Metadata)
	if err != nil || len(metadata) > monitoring.MaxEventBytes {
		return IngestProcessingError, errors.New("metadata encoding rejected")
	}
	size := int64(len(e.Evidence)) + int64(len(metadata))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestProcessingError, err
	}
	defer tx.Rollback()
	// The ingestion budget is charged against monitoring-owned payload only.
	// Counting the whole file here is what let posture-assessment history crowd
	// out monitoring evidence, so the meter is maintained in the same
	// transaction as the event it describes and cannot drift.
	inUse, err := usageBytes(ctx, tx, usageMonitoring)
	if err != nil {
		return IngestProcessingError, err
	}
	if inUse+size > s.limits.MaxBytes {
		return IngestPressureRejected, ErrStoragePressure
	}
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO events(event_id,source,event_type,platform,occurred_ns,observed_ns,severity,evidence,schema_version,source_sequence,metadata_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, e.ID, e.Source, e.Type, e.Platform, e.OccurredAt.UnixNano(), e.ObservedAt.UnixNano(), e.Severity, e.Evidence, e.SchemaVersion, e.SourceSequence, metadata)
	if err != nil {
		return IngestProcessingError, err
	}
	inserted, aerr := res.RowsAffected()
	if aerr != nil {
		// A store that cannot report the effect of its own insert cannot be
		// trusted to say whether the event was new, so it says so rather than
		// guessing.
		return IngestProcessingError, errors.New("ingest result unavailable")
	}
	if inserted == 0 {
		return s.classifyIgnoredInsert(ctx, tx, e, metadata)
	}
	if err := addUsage(ctx, tx, usageMonitoring, size); err != nil {
		return IngestProcessingError, err
	}
	if len(e.Checkpoint) > 0 && !s.checkpointBlocked(e.Source) {
		if len(e.Checkpoint) > 32768 {
			return IngestProcessingError, errors.New("checkpoint too large")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO checkpoints(source,value,updated_ns) VALUES(?,?,?) ON CONFLICT(source) DO UPDATE SET value=excluded.value,updated_ns=excluded.updated_ns`, e.Source, e.Checkpoint, time.Now().UnixNano())
		if err != nil {
			return IngestProcessingError, err
		}
	}
	if err := tx.Commit(); err != nil {
		return IngestProcessingError, err
	}
	return IngestInserted, nil
}

// classifyIgnoredInsert decides why an insert was ignored. `INSERT OR IGNORE`
// also ignores a constraint violation other than uniqueness, so a missing row is
// reported as a processing error rather than assumed to be a replay: the store
// must not claim an event is already stored without having found it.
//
// Every stored column that carries content is compared, not just the id. A
// replay is the same observation arriving twice and is therefore byte for byte
// identical in the fields the store owns; anything else that shares an identity
// is a collision, and a collision means an event is not being stored.
func (s *Store) classifyIgnoredInsert(ctx context.Context, tx *sql.Tx, e monitoring.Event, metadata []byte) (IngestOutcome, error) {
	var eventType, evidence, sourceSequence string
	var storedMetadata []byte
	var occurred int64
	err := tx.QueryRowContext(ctx, `SELECT event_type,occurred_ns,evidence,source_sequence,metadata_json FROM events WHERE source=? AND event_id=?`, e.Source, e.ID).
		Scan(&eventType, &occurred, &evidence, &sourceSequence, &storedMetadata)
	if errors.Is(err, sql.ErrNoRows) {
		return IngestProcessingError, errors.New("ingest was ignored but no stored event explains why")
	}
	if err != nil {
		return IngestProcessingError, err
	}
	// The occurrence time is part of the identity, not just the id: the same file
	// event redelivered within one source run carries the same occurrence time,
	// while a different observation reusing an id necessarily carries another.
	if eventType == e.Type && occurred == e.OccurredAt.UnixNano() && evidence == string(e.Evidence) &&
		sourceSequence == e.SourceSequence && bytes.Equal(storedMetadata, metadata) {
		// Identical content: a replay. Its checkpoint is deliberately not written;
		// see processEvent.
		return IngestDuplicate, nil
	}
	// Same identity, different content. One of the two events cannot be stored,
	// so this is reported as a failure rather than as a harmless repeat: the
	// engine then freezes the source's replay position instead of advancing past
	// evidence that was never persisted.
	return IngestCollision, ErrEventIdentityCollision
}

// recordIngest counts one outcome for one source, folding sources beyond the
// tracking bound into a single aggregate bucket so memory cannot grow with the
// number of distinct source names seen.
func (s *Store) recordIngest(source string, outcome IngestOutcome) {
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	if s.ingestBySource == nil {
		s.ingestBySource = map[string]*IngestCounters{}
	}
	counters, ok := s.ingestBySource[source]
	if !ok {
		if len(s.ingestBySource) >= maxTrackedIngestSources {
			counters = &s.ingestOverflow
			counters.Source = otherIngestSourcesLabel
		} else {
			counters = &IngestCounters{Source: source}
			s.ingestBySource[source] = counters
		}
	}
	switch outcome {
	case IngestInserted:
		counters.Inserted++
	case IngestDuplicate:
		counters.Duplicates++
	case IngestCollision:
		counters.Collisions++
	case IngestPressureRejected:
		counters.Rejected++
	default:
		counters.Failed++
	}
}

// IngestStats reports what ingestion has done, per source, since this core
// process started. It is ordered by source and bounded, and the aggregate bucket
// for sources beyond the tracking bound is included only when it was used.
func (s *Store) IngestStats() []IngestCounters {
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	out := make([]IngestCounters, 0, len(s.ingestBySource)+1)
	for _, counters := range s.ingestBySource {
		out = append(out, *counters)
	}
	if !s.ingestOverflow.Empty() {
		out = append(out, s.ingestOverflow)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}
func (s *Store) Load(ctx context.Context, source string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, "SELECT value FROM checkpoints WHERE source=?", source).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, monitoring.ErrCheckpointNotFound
	}
	return value, err
}
func (s *Store) Save(ctx context.Context, source string, value []byte) error {
	if len(value) == 0 {
		return nil
	}
	if len(value) > 32768 {
		return errors.New("checkpoint too large")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO checkpoints(source,value,updated_ns) VALUES(?,?,?) ON CONFLICT(source) DO UPDATE SET value=excluded.value,updated_ns=excluded.updated_ns`, source, value, time.Now().UnixNano())
	return err
}
func (s *Store) Invalidate(ctx context.Context, source string, _ []byte) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM checkpoints WHERE source=?", source)
	return err
}
func (s *Store) RecordGap(ctx context.Context, g monitoring.Gap) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO monitoring_gaps(source,start_ns,end_ns,reason,recorded_ns) VALUES(?,?,?,?,?)", g.Source, g.Start.UnixNano(), g.End.UnixNano(), g.Reason, time.Now().UnixNano())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM monitoring_gaps WHERE id IN (SELECT id FROM monitoring_gaps ORDER BY recorded_ns DESC,id DESC LIMIT -1 OFFSET ?)`, s.limits.MaxGaps)
	return err
}

// eventBytesExpr measures the monitoring-owned payload charged against the
// ingestion budget: the event body plus its metadata JSON.
const eventBytesExpr = `COALESCE(SUM(length(evidence)+length(metadata_json)),0)`

func (s *Store) Retain(ctx context.Context) error {
	cutoff := time.Now().Add(-s.limits.EventAge).UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var freed int64
	if err = tx.QueryRowContext(ctx, `SELECT `+eventBytesExpr+` FROM events WHERE observed_ns < ?`, cutoff).Scan(&freed); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE observed_ns < ?", cutoff); err != nil {
		return err
	}
	var overflow int64
	if err = tx.QueryRowContext(ctx, `SELECT `+eventBytesExpr+` FROM events WHERE id NOT IN (SELECT id FROM events ORDER BY observed_ns DESC,id DESC LIMIT ?)`, s.limits.MaxEvents).Scan(&overflow); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY observed_ns DESC,id DESC LIMIT -1 OFFSET ?)`, s.limits.MaxEvents); err != nil {
		return err
	}
	if freed+overflow > 0 {
		if err = addUsage(ctx, tx, usageMonitoring, -(freed + overflow)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.checkpointWAL(ctx)
	return nil
}

// checkpointWAL folds and truncates the write-ahead log. Assessment history is
// written in bursts, and the WAL sidecar otherwise keeps the database file at
// its peak long after the rows that caused it have been retained away.
//
// A busy log is reported as a result, not as an error, so this is best-effort
// by design: retention must never fail because a reader held the log briefly.
func (s *Store) checkpointWAL(ctx context.Context) {
	var busy, logPages, checkpointed int
	_ = s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logPages, &checkpointed)
}

type EventRecord struct {
	ID             int64             `json:"cursor"`
	EventID        string            `json:"event_id"`
	Source         string            `json:"source"`
	Type           string            `json:"type"`
	Platform       string            `json:"platform"`
	OccurredAt     time.Time         `json:"occurred_at"`
	ObservedAt     time.Time         `json:"observed_at"`
	Severity       string            `json:"severity,omitempty"`
	Evidence       string            `json:"evidence"`
	SchemaVersion  string            `json:"schema_version"`
	SourceSequence string            `json:"source_sequence,omitempty"`
	Metadata       map[string]string `json:"metadata"`
}
type GapRecord struct {
	ID     int64     `json:"cursor"`
	Source string    `json:"source"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Reason string    `json:"reason"`
}
type Query struct {
	Source string `json:"source,omitempty"`
	Type   string `json:"type,omitempty"`
	Before int64  `json:"before,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (s *Store) Recent(ctx context.Context, q Query) ([]EventRecord, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > s.limits.MaxPageSize {
		return nil, errors.New("page too large")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,event_id,source,event_type,platform,occurred_ns,observed_ns,severity,evidence,schema_version,source_sequence,metadata_json FROM events WHERE (?='' OR source=?) AND (?='' OR event_type=?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, q.Source, q.Source, q.Type, q.Type, q.Before, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventRecord
	for rows.Next() {
		var r EventRecord
		var occurred, observed int64
		var metadata []byte
		if err = rows.Scan(&r.ID, &r.EventID, &r.Source, &r.Type, &r.Platform, &occurred, &observed, &r.Severity, &r.Evidence, &r.SchemaVersion, &r.SourceSequence, &metadata); err != nil {
			return nil, err
		}
		r.OccurredAt = time.Unix(0, occurred)
		r.ObservedAt = time.Unix(0, observed)
		if json.Unmarshal(metadata, &r.Metadata) != nil {
			r.Metadata = map[string]string{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Gaps(ctx context.Context, limit int) ([]GapRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > s.limits.MaxPageSize {
		return nil, errors.New("page too large")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,source,start_ns,end_ns,reason FROM monitoring_gaps ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GapRecord
	for rows.Next() {
		var r GapRecord
		var start, end int64
		if err = rows.Scan(&r.ID, &r.Source, &start, &end, &r.Reason); err != nil {
			return nil, err
		}
		r.Start = time.Unix(0, start)
		r.End = time.Unix(0, end)
		out = append(out, r)
	}
	return out, rows.Err()
}

type Info struct {
	EventCount       int64 `json:"event_count"`
	GapCount         int64 `json:"gap_count"`
	DatabaseBytes    int64 `json:"database_bytes"`
	MaxEvents        int   `json:"max_events"`
	MaxDatabaseBytes int64 `json:"max_database_bytes"`
	RetentionDays    int   `json:"retention_days"`
	Encrypted        bool  `json:"encrypted"`
}

func (s *Store) Info(ctx context.Context) (Info, error) {
	var i Info
	i.MaxEvents = s.limits.MaxEvents
	i.MaxDatabaseBytes = s.limits.MaxBytes
	i.RetentionDays = int(s.limits.EventAge / (24 * time.Hour))
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&i.EventCount); err != nil {
		return i, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM monitoring_gaps").Scan(&i.GapCount); err != nil {
		return i, err
	}
	i.DatabaseBytes = s.databaseBytes()
	return i, nil
}
func (s *Store) Integrity(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("integrity check failed")
	}
	return nil
}
func (s *Store) Backup(ctx context.Context, target string) error {
	if _, err := os.Stat(target); err == nil {
		return errors.New("backup target exists")
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return err
	}
	return os.Chmod(target, 0600)
}
