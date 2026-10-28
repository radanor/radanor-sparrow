package assessment

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// ErrScanInProgress is returned when a scan is requested while one is already
// running. Only one scan may be active per device.
var ErrScanInProgress = errors.New("a scan is already in progress")

// ErrScanCancelled reports that a scan was cancelled before completion.
var ErrScanCancelled = errors.New("scan cancelled")

// persistTimeout bounds the durability step of a scan independently of the
// scan's own cancellation, so cancelled evidence is still recorded.
const persistTimeout = 15 * time.Second

// Collector produces one or more observations about the running system. It
// returns facts only, never security verdicts, and must respect ctx for both
// deadline and cancellation.
type Collector interface {
	ID() string
	Collect(ctx context.Context) ([]Observation, error)
}

// ScanRequest describes one assessment scan.
type ScanRequest struct {
	Trigger string `json:"trigger"` // "manual" or "scheduled"
}

// ScanOutcome summarizes a completed scan.
type ScanOutcome struct {
	RunID            int64     `json:"run_id"`
	Trigger          string    `json:"trigger"`
	Status           string    `json:"status"` // completed | cancelled | failed
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	Duration         string    `json:"duration"`
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

// ScanProgress is a bounded, read-only view of the in-flight scan used by the
// dashboard to show honest progress without exposing internals.
type ScanProgress struct {
	Phase           string `json:"phase"` // idle | collecting | evaluating | persisting | done
	RunID           int64  `json:"run_id,omitempty"`
	Trigger         string `json:"trigger,omitempty"`
	CollectorsDone  int    `json:"collectors_done"`
	CollectorsTotal int    `json:"collectors_total"`
	// StartedAt is a pointer because Go never omits a zero struct: as a
	// time.Time it serialized as 0001-01-01T00:00:00Z while no scan had run,
	// which is a timestamp that never happened. The frontend declares it
	// optional, so nil is the honest representation of "no scan has started".
	StartedAt *time.Time `json:"started_at,omitempty"`
}

// Scanner coordinates collectors and evaluators for one device. It enforces a
// single active scan and bounds parallelism.
type Scanner struct {
	checks     []CheckDefinition
	collectors []Collector
	store      Store
	timeout    time.Duration
	maxWorkers int

	mu       sync.Mutex
	running  bool
	progress ScanProgress

	progressMu sync.Mutex
	hook       func(ScanProgress)
}

// Store is the persistence the scanner needs. It is satisfied by the CE-4
// SQLite store extended for CE-5.
type Store interface {
	StartRun(ctx context.Context, trigger string) (int64, error)
	FinishRun(ctx context.Context, runID int64, outcome ScanOutcome) error
	SaveObservations(ctx context.Context, runID int64, obs []Observation) error
	SaveCheckResults(ctx context.Context, runID int64, results []CheckResult) error
	ApplyFindings(ctx context.Context, runID int64, results []CheckResult) (newCount, openCount int, err error)
}

// NewScanner builds a scanner. Collector timeouts default to 30s and
// parallelism to a small, CPU-bounded value so an assessment cannot starve the
// monitoring engine or the rest of the system.
func NewScanner(store Store, checks []CheckDefinition, collectors []Collector) *Scanner {
	workers := runtime.NumCPU() / 4
	if workers < 1 {
		workers = 1
	}
	if workers > 4 {
		workers = 4
	}
	return &Scanner{checks: checks, collectors: collectors, store: store, timeout: 30 * time.Second, maxWorkers: workers,
		progress: ScanProgress{Phase: "idle", CollectorsTotal: len(collectors)}}
}

// SetCollectorTimeout overrides the per-collector deadline. Values are clamped
// to a sane positive range.
func (s *Scanner) SetCollectorTimeout(d time.Duration) {
	if d <= 0 {
		d = 30 * time.Second
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	s.mu.Lock()
	s.timeout = d
	s.mu.Unlock()
}

// Running reports whether a scan is currently active.
func (s *Scanner) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// SetProgressHook installs a callback invoked whenever the scan advances. The
// hook must not block; the scanner never waits for it to return meaningful work.
func (s *Scanner) SetProgressHook(hook func(ScanProgress)) {
	s.progressMu.Lock()
	s.hook = hook
	s.progressMu.Unlock()
}

// Progress returns a snapshot of the current scan state. It is safe to call at
// any time, including while no scan is running.
func (s *Scanner) Progress() ScanProgress {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.progress
}

// setProgress updates the snapshot and notifies the hook.
func (s *Scanner) setProgress(update ScanProgress) {
	s.progressMu.Lock()
	if update.CollectorsTotal == 0 {
		update.CollectorsTotal = s.progress.CollectorsTotal
	}
	if update.RunID == 0 {
		update.RunID = s.progress.RunID
	}
	if update.Trigger == "" {
		update.Trigger = s.progress.Trigger
	}
	if update.StartedAt == nil {
		update.StartedAt = s.progress.StartedAt
	}
	if update.CollectorsDone == 0 {
		update.CollectorsDone = s.progress.CollectorsDone
	}
	s.progress = update
	hook := s.hook
	snapshot := s.progress
	s.progressMu.Unlock()
	if hook != nil {
		hook(snapshot)
	}
}

// markCollectorDone advances the completed-collector counter.
func (s *Scanner) markCollectorDone() {
	s.progressMu.Lock()
	s.progress.CollectorsDone++
	snapshot := s.progress
	hook := s.hook
	s.progressMu.Unlock()
	if hook != nil {
		hook(snapshot)
	}
}

// Run executes one scan. It refuses to start a second concurrent scan, honors
// ctx cancellation, and always records a terminal run status.
func (s *Scanner) Run(ctx context.Context, req ScanRequest) (ScanOutcome, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ScanOutcome{}, ErrScanInProgress
	}
	s.running = true
	timeout := s.timeout
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	if req.Trigger == "" {
		req.Trigger = "manual"
	}
	started := time.Now()
	startedAt := started
	runID, err := s.store.StartRun(ctx, req.Trigger)
	if err != nil {
		s.setProgress(ScanProgress{Phase: "idle", CollectorsTotal: len(s.collectors)})
		return ScanOutcome{}, fmt.Errorf("start run: %w", err)
	}
	s.setProgress(ScanProgress{Phase: "collecting", RunID: runID, Trigger: req.Trigger, StartedAt: &startedAt, CollectorsTotal: len(s.collectors)})

	obs := s.collect(ctx, timeout)
	s.setProgress(ScanProgress{Phase: "evaluating"})
	results := s.evaluate(obs)

	outcome := ScanOutcome{
		RunID:       runID,
		Trigger:     req.Trigger,
		Status:      "completed",
		StartedAt:   started,
		FinishedAt:  time.Now(),
		ChecksTotal: len(results),
	}
	for _, r := range results {
		switch r.Result {
		case Pass:
			outcome.Passed++
		case Fail:
			outcome.Failed++
		case Unknown:
			outcome.Unknown++
		case NotSupported:
			outcome.NotSupported++
		case Stale:
			outcome.Stale++
		}
	}
	outcome.Duration = outcome.FinishedAt.Sub(started).Round(time.Millisecond).String()
	if ctx.Err() != nil {
		// Cancellation is a real, reported outcome, not a silent gap.
		outcome.Status = "cancelled"
	}
	outcome.CollectorsRun = len(s.collectors)
	// A collector that was refused, was incomplete, or failed is reported as a
	// failed collection. A genuinely unsupported platform is not a failure.
	for _, o := range obs {
		switch o.Status {
		case Failed, Incomplete, PermissionDenied:
			outcome.CollectorsFailed++
		}
	}

	s.setProgress(ScanProgress{Phase: "persisting"})
	// Persistence deliberately runs on its own bounded context: a cancelled
	// scan still records the evidence it managed to gather and its honest
	// terminal status, rather than losing the run to the cancellation.
	persistCtx, persistCancel := context.WithTimeout(context.Background(), persistTimeout)
	defer persistCancel()
	if err := s.persist(persistCtx, runID, obs, results, &outcome); err != nil {
		outcome.Status = "failed"
		_ = s.store.FinishRun(persistCtx, runID, outcome)
		s.setProgress(ScanProgress{Phase: "done"})
		return outcome, fmt.Errorf("persist scan: %w", err)
	}
	if err := s.store.FinishRun(persistCtx, runID, outcome); err != nil {
		s.setProgress(ScanProgress{Phase: "done"})
		return outcome, fmt.Errorf("finish run: %w", err)
	}
	s.setProgress(ScanProgress{Phase: "done"})
	if outcome.Status == "cancelled" {
		return outcome, ErrScanCancelled
	}
	return outcome, nil
}

// persist writes observations and results, then reconciles findings.
func (s *Scanner) persist(ctx context.Context, runID int64, obs []Observation, results []CheckResult, outcome *ScanOutcome) error {
	if err := s.store.SaveObservations(ctx, runID, obs); err != nil {
		return err
	}
	if err := s.store.SaveCheckResults(ctx, runID, results); err != nil {
		return err
	}
	newCount, openCount, err := s.store.ApplyFindings(ctx, runID, results)
	if err != nil {
		return err
	}
	outcome.NewFindings = newCount
	outcome.OpenFindings = openCount
	return nil
}

// collect runs every collector with a per-collector deadline and bounded
// parallelism. A slow or failing collector cannot block the others, and its
// failure is recorded as an observation rather than dropped.
func (s *Scanner) collect(ctx context.Context, timeout time.Duration) []Observation {
	type slot struct {
		obs []Observation
	}
	slots := make([]slot, len(s.collectors))
	sem := make(chan struct{}, s.maxWorkers)
	var wg sync.WaitGroup
	for i, c := range s.collectors {
		wg.Add(1)
		go func(i int, c Collector) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			observations, err := c.Collect(cctx)
			if err != nil {
				observations = append(observations, Observation{
					ID:          c.ID(),
					Collector:   c.ID(),
					CollectedAt: time.Now(),
					Status:      classifyCollectError(cctx, err),
					Source:      c.ID(),
					Error:       boundCollectError(err),
				})
			}
			// The scanner owns the observation timestamp and identity. A collector
			// that forgets to stamp a time would otherwise be measured against a
			// zero timestamp and look infinitely stale, silently downgrading every
			// one of its checks to STALE.
			now := time.Now()
			for j := range observations {
				if observations[j].CollectedAt.IsZero() {
					observations[j].CollectedAt = now
				}
				if observations[j].Collector == "" {
					observations[j].Collector = c.ID()
				}
				if observations[j].Source == "" {
					observations[j].Source = c.ID()
				}
			}
			slots[i].obs = observations
			s.markCollectorDone()
		}(i, c)
	}
	wg.Wait()
	var all []Observation
	for _, sl := range slots {
		all = append(all, sl.obs...)
	}
	return all
}

// evaluate maps each check onto the observation its collector produced,
// yielding one result per check so the dashboard can show a stable, complete
// set including checks that could not be evaluated.
func (s *Scanner) evaluate(obs []Observation) []CheckResult {
	byCollector := map[string]Observation{}
	for _, o := range obs {
		// Keep the first observation per collector; collectors emit a single
		// canonical observation carrying their typed values.
		if _, ok := byCollector[o.Collector]; !ok {
			byCollector[o.Collector] = o
		}
	}
	results := make([]CheckResult, 0, len(s.checks))
	for _, c := range s.checks {
		obs, ok := byCollector[c.CollectorID]
		if !ok {
			// The collector produced nothing at all: report an honest failure
			// rather than inventing a verdict.
			results = append(results, c.Evaluate(Observation{
				ID:          c.ID,
				Collector:   c.CollectorID,
				CollectedAt: time.Now(),
				Status:      Failed,
				Source:      c.CollectorID,
				Error:       "collector returned no observation",
			}))
			continue
		}
		results = append(results, c.Evaluate(obs))
	}
	return results
}
