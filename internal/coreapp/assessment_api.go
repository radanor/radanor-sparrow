package coreapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/config"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// CE-5 assessment operations served over the authenticated core IPC socket.
//
// Every operation is read-only except `assessment_scan` (which starts a scan)
// and `assessment_cancel` (which stops the active one). Neither accepts a
// command, a path, or any other attacker-controlled input: a scan always runs
// the fixed, reviewed collector set, and cancellation only signals the scanner's
// own context. There is deliberately no endpoint that takes an executable, a
// filesystem path, or a check list from the caller.

const (
	assessmentStatusSchema   = "shield.assessment_status/v1"
	assessmentChecksSchema   = "shield.assessment_checks/v1"
	assessmentFindingsSchema = "shield.assessment_findings/v1"
	assessmentFindingSchema  = "shield.assessment_finding/v1"
	assessmentRunsSchema     = "shield.assessment_runs/v1"
	assessmentRunSchema      = "shield.assessment_run/v1"
	assessmentDeviceSchema   = "shield.device_info/v1"
	assessmentScanSchema     = "shield.assessment_scan/v1"

	// minAssessInterval is the most frequent automatic assessment the scheduler
	// will honor. It keeps the periodic path deliberately conservative.
	minAssessInterval = time.Hour
	// defaultAssessMaxRuns bounds how many assessment runs are retained.
	defaultAssessMaxRuns = 200
)

type assessmentAPI struct {
	scanner *assessment.Scanner
	store   *storage.Store
	logger  *slog.Logger

	mu        sync.Mutex
	cancel    context.CancelFunc
	active    bool
	lastRun   *assessment.ScanOutcome
	lastError string
	scheduler schedulerConfig
	nextScan  time.Time

	// reload signals the scheduler loop that the persisted schedule changed, so
	// a settings update takes effect without restarting the core. It is buffered
	// and sent to non-blockingly.
	reload chan struct{}

	// onScanComplete is invoked after a scan finishes successfully, so the core
	// can refresh related state (the CE-6 inventory) without the scanner having
	// to know about it. It must not block.
	onScanComplete func()
}

// setOnScanComplete installs the post-scan hook.
func (a *assessmentAPI) setOnScanComplete(fn func()) {
	a.mu.Lock()
	a.onScanComplete = fn
	a.mu.Unlock()
}

// notifyScanComplete runs the post-scan hook, if any. It must be called without
// holding a.mu.
func (a *assessmentAPI) notifyScanComplete() {
	a.mu.Lock()
	fn := a.onScanComplete
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Where the effective schedule comes from. The value is reported to the UI, so
// it must name the configuration that is actually in force: reporting the
// compiled-in default while a stored preference is driving the schedule is a
// misleading answer, not a cosmetic one.
const (
	schedulerSourceDisabled    = "disabled"
	schedulerSourceEnvironment = "environment"
	schedulerSourceSettings    = "settings"
)

// applySchedule installs a new schedule and wakes the scheduler loop. It is how
// a settings update changes the schedule at runtime.
func (a *assessmentAPI) applySchedule(enabled bool, interval time.Duration) {
	a.applyScheduleFrom(enabled, interval, schedulerSourceSettings)
}

// applyScheduleFrom is applySchedule with an explicit provenance for the value,
// used at startup when the resolved schedule may come from the environment or
// from the user's stored preferences.
//
// An interval of zero is never installed: it would report a schedule with no
// interval, and the scheduler loop requires a positive interval before it will
// run anything. The previously installed interval is kept, which is what makes a
// later "enable only" request work.
func (a *assessmentAPI) applyScheduleFrom(enabled bool, interval time.Duration, source string) {
	a.mu.Lock()
	a.scheduler.Enabled = enabled
	if interval > 0 {
		a.scheduler.Interval = interval
	}
	a.scheduler.ConfigSource = source
	a.mu.Unlock()
	if a.reload != nil {
		select {
		case a.reload <- struct{}{}:
		default:
		}
	}
}

// setScheduleSource records where the effective schedule came from without
// changing the schedule itself.
func (a *assessmentAPI) setScheduleSource(source string) {
	a.mu.Lock()
	a.scheduler.ConfigSource = source
	a.mu.Unlock()
}

// schedulerConfigSnapshot returns the current schedule for the settings API.
func (a *assessmentAPI) schedulerConfigSnapshot() schedulerConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scheduler
}

type schedulerConfig struct {
	Enabled      bool
	Interval     time.Duration
	MaxRetained  int
	ConfigSource string
}

// schedulerFromEnv reads the conservative scheduler configuration. Automatic
// assessments are disabled unless explicitly configured, and an interval below
// the floor is refused rather than silently clamped, so a mistyped value cannot
// turn into an aggressive scan loop.
func schedulerFromEnv() (schedulerConfig, error) {
	cfg := schedulerConfig{MaxRetained: defaultAssessMaxRuns, ConfigSource: schedulerSourceDisabled}
	if raw := envDuration("ASSESS_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, errors.New("invalid " + config.EnvPrefix + "ASSESS_INTERVAL")
		}
		if d < minAssessInterval {
			return cfg, errors.New(config.EnvPrefix + "ASSESS_INTERVAL is below the one-hour minimum")
		}
		cfg.Enabled = true
		cfg.Interval = d
		cfg.ConfigSource = schedulerSourceEnvironment
	}
	if raw := envInt("ASSESS_MAX_RUNS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 10 || n > 5000 {
			return cfg, errors.New("invalid " + config.EnvPrefix + "ASSESS_MAX_RUNS")
		}
		cfg.MaxRetained = n
	}
	return cfg, nil
}

// envDuration returns the value of an environment variable, or the empty
// string when it is unset.
func envDuration(suffix string) string { return config.Env(suffix) }

// envInt returns the value of a branded setting, or the empty string when it is
// unset under either the current or the legacy environment variable name.
func envInt(suffix string) string { return config.Env(suffix) }

// checksForPlatform returns the check definitions compiled for this platform.
func checksForPlatform() []assessment.CheckDefinition { return collectors.PlatformChecks() }

// running reports whether a scan is currently active or has been reserved but
// not yet started. It is the single source of truth for "is a scan in flight".
func (a *assessmentAPI) running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active || a.scanner.Running()
}

// handle dispatches one assessment IPC operation.
func (a *assessmentAPI) handle(req ipc.Request) ipc.Response {
	switch req.Operation {
	case "assessment_scan":
		return a.startScan(req.Value)
	case "assessment_cancel":
		return a.cancelScan()
	case "assessment_status":
		return a.status()
	case "assessment_device":
		return a.device()
	case "assessment_checks":
		return a.checks(req.Value)
	case "assessment_findings":
		return a.findings(req.Value)
	case "assessment_finding":
		return a.finding(req.Value)
	case "assessment_runs":
		return a.runs(req.Value)
	case "assessment_run":
		return a.run(req.Value)
	default:
		return ipc.Response{Error: "unsupported operation"}
	}
}

type deviceInfoResponse struct {
	Schema           string                      `json:"schema"`
	State            string                      `json:"state"`
	CollectionStatus assessment.CollectionStatus `json:"collection_status,omitempty"`
	CollectedAt      *time.Time                  `json:"collected_at,omitempty"`
	OperatingSystem  string                      `json:"operating_system,omitempty"`
	Architecture     string                      `json:"architecture,omitempty"`
	Platform         string                      `json:"platform,omitempty"`
	Stale            bool                        `json:"stale"`
	Note             string                      `json:"note"`
}

// device exposes a deliberately small projection of the stored platform
// observation. It never returns arbitrary collector values, source paths or
// errors, and it only reads evidence from a completed assessment.
func (a *assessmentAPI) device() ipc.Response {
	snapshot, err := a.store.LatestObservation(context.Background(), "platform")
	if err != nil {
		a.logger.Error("device information read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	// Both absences keep the state "missing" so the contract stays additive; the
	// note carries the part that differs, because "run an assessment" is wrong
	// advice for a user whose latest assessment simply recorded nothing.
	resp := deviceInfoResponse{Schema: assessmentDeviceSchema, State: "missing", Note: missingDeviceNote(snapshot.CompletedAssessment)}
	if !snapshot.Found {
		return marshalBounded(resp)
	}
	obs := snapshot.Observation
	resp.CollectionStatus = obs.Status
	resp.CollectedAt = &obs.CollectedAt
	// A failed, unsupported or permission-denied collection cannot establish
	// identity, even when a legacy row happens to retain old values.
	switch {
	case obs.Status != assessment.Collected && obs.Status != assessment.Incomplete:
		resp.State = "unavailable"
		resp.Note = "The latest completed assessment could not read device information."
		return marshalBounded(resp)
	}

	resp.Platform = boundedDeviceValue(obs.Platform)
	for _, value := range obs.Values {
		if value.Kind != assessment.ValueString {
			continue
		}
		switch value.Key {
		case "pretty_name", "product_name":
			if resp.OperatingSystem == "" {
				resp.OperatingSystem = boundedDeviceValue(value.Text)
			}
		case "arch":
			resp.Architecture = boundedDeviceValue(value.Text)
		}
	}
	resp.Stale = time.Since(obs.CollectedAt) > assessment.DefaultCheckFreshness
	switch {
	case resp.Stale:
		resp.State = "stale"
		resp.Note = "Device information is from an assessment whose evidence is stale; run a new assessment to refresh it."
	case obs.Status == assessment.Collected && resp.OperatingSystem != "" && resp.Architecture != "":
		resp.State = "available"
		resp.Note = "Device information was read from the latest completed assessment."
	default:
		// Only collected or incomplete evidence reaches this point (the status
		// filter above), so anything short of a full identity is partial.
		resp.State = "incomplete"
		resp.Note = "The latest completed assessment recorded only part of the device information."
	}
	return marshalBounded(resp)
}

// missingDeviceNote explains an absent device projection with a fixed, bounded
// string that matches what actually happened.
func missingDeviceNote(completedAssessment bool) string {
	if completedAssessment {
		return "The latest assessment did not record device information."
	}
	return "Run an assessment to record device information."
}

func boundedDeviceValue(value string) string {
	if len(value) > assessment.MaxTextValue {
		return ""
	}
	return value
}

// startScan begins one manual scan asynchronously. It returns as soon as the
// scan is accepted so the IPC deadline is never spent waiting on collectors;
// progress and the final outcome are read from assessment_status.
func (a *assessmentAPI) startScan(raw string) ipc.Response {
	var body struct{}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid scan request"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	// The reserved flag closes the window between accepting a scan and the
	// scanner's own running flag becoming visible, so two rapid requests can
	// never both start.
	if a.active || a.scanner.Running() {
		a.mu.Unlock()
		cancel()
		return ipc.Response{Error: "a scan is already in progress"}
	}
	a.active = true
	a.cancel = cancel
	a.lastError = ""
	a.mu.Unlock()

	go func() {
		defer cancel()
		outcome, err := a.scanner.Run(ctx, assessment.ScanRequest{Trigger: "manual"})
		a.mu.Lock()
		a.active = false
		a.cancel = nil
		var failed bool
		if err != nil && !errors.Is(err, assessment.ErrScanCancelled) {
			a.lastError = boundedError(err)
			failed = true
			a.logger.Error("assessment scan failed", "error", err)
		} else {
			a.lastError = ""
			a.lastRun = &outcome
			a.logger.Info("assessment scan finished", "run_id", outcome.RunID, "status", outcome.Status, "failed_checks", outcome.Failed, "new_findings", outcome.NewFindings)
		}
		a.mu.Unlock()
		// The post-scan hook runs without the lock held; it only schedules
		// background work and must not block the scan-completion path.
		if !failed {
			a.notifyScanComplete()
		}
	}()

	payload, _ := json.Marshal(map[string]any{"schema": assessmentScanSchema, "accepted": true, "trigger": "manual", "started_at": time.Now()})
	return ipc.Response{OK: true, Value: string(payload)}
}

func (a *assessmentAPI) cancelScan() ipc.Response {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel == nil {
		return ipc.Response{Error: "no scan is in progress"}
	}
	cancel()
	payload, _ := json.Marshal(map[string]any{"schema": assessmentScanSchema, "cancelled": true, "requested_at": time.Now()})
	return ipc.Response{OK: true, Value: string(payload)}
}

type assessmentStatusResponse struct {
	Schema       string                    `json:"schema"`
	GeneratedAt  time.Time                 `json:"generated_at"`
	Availability string                    `json:"availability"`
	Note         string                    `json:"note"`
	Scan         assessment.ScanProgress   `json:"scan"`
	Running      bool                      `json:"running"`
	Scheduler    schedulerStatus           `json:"scheduler"`
	Summary      storage.AssessmentSummary `json:"summary"`
	LastOutcome  *assessment.ScanOutcome   `json:"last_outcome,omitempty"`
	LastError    string                    `json:"last_error,omitempty"`
	Limits       []string                  `json:"limits"`
}

type schedulerStatus struct {
	Enabled      bool   `json:"enabled"`
	Interval     string `json:"interval,omitempty"`
	ConfigSource string `json:"config_source"`
	// NextScanAt is a pointer because a zero time.Time is a struct and is
	// therefore never omitted by `omitempty`: it is serialized as
	// 0001-01-01T00:00:00Z and rendered by the dashboard as a real date that
	// never happened. It is nil whenever no scan is scheduled, so the client
	// shows an unavailable state instead of a fabricated time.
	NextScanAt *time.Time `json:"next_scan_at,omitempty"`
}

func (a *assessmentAPI) status() ipc.Response {
	progress := a.scanner.Progress()
	a.mu.Lock()
	// A reserved-but-not-yet-running scan still counts as in progress, so a
	// status read immediately after accepting a scan never claims the device is
	// idle when a scan has in fact been started.
	running := a.active || a.scanner.Running()
	last := a.lastRun
	lastErr := a.lastError
	next := a.nextScan
	sched := a.scheduler
	a.mu.Unlock()

	// A published next-scan time exists only while the schedule is live. A stale
	// value left behind by a schedule that has just been switched off must not be
	// advertised as a time the core intends to run.
	var nextScan *time.Time
	if sched.Enabled && sched.Interval > 0 && !next.IsZero() {
		nextScan = &next
	}

	summary, err := a.store.Summary(context.Background())
	if err != nil {
		a.logger.Error("assessment summary read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	note := "assessments run inside the background core while the user session is active"
	status := assessmentStatusResponse{
		Schema:       assessmentStatusSchema,
		GeneratedAt:  time.Now(),
		Availability: "available",
		Note:         note,
		Scan:         progress,
		Running:      running,
		Scheduler:    schedulerStatus{Enabled: sched.Enabled, Interval: sched.Interval.String(), ConfigSource: sched.ConfigSource, NextScanAt: nextScan},
		Summary:      summary,
		LastOutcome:  last,
		LastError:    lastErr,
		Limits: []string{
			"only one assessment may run at a time; a second request is refused rather than queued",
			"collectors are unprivileged, so checks that need elevated access report UNKNOWN instead of a verdict",
			"an interrupted scan is recorded as interrupted and never silently completed",
		},
	}
	if sched.Interval == 0 {
		status.Scheduler.Interval = ""
	}
	return marshalBounded(status)
}

type checksResponse struct {
	Schema  string                      `json:"schema"`
	RunID   int64                       `json:"run_id,omitempty"`
	Checks  []storage.CheckResultRecord `json:"checks"`
	Summary storage.AssessmentSummary   `json:"summary"`
	Defs    []checkDefinitionSummary    `json:"definitions,omitempty"`
}

type checkDefinitionSummary struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Severity string   `json:"severity,omitempty"`
	Platform []string `json:"platforms"`
}

// assessmentMaxCheckPage bounds a check-result page, and assessmentMinPage is
// the floor the shrink loop will not go below. A full page of stored check
// results carries up to 8 KiB of evidence each, so it can exceed the IPC frame
// bound; returning a smaller page is better than failing the read.
const (
	assessmentMaxCheckPage = 256
	assessmentMinPage      = 1
)

// encodeShrinkingPage marshals a page whose encoded size depends on the page
// limit, halving the limit until the encoded response fits the IPC response
// bound. It is the same guard the monitoring event reader uses. A truthful
// error is returned when even a single record does not fit, and never a
// truncated or fabricated page.
func encodeShrinkingPage(build func(limit int) (any, error)) ipc.Response {
	limit := assessmentMaxCheckPage
	for {
		value, err := build(limit)
		if err != nil {
			return ipc.Response{Error: err.Error()}
		}
		payload, merr := json.Marshal(value)
		if merr != nil {
			return ipc.Response{Error: "response encoding failed"}
		}
		if len(payload) <= ipc.MaxResponseBytes-2048 {
			return ipc.Response{OK: true, Value: string(payload)}
		}
		if limit <= assessmentMinPage {
			return ipc.Response{Error: "response too large"}
		}
		limit = max(assessmentMinPage, limit/2)
	}
}

func (a *assessmentAPI) checks(raw string) ipc.Response {
	var body struct{}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid checks request"}
	}
	summary, err := a.store.Summary(context.Background())
	if err != nil {
		return ipc.Response{Error: "storage unavailable"}
	}
	defs := a.definitions()
	return encodeShrinkingPage(func(limit int) (any, error) {
		results, runID, rerr := a.store.LatestResults(context.Background(), limit)
		if rerr != nil {
			a.logger.Error("assessment checks read failed", "error", rerr)
			return nil, errors.New("storage unavailable")
		}
		if results == nil {
			results = []storage.CheckResultRecord{}
		}
		return checksResponse{Schema: assessmentChecksSchema, RunID: runID, Checks: results, Summary: summary, Defs: defs}, nil
	})
}

func (a *assessmentAPI) definitions() []checkDefinitionSummary {
	defs := checksForPlatform()
	out := make([]checkDefinitionSummary, 0, len(defs))
	for _, d := range defs {
		out = append(out, checkDefinitionSummary{ID: d.ID, Version: d.Version, Category: d.Category, Title: d.Title, Severity: string(d.Severity), Platform: d.Platforms})
	}
	return out
}

type findingsResponse struct {
	Schema   string                    `json:"schema"`
	Findings []storage.FindingRecord   `json:"findings"`
	Total    int                       `json:"total_open"`
	Summary  storage.AssessmentSummary `json:"summary"`
}

func (a *assessmentAPI) findings(raw string) ipc.Response {
	var body struct {
		Lifecycle string `json:"lifecycle,omitempty"`
		Before    int64  `json:"before,omitempty"`
		Limit     int    `json:"limit,omitempty"`
	}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid findings request"}
	}
	limit, err := clampPage(body.Limit)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	list, err := a.store.Findings(context.Background(), body.Lifecycle, body.Before, limit)
	if err != nil {
		a.logger.Error("assessment findings read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	if list == nil {
		list = []storage.FindingRecord{}
	}
	summary, err := a.store.Summary(context.Background())
	if err != nil {
		return ipc.Response{Error: "storage unavailable"}
	}
	return marshalBounded(findingsResponse{Schema: assessmentFindingsSchema, Findings: list, Total: summary.OpenFindings, Summary: summary})
}

type findingResponse struct {
	Schema  string                  `json:"schema"`
	Finding storage.FindingRecord   `json:"finding"`
	History []storage.HistoryRecord `json:"history"`
}

func (a *assessmentAPI) finding(raw string) ipc.Response {
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid finding request"}
	}
	f, err := a.store.Finding(context.Background(), body.Fingerprint)
	if err != nil {
		return ipc.Response{Error: "invalid fingerprint"}
	}
	if f == nil {
		return ipc.Response{Error: "finding not found"}
	}
	history, err := a.store.FindingHistory(context.Background(), body.Fingerprint, 20)
	if err != nil {
		return ipc.Response{Error: "storage unavailable"}
	}
	if history == nil {
		history = []storage.HistoryRecord{}
	}
	return marshalBounded(findingResponse{Schema: assessmentFindingSchema, Finding: *f, History: history})
}

type runsResponse struct {
	Schema     string              `json:"schema"`
	Runs       []storage.RunRecord `json:"runs"`
	NextBefore int64               `json:"next_before,omitempty"`
}

func (a *assessmentAPI) runs(raw string) ipc.Response {
	var body struct {
		Before int64 `json:"before,omitempty"`
		Limit  int   `json:"limit,omitempty"`
	}
	if err := decodeStrict(raw, &body); err != nil {
		return ipc.Response{Error: "invalid runs request"}
	}
	limit, err := clampPage(body.Limit)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	list, err := a.store.Runs(context.Background(), body.Before, limit)
	if err != nil {
		a.logger.Error("assessment runs read failed", "error", err)
		return ipc.Response{Error: "storage unavailable"}
	}
	if list == nil {
		list = []storage.RunRecord{}
	}
	resp := runsResponse{Schema: assessmentRunsSchema, Runs: list}
	if len(list) == limit {
		resp.NextBefore = list[len(list)-1].ID
	}
	return marshalBounded(resp)
}

type runResponse struct {
	Schema string                      `json:"schema"`
	Run    storage.RunRecord           `json:"run"`
	Checks []storage.CheckResultRecord `json:"checks"`
}

func (a *assessmentAPI) run(raw string) ipc.Response {
	var body struct {
		RunID int64 `json:"run_id"`
	}
	if err := decodeStrict(raw, &body); err != nil || body.RunID <= 0 {
		return ipc.Response{Error: "invalid run request"}
	}
	runs, err := a.store.Runs(context.Background(), body.RunID+1, 1)
	if err != nil || len(runs) != 1 || runs[0].ID != body.RunID {
		return ipc.Response{Error: "run not found"}
	}
	return encodeShrinkingPage(func(limit int) (any, error) {
		results, rerr := a.store.RunResults(context.Background(), body.RunID, limit)
		if rerr != nil {
			a.logger.Error("assessment run read failed", "error", rerr)
			return nil, errors.New("storage unavailable")
		}
		if results == nil {
			results = []storage.CheckResultRecord{}
		}
		return runResponse{Schema: assessmentRunSchema, Run: runs[0], Checks: results}, nil
	})
}

// marshalBounded encodes a response and refuses to exceed the IPC bound, so a
// growing database can never produce an oversized frame.
func marshalBounded(value any) ipc.Response {
	payload, err := json.Marshal(value)
	if err != nil {
		return ipc.Response{Error: "response encoding failed"}
	}
	if len(payload) > ipc.MaxResponseBytes-2048 {
		return ipc.Response{Error: "response too large"}
	}
	return ipc.Response{OK: true, Value: string(payload)}
}

func boundedError(err error) string {
	msg := err.Error()
	if len(msg) > 256 {
		msg = msg[:256]
	}
	return msg
}

// runScheduler periodically starts an assessment. It never overlaps a running
// scan and never retries on failure: a missed window simply waits for the next
// tick, which prevents a retry loop from burning CPU.
func (a *assessmentAPI) runScheduler(ctx context.Context) {
	// The ticker is created stopped and reconfigured whenever the persisted
	// schedule changes, so a settings update is applied without a restart. When
	// the schedule is disabled there is no live ticker at all.
	ticker := time.NewTicker(time.Hour)
	ticker.Stop()
	defer ticker.Stop()
	active := false
	reconfigure := func() {
		a.mu.Lock()
		cfg := a.scheduler
		if cfg.Enabled && cfg.Interval > 0 {
			a.nextScan = time.Now().Add(cfg.Interval)
		} else {
			a.nextScan = time.Time{}
		}
		a.mu.Unlock()
		if cfg.Enabled && cfg.Interval > 0 {
			ticker.Reset(cfg.Interval)
			active = true
			return
		}
		ticker.Stop()
		active = false
	}
	reconfigure()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.reload:
			reconfigure()
			continue
		case <-ticker.C:
			if !active {
				continue
			}
			a.mu.Lock()
			interval := a.scheduler.Interval
			if interval <= 0 {
				// Unreachable while active, but keep nextScan honest.
				interval = minAssessInterval
			}
			a.nextScan = time.Now().Add(interval)
			busy := a.active || a.scanner.Running()
			if !busy {
				a.active = true
			}
			a.mu.Unlock()
			if busy {
				// A missed window simply waits for the next tick; there is no
				// retry loop that could burn CPU.
				a.logger.Info("scheduled assessment skipped", "reason", "a scan is already in progress")
				continue
			}
			scanCtx, cancel := context.WithCancel(ctx)
			a.mu.Lock()
			a.cancel = cancel
			a.mu.Unlock()
			outcome, err := a.scanner.Run(scanCtx, assessment.ScanRequest{Trigger: "scheduled"})
			cancel()
			a.mu.Lock()
			a.active = false
			a.cancel = nil
			var failed bool
			if err != nil && !errors.Is(err, assessment.ErrScanCancelled) {
				a.lastError = boundedError(err)
				failed = true
			} else {
				a.lastRun = &outcome
				a.lastError = ""
			}
			a.mu.Unlock()
			if !failed {
				a.notifyScanComplete()
			}
		}
	}
}
