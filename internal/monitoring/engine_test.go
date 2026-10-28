package monitoring

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedSource is simulation-only test infrastructure. It is not linked into production commands.
type scriptedSource struct {
	id  string
	run func(context.Context, []byte, EmitFunc, Reporter) error
}

func (s scriptedSource) Descriptor() Descriptor {
	return Descriptor{ContractVersion: SourceContractV1, ID: s.id, Platforms: []string{"simulated-test"}, Capabilities: []string{"deterministic simulated delivery"}, RequiredAccess: PrivilegeUser, EventTypes: []string{"test.observation"}, SchemaVersion: EventSchemaV1, Recovery: RecoveryCheckpoint, StartupBehavior: "starts immediately", ShutdownBehavior: "returns on context cancellation", HealthReporting: "explicit simulated confirmation", Limitations: []string{"synthetic test data only"}}
}
func (s scriptedSource) Run(ctx context.Context, cp []byte, emit EmitFunc, report Reporter) error {
	return s.run(ctx, cp, emit, report)
}
func event(id, source string) Event {
	now := time.Now()
	return Event{ID: id, Source: source, Type: "test.observation", Platform: "simulated-test", OccurredAt: now.Add(-time.Millisecond), ObservedAt: now, Evidence: EvidenceTelemetry, SchemaVersion: EventSchemaV1}
}
func testConfig() Config {
	c := DefaultConfig()
	c.PerSourceQueue = 4
	c.CentralQueue = 4
	c.Workers = 1
	c.EnqueueTimeout = 2 * time.Millisecond
	c.ProcessingTimeout = 20 * time.Millisecond
	c.RetryDelay = time.Millisecond
	c.RestartDelay = time.Millisecond
	return c
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func runEngine(t *testing.T, e *Engine) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("engine did not shut down")
		}
	})
	return cancel
}

func TestNormalDeliveryOrderingAndCheckpoint(t *testing.T) {
	store := &memoryCheckpoints{values: map[string][]byte{}}
	var mu sync.Mutex
	var got []string
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		for i := 0; i < 3; i++ {
			e := event(fmt.Sprint(i), "a")
			e.Checkpoint = []byte(e.ID)
			if err := emit(ctx, e); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(_ context.Context, e Event) error { mu.Lock(); got = append(got, e.ID); mu.Unlock(); return nil }, store, s)
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 3 })
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(got) != "[0 1 2]" {
		t.Fatalf("ordering: %v", got)
	}
	if string(store.get("a")) != "2" {
		t.Fatalf("checkpoint not saved")
	}
}

func TestDuplicateSuppressionDoesNotMergeDistinctEvents(t *testing.T) {
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		_ = emit(ctx, event("same", "a"))
		_ = emit(ctx, event("same", "a"))
		_ = emit(ctx, event("distinct", "a"))
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(context.Context, Event) error { return nil }, nil, s)
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 2 && e.Health()["a"].Duplicates == 1 })
}

func TestMalformedAndOversizedEventsAreDropped(t *testing.T) {
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		bad := event("", "a")
		_ = emit(ctx, bad)
		large := event("large", "a")
		large.Metadata = map[string]string{"x": string(make([]byte, MaxValueBytes+1))}
		_ = emit(ctx, large)
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(context.Context, Event) error { return nil }, nil, s)
	runEngine(t, e)
	eventually(t, func() bool { return e.Health()["a"].DroppedEvents == 2 })
	if e.Processed() != 0 {
		t.Fatal("invalid events processed")
	}
}

func TestPermissionUnsupportedCrashAndIsolation(t *testing.T) {
	permission := scriptedSource{id: "permission", run: func(context.Context, []byte, EmitFunc, Reporter) error { return ErrPermissionDenied }}
	unsupported := scriptedSource{id: "unsupported", run: func(context.Context, []byte, EmitFunc, Reporter) error { return ErrUnsupported }}
	crash := scriptedSource{id: "crash", run: func(context.Context, []byte, EmitFunc, Reporter) error { return errors.New("simulated crash") }}
	good := scriptedSource{id: "good", run: func(ctx context.Context, _ []byte, emit EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		_ = emit(ctx, event("ok", "good"))
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.MaxSourceRestarts = 1
	e, _ := New(c, func(context.Context, Event) error { return nil }, nil, permission, unsupported, crash, good)
	runEngine(t, e)
	eventually(t, func() bool {
		h := e.Health()
		return h["permission"].State == HealthPermissionDenied && h["unsupported"].State == HealthUnsupported && h["crash"].State == HealthFailed && e.Processed() == 1
	})
	if e.Health()["crash"].RestartCount != 1 {
		t.Fatal("controlled restart missing")
	}
}

func TestInvalidCheckpointRecoveryAndGap(t *testing.T) {
	store := &memoryCheckpoints{values: map[string][]byte{"a": []byte("bad")}}
	var calls atomic.Int32
	s := scriptedSource{id: "a", run: func(ctx context.Context, cp []byte, _ EmitFunc, r Reporter) error {
		if calls.Add(1) == 1 {
			return ErrInvalidCheckpoint
		}
		if len(cp) != 0 {
			return errors.New("checkpoint was not cleared")
		}
		r.ConfirmHealthy()
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(context.Context, Event) error { return nil }, store, s)
	runEngine(t, e)
	eventually(t, func() bool { return calls.Load() == 2 && e.Health()["a"].MonitoringGaps == 1 })
	if store.invalidated.Load() != 1 {
		t.Fatal("checkpoint not invalidated")
	}
}

func TestOverflowSlowConsumerBackpressureAndCancellation(t *testing.T) {
	blocked := make(chan struct{})
	started := make(chan struct{}, 1)
	var queueErrors atomic.Uint64
	s := scriptedSource{id: "burst", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		for i := 0; i < 100; i++ {
			if errors.Is(emit(ctx, event(fmt.Sprint(i), "burst")), ErrQueueFull) {
				queueErrors.Add(1)
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.PerSourceQueue = 1
	c.CentralQueue = 1
	c.EnqueueTimeout = time.Millisecond
	e, _ := New(c, func(ctx context.Context, _ Event) error {
		select {
		case started <- struct{}{}:
		default:
			{
			}
		}
		select {
		case <-blocked:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil, s)
	cancel := runEngine(t, e)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("processor did not start")
	}
	eventually(t, func() bool { return queueErrors.Load() > 0 && e.Health()["burst"].QueueOverflows > 0 })
	cancel()
	close(blocked)
}

func TestIdleConfirmationAndShutdownGoroutineStability(t *testing.T) {
	base := runtime.NumGoroutine()
	s := scriptedSource{id: "idle", run: func(ctx context.Context, _ []byte, _ EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(context.Context, Event) error { return nil }, nil, s)
	cancel := runEngine(t, e)
	eventually(t, func() bool { return e.Health()["idle"].State == HealthHealthy })
	time.Sleep(20 * time.Millisecond)
	if e.Health()["idle"].State != HealthHealthy {
		t.Fatal("quiet confirmed source marked unhealthy")
	}
	cancel()
	eventually(t, func() bool { return e.Health()["idle"].State == HealthStopped })
	if runtime.NumGoroutine() > base+8 {
		t.Fatal("unexpected goroutine growth")
	}
}

func TestProcessorRetryAndTimeout(t *testing.T) {
	var calls atomic.Int32
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		_ = emit(ctx, event("1", "a"))
		<-ctx.Done()
		return ctx.Err()
	}}
	e, _ := New(testConfig(), func(ctx context.Context, _ Event) error {
		if calls.Add(1) < 3 {
			return errors.New("temporary")
		}
		return nil
	}, nil, s)
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 1 })
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestProcessorReceivesBoundedDeadline(t *testing.T) {
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		_ = emit(ctx, event("1", "a"))
		<-ctx.Done()
		return ctx.Err()
	}}
	deadlineSeen := make(chan time.Duration, 1)
	c := testConfig()
	c.ProcessingTimeout = 25 * time.Millisecond
	e, _ := New(c, func(ctx context.Context, _ Event) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("missing deadline")
		}
		deadlineSeen <- time.Until(deadline)
		return nil
	}, nil, s)
	runEngine(t, e)
	select {
	case remaining := <-deadlineSeen:
		if remaining <= 0 || remaining > c.ProcessingTimeout {
			t.Fatalf("invalid deadline: %v", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("processor not invoked")
	}
}

func TestCheckpointNeverAdvancesPastFailedEarlierEvent(t *testing.T) {
	store := &memoryCheckpoints{values: map[string][]byte{}}
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		first := event("first", "a")
		first.Checkpoint = []byte("one")
		second := event("second", "a")
		second.Checkpoint = []byte("two")
		_ = emit(ctx, first)
		_ = emit(ctx, second)
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.Workers = 2
	c.MaxRetries = 0
	e, _ := New(c, func(_ context.Context, event Event) error {
		if event.ID == "first" {
			time.Sleep(10 * time.Millisecond)
			return errors.New("simulated failure")
		}
		return nil
	}, store, s)
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 1 && e.Health()["a"].DroppedEvents == 1 })
	if got := store.get("a"); len(got) != 0 {
		t.Fatalf("checkpoint advanced to %q", got)
	}
	if e.Health()["a"].MonitoringGaps == 0 {
		t.Fatal("missing processing gap")
	}
}

func TestConcurrentSourceFairness(t *testing.T) {
	makeSource := func(id string, count int) scriptedSource {
		return scriptedSource{id: id, run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
			for i := 0; i < count; i++ {
				if err := emit(ctx, event(fmt.Sprint(i), id)); err != nil && !errors.Is(err, ErrQueueFull) {
					return err
				}
			}
			<-ctx.Done()
			return ctx.Err()
		}}
	}
	var mu sync.Mutex
	seen := map[string]int{}
	c := testConfig()
	c.PerSourceQueue, c.CentralQueue = 128, 128
	e, _ := New(c, func(_ context.Context, event Event) error {
		mu.Lock()
		seen[event.Source]++
		mu.Unlock()
		return nil
	}, nil, makeSource("busy", 100), makeSource("quiet", 1))
	runEngine(t, e)
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return seen["busy"] > 0 && seen["quiet"] == 1 })
}

type memoryCheckpoints struct {
	mu          sync.Mutex
	values      map[string][]byte
	invalidated atomic.Uint64
}

func (m *memoryCheckpoints) Load(_ context.Context, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[id]
	if !ok {
		return nil, ErrCheckpointNotFound
	}
	return append([]byte(nil), v...), nil
}
func (m *memoryCheckpoints) Save(_ context.Context, id string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[id] = append([]byte(nil), v...)
	return nil
}
func (m *memoryCheckpoints) Invalidate(_ context.Context, id string, _ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, id)
	m.invalidated.Add(1)
	return nil
}
func (m *memoryCheckpoints) get(id string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.values[id]...)
}

// durableStore simulates CE-4 storage: the processor commits the event and its
// checkpoint in one call, and permanent checkpoint blocks are honored.
type durableStore struct {
	mu      sync.Mutex
	stored  []string
	values  map[string][]byte
	blocked map[string]bool
	fails   map[string]bool
	saves   atomic.Uint64
}

func (d *durableStore) Load(_ context.Context, id string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.values[id]
	if !ok {
		return nil, ErrCheckpointNotFound
	}
	return append([]byte(nil), v...), nil
}
func (d *durableStore) Save(_ context.Context, id string, v []byte) error {
	d.saves.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.values[id] = append([]byte(nil), v...)
	return nil
}
func (d *durableStore) Invalidate(context.Context, string, []byte) error { return nil }
func (d *durableStore) BlockCheckpoints(source string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.blocked[source] = true
}
func (d *durableStore) process(_ context.Context, e Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stored = append(d.stored, e.ID)
	if d.fails[e.ID] {
		return errors.New("storage failure")
	}
	if len(e.Checkpoint) > 0 && !d.blocked[e.Source] {
		d.values[e.Source] = append([]byte(nil), e.Checkpoint...)
	}
	return nil
}
func (d *durableStore) checkpoint(source string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return string(d.values[source])
}
func (d *durableStore) blockedOn(source string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.blocked[source]
}

func TestDurableCheckpointsRequireBlockingStore(t *testing.T) {
	silent := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, _ EmitFunc, _ Reporter) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.DurableCheckpoints = true
	if _, err := New(c, func(context.Context, Event) error { return nil }, nil, silent); err == nil {
		t.Fatal("durable configuration accepted a nil store")
	}
	if _, err := New(c, func(context.Context, Event) error { return nil }, &memoryCheckpoints{values: map[string][]byte{}}, silent); err == nil {
		t.Fatal("durable configuration accepted a non-blocking store")
	}
}

// With durable storage the engine must not write checkpoints separately: the
// processor already committed event and checkpoint atomically.
func TestDurableCheckpointsAreNotSavedSeparately(t *testing.T) {
	store := &durableStore{values: map[string][]byte{}, blocked: map[string]bool{}, fails: map[string]bool{}}
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		for _, id := range []string{"1", "2"} {
			e := event(id, "a")
			e.Checkpoint = []byte("cp-" + id)
			if err := emit(ctx, e); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.DurableCheckpoints = true
	e, err := New(c, store.process, store, s)
	if err != nil {
		t.Fatal(err)
	}
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 2 })
	if got := store.checkpoint("a"); got != "cp-2" {
		t.Fatalf("durable checkpoint = %q", got)
	}
	if store.saves.Load() != 0 {
		t.Fatalf("engine wrote checkpoints separately: %d saves", store.saves.Load())
	}
}

// A permanently failed event freezes the source's checkpoint; later successful
// events are still stored but cannot advance the replay position.
func TestDurableCheckpointFrozenAfterProcessingFailure(t *testing.T) {
	store := &durableStore{values: map[string][]byte{}, blocked: map[string]bool{}, fails: map[string]bool{"bad": true}}
	s := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		for _, id := range []string{"bad", "after"} {
			e := event(id, "a")
			e.Checkpoint = []byte("cp-" + id)
			if err := emit(ctx, e); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	c := testConfig()
	c.DurableCheckpoints = true
	e, err := New(c, store.process, store, s)
	if err != nil {
		t.Fatal(err)
	}
	runEngine(t, e)
	eventually(t, func() bool { return e.Processed() == 1 && store.blockedOn("a") })
	if got := store.checkpoint("a"); got == "cp-after" {
		t.Fatal("checkpoint advanced past a failed event")
	}
	health := e.Health()["a"]
	if health.State != HealthDegraded || health.DroppedEvents == 0 || health.MonitoringGaps == 0 || health.LastError == "" || health.LastSuccessfulEvent.IsZero() {
		t.Fatalf("failure not visible: %+v", health)
	}
}

func BenchmarkEventValidation(b *testing.B) {
	e := event("stable", "benchmark")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.Validate(time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkDeduplication(b *testing.B) {
	d := newDeduplicator(time.Second, 4096)
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := event(fmt.Sprint(i), "benchmark")
		d.duplicate(e, now)
	}
}

func BenchmarkBoundedPipeline(b *testing.B) {
	c := DefaultConfig()
	c.PerSourceQueue, c.CentralQueue, c.Workers = 1024, 4096, 2
	c.DedupWindow = 0
	var processed atomic.Uint64
	s := scriptedSource{id: "benchmark", run: func(ctx context.Context, _ []byte, emit EmitFunc, _ Reporter) error {
		for i := 0; i < b.N; i++ {
			if err := emit(ctx, event(fmt.Sprint(i), "benchmark")); err != nil {
				return err
			}
		}
		for processed.Load() < uint64(b.N) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				runtime.Gosched()
			}
		}
		return nil
	}}
	e, err := New(c, func(context.Context, Event) error { processed.Add(1); return nil }, nil, s)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.ReportAllocs()
	b.ResetTimer()
	done := make(chan struct{})
	go func() { _ = e.Run(ctx); close(done) }()
	for processed.Load() < uint64(b.N) {
		runtime.Gosched()
	}
	cancel()
	<-done
}
