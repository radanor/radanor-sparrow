package monitoring

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceRestartLoadsLatestDurableCheckpoint(t *testing.T) {
	store := &durableStore{values: map[string][]byte{"a": []byte("initial")}, blocked: map[string]bool{}, fails: map[string]bool{}}
	resumed := make(chan string, 1)
	calls := 0
	source := scriptedSource{id: "a", run: func(ctx context.Context, cp []byte, emit EmitFunc, report Reporter) error {
		calls++
		if calls == 1 {
			e := event("committed", "a")
			e.Checkpoint = []byte("latest")
			if err := emit(ctx, e); err != nil {
				return err
			}
			for store.checkpoint("a") != "latest" {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
			return errors.New("fixture source interruption")
		}
		resumed <- string(cp)
		<-ctx.Done()
		return ctx.Err()
	}}
	config := testConfig()
	config.DurableCheckpoints = true
	engine, err := New(config, store.process, store, source)
	if err != nil {
		t.Fatal(err)
	}
	runEngine(t, engine)
	select {
	case cp := <-resumed:
		if cp != "latest" {
			t.Fatalf("resumed from %q, want latest durable checkpoint", cp)
		}
	case <-time.After(time.Second):
		t.Fatal("source did not restart")
	}
}

type recoveryFaultStore struct {
	*durableStore
	loads                    int
	failLoad, failInvalidate bool
	failed                   atomic.Bool
}

func (s *recoveryFaultStore) Load(ctx context.Context, id string) ([]byte, error) {
	s.loads++
	if s.failLoad && s.loads > 1 {
		s.failed.Store(true)
		return nil, errors.New("fixture checkpoint load failed")
	}
	return s.durableStore.Load(ctx, id)
}
func (s *recoveryFaultStore) Invalidate(context.Context, string, []byte) error {
	if s.failInvalidate {
		s.failed.Store(true)
		return errors.New("fixture checkpoint invalidation failed")
	}
	return nil
}

func TestRecoveryStopsWhenCheckpointStorageFails(t *testing.T) {
	for _, invalidation := range []bool{false, true} {
		store := &recoveryFaultStore{durableStore: &durableStore{values: map[string][]byte{"a": []byte("stored")}, blocked: map[string]bool{}, fails: map[string]bool{}}, failLoad: !invalidation, failInvalidate: invalidation}
		var calls atomic.Int32
		source := scriptedSource{id: "a", run: func(context.Context, []byte, EmitFunc, Reporter) error {
			calls.Add(1)
			if invalidation {
				return ErrInvalidCheckpoint
			}
			return errors.New("fixture interruption")
		}}
		config := testConfig()
		config.DurableCheckpoints = true
		engine, err := New(config, store.process, store, source)
		if err != nil {
			t.Fatal(err)
		}
		cancel := runEngine(t, engine)
		eventually(t, func() bool { return store.failed.Load() && engine.Health()["a"].State == HealthFailed })
		if calls.Load() != 1 || store.checkpoint("a") != "stored" {
			t.Fatal("recovery discarded unreadable checkpoint state")
		}
		cancel()
	}
}

func TestLateProcessingDoesNotClaimFailedSourceHealthy(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	source := scriptedSource{id: "a", run: func(ctx context.Context, _ []byte, emit EmitFunc, r Reporter) error {
		r.ConfirmHealthy()
		if err := emit(ctx, event("before-read-failure", "a")); err != nil {
			return err
		}
		select {
		case <-entered:
			return errors.New("fixture terminal read failure")
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	config := testConfig()
	config.MaxSourceRestarts = 0
	config.ProcessingTimeout = time.Second
	config.MaxRetries = 0
	engine, err := New(config, func(ctx context.Context, _ Event) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	runEngine(t, engine)
	eventually(t, func() bool { return engine.Health()["a"].State == HealthFailed })
	close(release)
	eventually(t, func() bool { return engine.Processed() == 1 })
	if engine.Health()["a"].State != HealthFailed {
		t.Fatal("late earlier event falsely restored a stopped source to healthy")
	}
}
