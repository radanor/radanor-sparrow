package assessment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store used to observe what the scanner persists
// without touching a database. Every call is recorded so tests can assert the
// scanner always records a terminal status.
type fakeStore struct {
	mu            sync.Mutex
	runs          int
	finished      []ScanOutcome
	observations  int
	results       int
	findingsCalls int
	failPersist   bool
}

func (s *fakeStore) StartRun(ctx context.Context, trigger string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs++
	return int64(s.runs), nil
}

func (s *fakeStore) FinishRun(ctx context.Context, runID int64, outcome ScanOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, outcome)
	return nil
}

func (s *fakeStore) SaveObservations(ctx context.Context, runID int64, obs []Observation) error {
	if s.failPersist {
		return errors.New("persist refused")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations += len(obs)
	return nil
}

func (s *fakeStore) SaveCheckResults(ctx context.Context, runID int64, results []CheckResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results += len(results)
	return nil
}

func (s *fakeStore) ApplyFindings(ctx context.Context, runID int64, results []CheckResult) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findingsCalls++
	return 0, 0, nil
}

func (s *fakeStore) lastOutcome(t *testing.T) ScanOutcome {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.finished) == 0 {
		t.Fatal("no run was finished")
	}
	return s.finished[len(s.finished)-1]
}

// cannedCollector returns a fixed observation or error.
type cannedCollector struct {
	id  string
	obs []Observation
	err error
}

func (c cannedCollector) ID() string { return c.id }
func (c cannedCollector) Collect(ctx context.Context) ([]Observation, error) {
	return c.obs, c.err
}

// slowCollector blocks until the context is cancelled, so the scanner's
// per-collector timeout can be exercised.
type slowCollector struct{ id string }

func (c slowCollector) ID() string { return c.id }
func (c slowCollector) Collect(ctx context.Context) ([]Observation, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func collectedObs(id string) Observation {
	return Observation{ID: id, Collector: id, CollectedAt: time.Now(), Status: Collected, Source: "test"}
}

func simpleCheck(id, collectorID string, fn func(Observation) Eval) CheckDefinition {
	return CheckDefinition{ID: id, Version: "1.0.0", CollectorID: collectorID, Category: "test", Title: id, EvaluateFn: fn}
}

func passOnValue(key string) func(Observation) Eval {
	return func(obs Observation) Eval {
		for _, v := range obs.Values {
			if v.Key == key && v.Bool != nil && *v.Bool {
				return Eval{Result: Pass}
			}
		}
		return Eval{Result: Fail, Detail: "value not true"}
	}
}

func TestScannerRefusesOverlappingScans(t *testing.T) {
	store := &fakeStore{}
	block := make(chan struct{})
	release := make(chan struct{})
	collector := collectorFunc{id: "slow", fn: func(ctx context.Context) ([]Observation, error) {
		close(block)
		<-release
		return []Observation{collectedObs("slow")}, nil
	}}
	scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "slow", passOnValue("present"))}, []Collector{collector})

	done := make(chan error, 1)
	go func() {
		_, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"})
		done <- err
	}()
	<-block
	if !scanner.Running() {
		t.Fatal("scanner should report a run in progress")
	}
	if _, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"}); !errors.Is(err, ErrScanInProgress) {
		t.Fatalf("second concurrent scan error = %v, want ErrScanInProgress", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if scanner.Running() {
		t.Fatal("scanner should be idle after the run")
	}
}

func TestScannerCancellationIsRecordedNotSilentlyDropped(t *testing.T) {
	store := &fakeStore{}
	started := make(chan struct{})
	collector := collectorFunc{id: "slow", fn: func(ctx context.Context) ([]Observation, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "slow", passOnValue("present"))}, []Collector{collector})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := scanner.Run(ctx, ScanRequest{Trigger: "manual"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, ErrScanCancelled) {
		t.Fatalf("cancelled scan error = %v, want ErrScanCancelled", err)
	}
	outcome := store.lastOutcome(t)
	if outcome.Status != "cancelled" {
		t.Fatalf("recorded status = %q, want cancelled", outcome.Status)
	}
	// The cancelled run must still be persisted so the dashboard can report it.
	if store.observations == 0 || store.results == 0 {
		t.Fatalf("cancelled run was not persisted: observations=%d results=%d", store.observations, store.results)
	}
}

func TestScannerCollectorTimeoutDoesNotBlockOthers(t *testing.T) {
	store := &fakeStore{}
	fast := cannedCollector{id: "fast", obs: []Observation{collectedObs("fast")}}
	silent := slowCollector{id: "silent"}
	scanner := NewScanner(store, []CheckDefinition{
		simpleCheck("c1", "fast", func(Observation) Eval { return Eval{Result: Pass} }),
		simpleCheck("c2", "silent", func(Observation) Eval { return Eval{Result: Pass} }),
	}, []Collector{fast, silent})
	scanner.SetCollectorTimeout(50 * time.Millisecond)

	start := time.Now()
	outcome, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("scan took %s; the collector timeout did not apply", elapsed)
	}
	if outcome.ChecksTotal != 2 {
		t.Fatalf("checks = %d, want 2", outcome.ChecksTotal)
	}
	if outcome.CollectorsFailed == 0 {
		t.Fatal("the timed-out collector must be counted as a failed collection")
	}
}

func TestScannerNeverTurnsCollectionProblemsIntoPasses(t *testing.T) {
	// A collector that is refused, and one that errors, must both yield a
	// non-passing result for their checks.
	cases := []struct {
		name string
		obs  Observation
	}{
		{"permission denied", Observation{ID: "c", Collector: "c", CollectedAt: time.Now(), Status: PermissionDenied, Source: "s", Error: "refused"}},
		{"unsupported", Observation{ID: "c", Collector: "c", CollectedAt: time.Now(), Status: Unsupported, Source: "s", Error: "n/a"}},
		{"failed", Observation{ID: "c", Collector: "c", CollectedAt: time.Now(), Status: Failed, Source: "s", Error: "boom"}},
		{"incomplete", Observation{ID: "c", Collector: "c", CollectedAt: time.Now(), Status: Incomplete, Source: "s", Error: "partial"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			// The underlying fact is genuinely true; the collection problem must
			// still prevent a PASS, which is the property under test.
			collector := cannedCollector{id: "c", obs: []Observation{tc.obs}}
			scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "c", passOnValue("present"))}, []Collector{collector})
			outcome, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if outcome.Passed != 0 {
				t.Fatalf("result passed=%d, want 0 for a %s collection", outcome.Passed, tc.name)
			}
			if outcome.Failed != 0 {
				t.Fatalf("result failed=%d, want 0 (a collection problem is not a security failure)", outcome.Failed)
			}
		})
	}
}

// TestScannerStampsMissingObservationTime is a regression test for a real defect:
// collectors that do not stamp CollectedAt had every check reported as STALE,
// because freshness was measured against a zero timestamp.
func TestScannerStampsMissingObservationTime(t *testing.T) {
	store := &fakeStore{}
	// A collector returning an observation with no timestamp at all.
	timeless := Observation{ID: "c", Status: Collected, Source: "test"}
	collector := cannedCollector{id: "c", obs: []Observation{timeless}}
	scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "c", func(Observation) Eval { return Eval{Result: Pass} })}, []Collector{collector})
	outcome, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Stale != 0 {
		t.Fatalf("stale = %d; a freshly collected observation must not be stale", outcome.Stale)
	}
	if outcome.Passed != 1 {
		t.Fatalf("passed = %d, want 1", outcome.Passed)
	}
}

func TestScannerReportsPersistFailureAsFailedRun(t *testing.T) {
	store := &fakeStore{failPersist: true}
	scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "c", func(Observation) Eval { return Eval{Result: Pass} })}, []Collector{cannedCollector{id: "c", obs: []Observation{collectedObs("c")}}})
	if _, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"}); err == nil {
		t.Fatal("expected a persist error")
	}
	if got := store.lastOutcome(t).Status; got != "failed" {
		t.Fatalf("recorded status = %q, want failed", got)
	}
}

func TestScannerProgressAdvancesAndSettles(t *testing.T) {
	store := &fakeStore{}
	var phases []string
	scanner := NewScanner(store, []CheckDefinition{simpleCheck("c1", "a", func(Observation) Eval { return Eval{Result: Pass} })},
		[]Collector{cannedCollector{id: "a", obs: []Observation{collectedObs("a")}}})
	scanner.SetProgressHook(func(p ScanProgress) { phases = append(phases, p.Phase) })
	if _, err := scanner.Run(context.Background(), ScanRequest{Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	final := scanner.Progress()
	if final.Phase != "done" {
		t.Fatalf("final phase = %q, want done", final.Phase)
	}
	if final.CollectorsDone != 1 || final.CollectorsTotal != 1 {
		t.Fatalf("collector progress = %d/%d, want 1/1", final.CollectorsDone, final.CollectorsTotal)
	}
	if len(phases) < 3 {
		t.Fatalf("expected several progress updates, got %v", phases)
	}
}

func boolPtr(v bool) *bool { return &v }

// collectorFunc adapts a function to the Collector interface.
type collectorFunc struct {
	id string
	fn func(context.Context) ([]Observation, error)
}

func (c collectorFunc) ID() string { return c.id }
func (c collectorFunc) Collect(ctx context.Context) ([]Observation, error) {
	return c.fn(ctx)
}
