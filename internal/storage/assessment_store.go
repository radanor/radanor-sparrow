package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// This file implements the persistence the CE-5 scanner needs. Every write is
// parameterized, size-bounded, and confined to short transactions so that
// concurrent monitoring event ingestion is never blocked by a posture scan.

// StartRun records the beginning of an assessment and returns its id. The row
// is created in the running state so that an interrupted scan leaves an
// honest, inspectable record rather than a silent gap.
//
// A run is refused when posture history has already consumed its own byte
// budget and pruning cannot make room. That refusal is
// ErrAssessmentStoragePressure, which is scoped to assessment data: monitoring
// ingestion and checkpointing continue untouched while it holds.
func (s *Store) StartRun(ctx context.Context, trigger string) (int64, error) {
	if trigger == "" {
		trigger = "manual"
	}
	if trigger != "manual" && trigger != "scheduled" {
		return 0, errors.New("invalid trigger")
	}
	if err := s.enforceAssessmentBudget(ctx); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO assessment_runs(trigger,status,started_ns,finished_ns,duration_ms,checks_total,passed,failed,unknown,not_supported,stale,collectors_run,collectors_failed,new_findings,open_findings) VALUES(?,'running',?,0,0,0,0,0,0,0,0,0,0,0,0)`, trigger, time.Now().UnixNano())
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	// Retention is a promise about what is stored, not about what is stored
	// "after the next scan". The budget check above ran before this row existed,
	// so the horizon is enforced again here: without this the newest run sits
	// alongside a full set of retained runs, and the stored count is one over the
	// configured limit until the following scan happens to prune it.
	if err := s.pruneRunHorizon(ctx, 0); err != nil {
		return 0, err
	}
	return runID, nil
}

// FinishRun writes the terminal status and outcome counters for a run, then
// leaves posture history inside its own byte budget.
//
// Pruning here, rather than only before the next scan starts, is what makes the
// budget a statement about what is stored now: a run whose check results are
// large is the largest writer in the assessment scope, and without this the store
// would sit above its budget from the end of one scan until the start of the
// next. The run that just finished is the newest, so it is the last to be
// considered for deletion; when it alone cannot fit, history stays over budget,
// pressure is reported, and the next scan start is refused.
func (s *Store) FinishRun(ctx context.Context, runID int64, outcome assessment.ScanOutcome) error {
	if outcome.Status != "completed" && outcome.Status != "cancelled" && outcome.Status != "failed" {
		return errors.New("invalid run status")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE assessment_runs SET status=?,finished_ns=?,duration_ms=?,checks_total=?,passed=?,failed=?,unknown=?,not_supported=?,stale=?,collectors_run=?,collectors_failed=?,new_findings=?,open_findings=? WHERE id=?`,
		outcome.Status, outcome.FinishedAt.UnixNano(), outcome.FinishedAt.Sub(outcome.StartedAt).Milliseconds(),
		outcome.ChecksTotal, outcome.Passed, outcome.Failed, outcome.Unknown, outcome.NotSupported, outcome.Stale,
		outcome.CollectorsRun, outcome.CollectorsFailed, outcome.NewFindings, outcome.OpenFindings, runID)
	if err != nil {
		return err
	}
	// A refusal here is not an error for this run: the results are already stored
	// and reporting a completed scan as failed would be dishonest. Pressure is
	// surfaced through the storage report and enforced at the next run start.
	if perr := s.pruneAssessmentBytes(ctx); perr != nil && !errors.Is(perr, ErrAssessmentStoragePressure) {
		return perr
	}
	return nil
}

// SaveObservations stores one observation per collector for a run. Values are
// marshalled with a hard size bound so a misbehaving collector cannot bloat the
// database.
func (s *Store) SaveObservations(ctx context.Context, runID int64, obs []assessment.Observation) error {
	if len(obs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO observations(run_id,collector,platform,collected_ns,status,source,values_json,error) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range obs {
		if o.Collector == "" || len(o.Collector) > 128 {
			return errors.New("invalid observation collector")
		}
		encoded, err := encodeBounded(o.Values, assessment.MaxObservationBytes)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, runID, o.Collector, o.Platform, o.CollectedAt.UnixNano(), string(o.Status), o.Source, encoded, o.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestObservationSnapshot is the newest completed assessment's observation
// for one collector, together with why it may be absent. The two absences are
// deliberately distinct states, not one empty result: "no assessment has
// completed yet" and "the newest completed assessment recorded nothing for this
// collector" call for different explanations, and an older run must never be
// substituted for either one.
type LatestObservationSnapshot struct {
	// Observation is stored evidence. It is only meaningful when Found is true.
	Observation assessment.Observation
	// Found reports whether the newest completed assessment recorded this collector.
	Found bool
	// CompletedAssessment reports whether any completed assessment exists. It is
	// the evidence boundary the lookup stopped at.
	CompletedAssessment bool
}

// LatestObservation returns one named observation from the newest completed
// assessment. The collector name is bounded and the caller must still decide
// which of the stored fields are safe to expose through an API.
func (s *Store) LatestObservation(ctx context.Context, collector string) (LatestObservationSnapshot, error) {
	if collector == "" || len(collector) > 128 {
		return LatestObservationSnapshot{}, errors.New("invalid observation collector")
	}
	// The run is the driving row, joined to at most one observation (the table
	// is UNIQUE(run_id, collector)), so a single query answers both questions:
	// whether a completed assessment exists, and whether it holds this evidence.
	var (
		platform, status, source, observationError sql.NullString
		collectedNS                                sql.NullInt64
		valuesJSON                                 []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT o.platform,o.collected_ns,o.status,o.source,o.values_json,o.error
		FROM assessment_runs r
		LEFT JOIN observations o ON o.run_id=r.id AND o.collector=?
		WHERE r.status='completed'
		ORDER BY r.id DESC
		LIMIT 1`, collector).
		Scan(&platform, &collectedNS, &status, &source, &valuesJSON, &observationError)
	if errors.Is(err, sql.ErrNoRows) {
		// No completed assessment exists yet, so there is no evidence boundary.
		return LatestObservationSnapshot{}, nil
	}
	if err != nil {
		return LatestObservationSnapshot{}, err
	}
	// A logged run is present; status is NOT NULL exactly when its observation
	// row matched, so it tells the two absences apart.
	snapshot := LatestObservationSnapshot{CompletedAssessment: true}
	if !status.Valid {
		return snapshot, nil
	}
	var values []assessment.Value
	if err := json.Unmarshal(valuesJSON, &values); err != nil {
		return LatestObservationSnapshot{}, err
	}
	snapshot.Found = true
	snapshot.Observation = assessment.Observation{
		ID:          collector,
		Collector:   collector,
		Platform:    platform.String,
		CollectedAt: time.Unix(0, collectedNS.Int64),
		Status:      assessment.CollectionStatus(status.String),
		Source:      source.String,
		Values:      values,
		Error:       observationError.String,
	}
	return snapshot, nil
}

// SaveCheckResults stores the evidence-backed outcome of every check in a run.
func (s *Store) SaveCheckResults(ctx context.Context, runID int64, results []assessment.CheckResult) error {
	if len(results) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO check_results(run_id,check_id,check_version,category,title,platform,result,severity,detail,explanation,action,verify_method,limitations_json,collected_ns,evaluated_ns,fresh,evidence_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range results {
		if r.CheckID == "" || len(r.CheckID) > 128 {
			return errors.New("invalid check id")
		}
		limits, err := encodeBounded(r.Limitations, assessment.MaxObservationBytes)
		if err != nil {
			return err
		}
		evidence, err := encodeBounded(r.Observation, assessment.MaxObservationBytes)
		if err != nil {
			return err
		}
		fresh := 0
		if r.Fresh {
			fresh = 1
		}
		if _, err := stmt.ExecContext(ctx, runID, r.CheckID, r.CheckVersion, r.Category, r.Title, r.Observation.Platform, string(r.Result), string(r.Severity), r.Detail, r.Explanation, r.Action, r.VerifyMethod, limits, r.CollectedAt.UnixNano(), r.EvaluatedAt.UnixNano(), fresh, evidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// assessmentBytes returns the bytes currently charged to posture-assessment
// history. It is measured from the assessment tables only, so it is unaffected
// by monitoring evidence.
func (s *Store) assessmentBytes(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+assessmentBytesExpr).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// maxAssessmentPrunePasses bounds the pruning loop in enforceAssessmentBudget.
// Each pass removes one run, so the bound is a guard against a pathological
// database rather than a normal path.
const maxAssessmentPrunePasses = 10000

// enforceAssessmentBudget keeps posture history inside its own byte budget. It
// prunes by run count first and then by bytes, and reports
// ErrAssessmentStoragePressure only when even the newest run alone exceeds the
// budget. It never touches monitoring tables.
func (s *Store) enforceAssessmentBudget(ctx context.Context) error {
	// Zero selects the effective configured/override run limit.
	if err := s.RetainAssessments(ctx, 0); err != nil {
		return err
	}
	return s.pruneAssessmentBytes(ctx)
}

// pruneAssessmentBytes deletes the oldest runs until history fits its byte
// budget. It reports ErrAssessmentStoragePressure only when nothing is left to
// delete, which means the newest run alone is larger than the budget. It never
// touches monitoring tables, and it never deletes the newest run while older
// ones remain.
func (s *Store) pruneAssessmentBytes(ctx context.Context) error {
	for pass := 0; pass < maxAssessmentPrunePasses; pass++ {
		used, err := s.assessmentBytes(ctx)
		if err != nil {
			return err
		}
		if used <= s.limits.MaxAssessmentBytes {
			return nil
		}
		deleted, err := s.deleteOldestAssessmentRun(ctx)
		if err != nil {
			return err
		}
		if !deleted {
			return ErrAssessmentStoragePressure
		}
	}
	used, err := s.assessmentBytes(ctx)
	if err != nil {
		return err
	}
	if used > s.limits.MaxAssessmentBytes {
		return ErrAssessmentStoragePressure
	}
	return nil
}

// deleteOldestAssessmentRun removes a single run and everything that belongs to
// it. Findings themselves are shared across runs and are kept, so history is
// preserved even when the run that recorded it is pruned.
func (s *Store) deleteOldestAssessmentRun(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM assessment_runs ORDER BY id ASC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, query := range []string{
		`DELETE FROM observations WHERE run_id=?`,
		`DELETE FROM check_results WHERE run_id=?`,
		`DELETE FROM finding_history WHERE run_id=?`,
		`DELETE FROM assessment_runs WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// encodeBounded marshals a value and rejects it if it exceeds the documented
// per-record size limit.
func encodeBounded(v any, limit int) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("assessment record exceeds size limit")
	}
	return b, nil
}
