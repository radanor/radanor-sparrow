package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// Bounded, indexed, read-only access to the CE-5 assessment tables. Every query
// is parameterized and page-limited, and every list is ordered by an indexed
// column so the dashboard cannot force an unbounded scan.

// RunRecord is one assessment run as shown in scan history.
type RunRecord struct {
	ID               int64     `json:"id"`
	Trigger          string    `json:"trigger"`
	Status           string    `json:"status"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	DurationMS       int64     `json:"duration_ms"`
	ChecksTotal      int       `json:"checks_total"`
	Passed           int       `json:"passed"`
	Failed           int       `json:"failed"`
	Unknown          int       `json:"unknown"`
	NotSupported     int       `json:"not_supported"`
	Stale            int       `json:"stale"`
	CollectorsRun    int       `json:"collectors_run"`
	CollectorsFailed int       `json:"collectors_failed"`
	NewFindings      int       `json:"new_findings"`
	OpenFindings     int       `json:"open_findings"`
}

// CheckResultRecord is a stored check outcome, including its evidence.
type CheckResultRecord struct {
	RunID        int64                  `json:"run_id"`
	CheckID      string                 `json:"check_id"`
	CheckVersion string                 `json:"check_version"`
	Category     string                 `json:"category"`
	Title        string                 `json:"title"`
	Platform     string                 `json:"platform"`
	Result       assessment.Result      `json:"result"`
	Severity     assessment.Severity    `json:"severity,omitempty"`
	Detail       string                 `json:"detail,omitempty"`
	Explanation  string                 `json:"explanation"`
	Action       string                 `json:"action"`
	VerifyMethod string                 `json:"verify_method"`
	Limitations  []string               `json:"limitations,omitempty"`
	Evidence     assessment.Observation `json:"evidence"`
	CollectedAt  time.Time              `json:"collected_at"`
	EvaluatedAt  time.Time              `json:"evaluated_at"`
	Fresh        bool                   `json:"fresh"`
}

// FindingRecord is a stored finding with its lifecycle.
type FindingRecord struct {
	ID           int64                  `json:"id"`
	Fingerprint  string                 `json:"fingerprint"`
	CheckID      string                 `json:"check_id"`
	CheckVersion string                 `json:"check_version"`
	Platform     string                 `json:"platform"`
	Severity     assessment.Severity    `json:"severity"`
	Lifecycle    assessment.Lifecycle   `json:"lifecycle"`
	Title        string                 `json:"title"`
	Summary      string                 `json:"summary,omitempty"`
	Action       string                 `json:"action"`
	VerifyMethod string                 `json:"verify_method"`
	Limitations  []string               `json:"limitations,omitempty"`
	Evidence     assessment.Observation `json:"evidence"`
	FirstSeenAt  time.Time              `json:"first_seen_at"`
	LastSeenAt   time.Time              `json:"last_seen_at"`
	ResolvedAt   *time.Time             `json:"resolved_at,omitempty"`
	ReopenedAt   *time.Time             `json:"reopened_at,omitempty"`
	RunID        int64                  `json:"run_id"`
}

// HistoryRecord is one lifecycle transition in a finding's verification history.
type HistoryRecord struct {
	RunID      int64                `json:"run_id"`
	Lifecycle  assessment.Lifecycle `json:"lifecycle"`
	Result     assessment.Result    `json:"result"`
	Detail     string               `json:"detail,omitempty"`
	RecordedAt time.Time            `json:"recorded_at"`
}

// AssessmentSummary is the compact posture overview for the dashboard.
type AssessmentSummary struct {
	HasRun         bool           `json:"has_run"`
	LastRun        *RunRecord     `json:"last_run,omitempty"`
	LastCompleteAt *time.Time     `json:"last_complete_at,omitempty"`
	OpenFindings   int            `json:"open_findings"`
	BySeverity     map[string]int `json:"by_severity"`
	FailedChecks   int            `json:"failed_checks"`
	UnknownChecks  int            `json:"unknown_checks"`
	StaleChecks    int            `json:"stale_checks"`
	NotSupported   int            `json:"not_supported_checks"`
	TotalChecks    int            `json:"total_checks"`
	ChecksFresh    bool           `json:"checks_fresh"`
	RunningRunID   int64          `json:"running_run_id,omitempty"`
	// StorageBytes, StorageMaxBytes and StoragePressure report posture-storage
	// degradation specifically. They are assessment-scoped: a report here never
	// means monitoring ingestion is impaired.
	StorageBytes    int64 `json:"storage_bytes"`
	StorageMaxBytes int64 `json:"storage_max_bytes"`
	StoragePressure bool  `json:"storage_pressure"`
}

func scanRun(row interface{ Scan(...any) error }) (RunRecord, error) {
	var r RunRecord
	var started, finished int64
	if err := row.Scan(&r.ID, &r.Trigger, &r.Status, &started, &finished, &r.DurationMS,
		&r.ChecksTotal, &r.Passed, &r.Failed, &r.Unknown, &r.NotSupported, &r.Stale,
		&r.CollectorsRun, &r.CollectorsFailed, &r.NewFindings, &r.OpenFindings); err != nil {
		return RunRecord{}, err
	}
	r.StartedAt = time.Unix(0, started)
	if finished > 0 {
		r.FinishedAt = time.Unix(0, finished)
	}
	return r, nil
}

const runColumns = `id,trigger,status,started_ns,finished_ns,duration_ms,checks_total,passed,failed,unknown,not_supported,stale,collectors_run,collectors_failed,new_findings,open_findings`

// Runs lists assessment runs newest-first with an id cursor.
func (s *Store) Runs(ctx context.Context, before int64, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > s.limits.MaxPageSize {
		return nil, errors.New("page too large")
	}
	if before < 0 {
		return nil, errors.New("invalid cursor")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+` FROM assessment_runs WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRecord
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRun returns the most recent run, if any.
func (s *Store) LatestRun(ctx context.Context) (*RunRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assessment_runs ORDER BY id DESC LIMIT 1`)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ActiveRun returns the newest run still in the running state, which is how the
// core reports an interrupted or in-flight assessment after a restart.
func (s *Store) ActiveRun(ctx context.Context) (*RunRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM assessment_runs WHERE status='running' ORDER BY id DESC LIMIT 1`)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

const checkResultColumns = `run_id,check_id,check_version,category,title,platform,result,severity,detail,explanation,action,verify_method,limitations_json,collected_ns,evaluated_ns,fresh,evidence_json`

func scanCheckResult(row interface{ Scan(...any) error }) (CheckResultRecord, error) {
	var r CheckResultRecord
	var result, severity string
	var limitations, evidence []byte
	var collected, evaluated int64
	var fresh int
	if err := row.Scan(&r.RunID, &r.CheckID, &r.CheckVersion, &r.Category, &r.Title, &r.Platform,
		&result, &severity, &r.Detail, &r.Explanation, &r.Action, &r.VerifyMethod,
		&limitations, &collected, &evaluated, &fresh, &evidence); err != nil {
		return CheckResultRecord{}, err
	}
	r.Result = assessment.Result(result)
	r.Severity = assessment.Severity(severity)
	r.CollectedAt = time.Unix(0, collected)
	r.EvaluatedAt = time.Unix(0, evaluated)
	r.Fresh = fresh == 1
	if len(limitations) > 0 {
		_ = json.Unmarshal(limitations, &r.Limitations)
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &r.Evidence)
	}
	return r, nil
}

// RunResults returns the check results for one run, ordered by check id.
func (s *Store) RunResults(ctx context.Context, runID int64, limit int) ([]CheckResultRecord, error) {
	if runID <= 0 {
		return nil, errors.New("invalid run id")
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkResultColumns+` FROM check_results WHERE run_id=? ORDER BY check_id LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckResultRecord
	for rows.Next() {
		r, err := scanCheckResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestResults returns the most recent evidence-backed outcome for every check
// from the newest completed run.
func (s *Store) LatestResults(ctx context.Context, limit int) ([]CheckResultRecord, int64, error) {
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	var runID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM assessment_runs WHERE status IN ('completed','cancelled') ORDER BY id DESC LIMIT 1`).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	results, err := s.RunResults(ctx, runID, limit)
	return results, runID, err
}

const findingColumns = `id,fingerprint,check_id,check_version,platform,severity,lifecycle,title,summary,action,verify_method,limitations_json,evidence_json,first_seen_ns,last_seen_ns,resolved_ns,reopened_ns,run_id`

func scanFinding(row interface{ Scan(...any) error }) (FindingRecord, error) {
	var f FindingRecord
	var severity, lifecycle string
	var limitations, evidence []byte
	var first, last int64
	var resolved, reopened sql.NullInt64
	if err := row.Scan(&f.ID, &f.Fingerprint, &f.CheckID, &f.CheckVersion, &f.Platform, &severity, &lifecycle,
		&f.Title, &f.Summary, &f.Action, &f.VerifyMethod, &limitations, &evidence,
		&first, &last, &resolved, &reopened, &f.RunID); err != nil {
		return FindingRecord{}, err
	}
	f.Severity = assessment.Severity(severity)
	f.Lifecycle = assessment.Lifecycle(lifecycle)
	f.FirstSeenAt = time.Unix(0, first)
	f.LastSeenAt = time.Unix(0, last)
	if resolved.Valid {
		t := time.Unix(0, resolved.Int64)
		f.ResolvedAt = &t
	}
	if reopened.Valid {
		t := time.Unix(0, reopened.Int64)
		f.ReopenedAt = &t
	}
	if len(limitations) > 0 {
		_ = json.Unmarshal(limitations, &f.Limitations)
	}
	if len(evidence) > 0 {
		_ = json.Unmarshal(evidence, &f.Evidence)
	}
	return f, nil
}

// Findings lists stored findings newest-first, optionally filtered by
// lifecycle, with an id cursor.
func (s *Store) Findings(ctx context.Context, lifecycle string, before int64, limit int) ([]FindingRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > s.limits.MaxPageSize {
		return nil, errors.New("page too large")
	}
	if lifecycle != "" && lifecycle != "new" && lifecycle != "continuing" && lifecycle != "resolved" && lifecycle != "reopened" && lifecycle != "superseded" {
		return nil, errors.New("invalid lifecycle filter")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+findingColumns+` FROM findings WHERE (?='' OR lifecycle=?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`,
		lifecycle, lifecycle, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingRecord
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Finding returns one finding by fingerprint.
func (s *Store) Finding(ctx context.Context, fingerprint string) (*FindingRecord, error) {
	if len(fingerprint) != 64 {
		return nil, errors.New("invalid fingerprint")
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+findingColumns+` FROM findings WHERE fingerprint=?`, fingerprint)
	f, err := scanFinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// FindingHistory returns the verification history for one finding, newest-first.
func (s *Store) FindingHistory(ctx context.Context, fingerprint string, limit int) ([]HistoryRecord, error) {
	if len(fingerprint) != 64 {
		return nil, errors.New("invalid fingerprint")
	}
	if limit <= 0 || limit > s.limits.MaxPageSize {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,lifecycle,result,detail,recorded_ns FROM finding_history WHERE fingerprint=? ORDER BY recorded_ns DESC, id DESC LIMIT ?`, fingerprint, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryRecord
	for rows.Next() {
		var h HistoryRecord
		var lifecycle, result string
		var recorded int64
		if err := rows.Scan(&h.RunID, &lifecycle, &result, &h.Detail, &recorded); err != nil {
			return nil, err
		}
		h.Lifecycle = assessment.Lifecycle(lifecycle)
		h.Result = assessment.Result(result)
		h.RecordedAt = time.Unix(0, recorded)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Summary builds the compact posture overview in a small number of indexed
// queries.
func (s *Store) Summary(ctx context.Context) (AssessmentSummary, error) {
	summary := AssessmentSummary{BySeverity: map[string]int{}}
	last, err := s.LatestRun(ctx)
	if err != nil {
		return summary, err
	}
	if last != nil {
		summary.HasRun = true
		summary.LastRun = last
		if last.Status == "running" {
			summary.RunningRunID = last.ID
		}
		if !last.FinishedAt.IsZero() {
			t := last.FinishedAt
			summary.LastCompleteAt = &t
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT severity,count(*) FROM findings WHERE lifecycle IN ('new','continuing','reopened') GROUP BY severity`)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var severity string
		var count int
		if err := rows.Scan(&severity, &count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.BySeverity[severity] = count
		summary.OpenFindings += count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return summary, err
	}
	results, _, err := s.LatestResults(ctx, 256)
	if err != nil {
		return summary, err
	}
	for _, r := range results {
		summary.TotalChecks++
		switch r.Result {
		case assessment.Fail:
			summary.FailedChecks++
		case assessment.Unknown:
			summary.UnknownChecks++
		case assessment.Stale:
			summary.StaleChecks++
		case assessment.NotSupported:
			summary.NotSupported++
		}
	}
	summary.ChecksFresh = summary.TotalChecks > 0 && summary.StaleChecks == 0
	// Surface posture-storage pressure explicitly so the dashboard can say the
	// assessment history is bounded out rather than silently showing nothing.
	used, err := s.assessmentBytes(ctx)
	if err != nil {
		return summary, err
	}
	summary.StorageBytes = used
	summary.StorageMaxBytes = s.limits.MaxAssessmentBytes
	summary.StoragePressure = used >= s.limits.MaxAssessmentBytes
	return summary, nil
}

// ReconcileInterruptedRuns closes any run left in the running state by a core
// that stopped or crashed mid-scan. Without this an interrupted scan would look
// like it was still in progress forever. The run keeps its recorded start time
// and is marked interrupted so the dashboard can report the gap honestly.
func (s *Store) ReconcileInterruptedRuns(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE assessment_runs SET status='interrupted', finished_ns=?, duration_ms=0 WHERE status='running'`, time.Now().UnixNano())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return n, err
}

// RetainAssessments bounds assessment growth: it keeps at most maxRuns runs
// (cascade-deleting their observations and results), prunes history rows for
// retired runs, and keeps resolved findings visible but bounded. Monitoring
// events, checkpoints and gaps are never touched here.
func (s *Store) RetainAssessments(ctx context.Context, maxRuns int) error {
	if err := s.pruneRunHorizon(ctx, maxRuns); err != nil {
		return err
	}
	// Folding the write-ahead log is worthwhile after a prune because assessment
	// history is written in bursts, and the sidecar otherwise keeps the database
	// file at its peak long after the rows that caused it are gone. It is kept out
	// of pruneRunHorizon so the horizon can also be enforced on the hot path of
	// starting a scan without paying for a checkpoint there.
	s.checkpointWAL(ctx)
	return nil
}

// pruneRunHorizon keeps at most maxRuns runs, deleting the oldest along with
// their observations, check results and history. A non-positive value means "use
// the configured limit", which honours a runtime override chosen through the
// settings API. Monitoring tables are never touched here.
func (s *Store) pruneRunHorizon(ctx context.Context, maxRuns int) error {
	if maxRuns <= 0 {
		maxRuns = s.assessmentRunLimit()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM observations WHERE run_id IN (SELECT id FROM assessment_runs ORDER BY id DESC LIMIT -1 OFFSET ?)`, maxRuns); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM check_results WHERE run_id IN (SELECT id FROM assessment_runs ORDER BY id DESC LIMIT -1 OFFSET ?)`, maxRuns); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM finding_history WHERE run_id IN (SELECT id FROM assessment_runs ORDER BY id DESC LIMIT -1 OFFSET ?)`, maxRuns); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM assessment_runs WHERE id IN (SELECT id FROM assessment_runs ORDER BY id DESC LIMIT -1 OFFSET ?)`, maxRuns); err != nil {
		return err
	}
	return tx.Commit()
}
