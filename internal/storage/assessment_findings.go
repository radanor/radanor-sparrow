package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// ApplyFindings reconciles this run's check results against stored findings and
// returns the number of new and currently-open findings.
//
// The lifecycle rules are deliberately strict and evidence-based:
//
//   - Only a FAIL produces a finding. UNKNOWN, NOT_SUPPORTED and STALE are not
//     evidence that a weakness is gone, so they can never resolve one.
//   - A finding is resolved only by a fresh, reliable PASS.
//   - A resolved finding that FAILs again is reopened, not silently duplicated.
//   - Failing checks whose evidence could not be collected leave existing
//     findings untouched.
//
// Rule versions are handled explicitly. A finding's identity includes the rule
// version, so a revised rule mints a new identity rather than silently
// continuing the old one. Without explicit supersession that left two findings
// for one weakness: the old rule's finding open forever and the new rule's
// finding alongside it. Supersession closes that gap honestly:
//
//   - current rule FAILs  -> the open finding from an older rule is marked
//     superseded, and the new rule's finding carries the weakness forward;
//   - current rule passes on fresh evidence -> the older rule's open finding is
//     resolved, which is genuine remediation evidence from the rule now in use;
//   - current rule is inconclusive -> the older rule's finding is left open,
//     because a rule that could not evaluate the check is not evidence of a fix.
//
// A rule change alone never resolves a weakness, and supersession is recorded
// in finding_history so a retired rule is distinguishable from a fix.
//
// All of this runs in one short transaction; the writes are bounded by the
// number of checks, so monitoring ingestion is not blocked.
func (s *Store) ApplyFindings(ctx context.Context, runID int64, results []assessment.CheckResult) (int, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	newCount, openCount := 0, 0
	now := time.Now().UnixNano()

	for _, r := range results {
		fingerprint := assessment.Fingerprint(r.CheckID, r.CheckVersion, r.Observation.Platform, r.Observation)
		evidence, err := encodeBounded(r.Observation, assessment.MaxObservationBytes)
		if err != nil {
			return 0, 0, err
		}
		limits, err := encodeBounded(r.Limitations, assessment.MaxObservationBytes)
		if err != nil {
			return 0, 0, err
		}
		severity := string(r.Severity)
		if severity == "" {
			severity = string(assessment.SeverityMedium)
		}

		existing, err := loadFinding(ctx, tx, fingerprint)
		if err != nil {
			return 0, 0, err
		}
		if existing == nil {
			// Only a confirmed weakness creates a finding. A PASS or an
			// inconclusive result for a check with no prior finding is not
			// evidence that an older rule's weakness is gone either, unless the
			// rule now in use positively verified it.
			if !assessment.GeneratesFinding(r.Result) {
				if err := settleOlderRuleFinding(ctx, tx, r, runID, now); err != nil {
					return 0, 0, err
				}
				continue
			}
			// A confirmed weakness under the rule now in use takes over from any
			// open finding an older rule raised for the same check, so a rule
			// bump can never leave two open findings for one weakness.
			if err := supersedeOpenRuleInstance(ctx, tx, r, fingerprint, runID, now); err != nil {
				return 0, 0, err
			}
			lifecycle := assessment.LifecycleNew
			if err := insertFinding(ctx, tx, fingerprint, r, severity, string(lifecycle), evidence, limits, now, runID); err != nil {
				return 0, 0, err
			}
			if err := insertHistory(ctx, tx, fingerprint, runID, string(lifecycle), string(r.Result), r.Detail, now); err != nil {
				return 0, 0, err
			}
			newCount++
			openCount++
			continue
		}

		// The fingerprint encodes the rule version, so a finding found here was
		// recorded by this same rule; a version change always takes the branch
		// above. Supersession, not a silent continuation, is what retires the
		// older instance.
		lifecycle := existing.Lifecycle

		next, changed := assessment.Transition(lifecycle, r)
		if changed {
			// resolved_ns and reopened_ns are only written by the transition that
			// sets them; passing NULL otherwise used to erase an earlier
			// resolution every time the lifecycle moved again.
			if _, err := tx.ExecContext(ctx, `UPDATE findings SET lifecycle=?,severity=?,evidence_json=?,summary=?,last_seen_ns=?,resolved_ns=COALESCE(?,resolved_ns),reopened_ns=COALESCE(?,reopened_ns),run_id=? WHERE fingerprint=?`,
				string(next), severity, evidence, r.Detail, now, resolvedTime(next, now), reopenedTime(next, now), runID, fingerprint); err != nil {
				return 0, 0, err
			}
			if err := insertHistory(ctx, tx, fingerprint, runID, string(next), string(r.Result), r.Detail, now); err != nil {
				return 0, 0, err
			}
		} else {
			// The weakness is unchanged (or could not be verified this run).
			// The finding stays open and its evidence is refreshed without the
			// lifecycle moving; the inconclusive case is recorded in history so
			// the absence of a resolution is auditable.
			if _, err := tx.ExecContext(ctx, `UPDATE findings SET last_seen_ns=?,severity=?,evidence_json=?,summary=?,run_id=? WHERE fingerprint=?`,
				now, severity, evidence, r.Detail, runID, fingerprint); err != nil {
				return 0, 0, err
			}
			if !assessment.GeneratesFinding(r.Result) {
				if err := insertHistory(ctx, tx, fingerprint, runID, string(lifecycle), string(r.Result), "not verified as fixed this run; the finding remains open", now); err != nil {
					return 0, 0, err
				}
			}
		}

		if next.IsOpen() {
			openCount++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return newCount, openCount, nil
}

// settleOlderRuleFinding handles a check result whose rule produced no
// weakness but for which an older rule has an open finding. A fresh pass is
// genuine remediation evidence and resolves it; anything inconclusive leaves it
// open, because a rule that could not evaluate the check is not a fix.
func settleOlderRuleFinding(ctx context.Context, tx *sql.Tx, r assessment.CheckResult, runID, now int64) error {
	target, err := openRuleInstance(ctx, tx, r, "")
	if err != nil || target == "" {
		return err
	}
	var lifecycle, version string
	if err := tx.QueryRowContext(ctx, `SELECT lifecycle,check_version FROM findings WHERE fingerprint=?`, target).Scan(&lifecycle, &version); err != nil {
		return err
	}
	next, changed := assessment.Transition(assessment.Lifecycle(lifecycle), r)
	if !changed {
		if err := insertHistory(ctx, tx, target, runID, lifecycle, string(r.Result),
			"not verified as fixed this run; the finding remains open (the check rule is now version "+r.CheckVersion+")", now); err != nil {
			return err
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET lifecycle=?,last_seen_ns=?,resolved_ns=COALESCE(?,resolved_ns),reopened_ns=COALESCE(?,reopened_ns),run_id=? WHERE fingerprint=?`,
		string(next), now, resolvedTime(next, now), reopenedTime(next, now), runID, target); err != nil {
		return err
	}
	detail := "verified as remediated under check rule version " + r.CheckVersion
	if version != r.CheckVersion {
		detail = "the check rule changed from version " + version + " to " + r.CheckVersion + ", which reports this weakness as fixed"
	}
	return insertHistory(ctx, tx, target, runID, string(next), string(r.Result), detail, now)
}

// supersedeOpenRuleInstance retires an open finding an older rule raised for
// the same check on the same platform, so the current rule's finding is the
// only one tracking the weakness. The retirement is recorded in history as a
// supersession rather than a resolution.
func supersedeOpenRuleInstance(ctx context.Context, tx *sql.Tx, r assessment.CheckResult, fingerprint string, runID, now int64) error {
	target, err := openRuleInstance(ctx, tx, r, fingerprint)
	if err != nil || target == "" {
		return err
	}
	var version string
	if err := tx.QueryRowContext(ctx, `SELECT check_version FROM findings WHERE fingerprint=?`, target).Scan(&version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET lifecycle=?,last_seen_ns=?,run_id=? WHERE fingerprint=?`,
		string(assessment.LifecycleSuperseded), now, runID, target); err != nil {
		return err
	}
	detail := "the check rule that reported this finding (version " + version + ") was replaced by version " + r.CheckVersion +
		"; the weakness is carried forward under the new rule and was not verified as remediated"
	return insertHistory(ctx, tx, target, runID, string(assessment.LifecycleSuperseded), string(r.Result), detail, now)
}

// openRuleInstance finds the open finding recorded for the same check on the
// same platform under any other rule version. The findings table is bounded by
// the compiled check set, so this scan stays small.
func openRuleInstance(ctx context.Context, tx *sql.Tx, r assessment.CheckResult, exclude string) (string, error) {
	var fingerprint string
	err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM findings WHERE check_id=? AND platform=? AND fingerprint!=? AND lifecycle IN ('new','continuing','reopened') ORDER BY id DESC LIMIT 1`,
		r.CheckID, r.Observation.Platform, exclude).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return fingerprint, err
}

func resolvedTime(l assessment.Lifecycle, now int64) any {
	if l == assessment.LifecycleResolved {
		return now
	}
	return nil
}

func reopenedTime(l assessment.Lifecycle, now int64) any {
	if l == assessment.LifecycleReopened {
		return now
	}
	return nil
}

type storedFinding struct {
	Lifecycle    assessment.Lifecycle
	CheckVersion string
}

func loadFinding(ctx context.Context, tx *sql.Tx, fingerprint string) (*storedFinding, error) {
	var f storedFinding
	var lifecycle, version string
	err := tx.QueryRowContext(ctx, `SELECT lifecycle,check_version FROM findings WHERE fingerprint=?`, fingerprint).Scan(&lifecycle, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f.Lifecycle = assessment.Lifecycle(lifecycle)
	f.CheckVersion = version
	return &f, nil
}

func insertFinding(ctx context.Context, tx *sql.Tx, fingerprint string, r assessment.CheckResult, severity, lifecycle string, evidence, limits []byte, now int64, runID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO findings(fingerprint,check_id,check_version,platform,severity,lifecycle,title,summary,action,verify_method,limitations_json,evidence_json,first_seen_ns,last_seen_ns,run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		fingerprint, r.CheckID, r.CheckVersion, r.Observation.Platform, severity, lifecycle, r.Title, r.Detail, r.Action, r.VerifyMethod, limits, evidence, now, now, runID)
	return err
}

func insertHistory(ctx context.Context, tx *sql.Tx, fingerprint string, runID int64, lifecycle, result, detail string, now int64) error {
	if len(detail) > 512 {
		detail = detail[:512]
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO finding_history(fingerprint,run_id,lifecycle,result,detail,recorded_ns) VALUES(?,?,?,?,?,?)`,
		fingerprint, runID, lifecycle, result, detail, now)
	return err
}
