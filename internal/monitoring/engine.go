package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Processor func(context.Context, Event) error
type GapSink interface {
	RecordGap(context.Context, Gap) error
}
type Config struct {
	// DurableCheckpoints requires a CheckpointBlocker store: events and their
	// checkpoints are committed in one transaction by the processor, and the
	// engine freezes checkpoint advancement for any source whose ordering broke.
	DurableCheckpoints bool
	PerSourceQueue     int
	CentralQueue       int
	Workers            int
	EnqueueTimeout     time.Duration
	ProcessingTimeout  time.Duration
	MaxRetries         int
	RetryDelay         time.Duration
	DedupWindow        time.Duration
	DedupCapacity      int
	MaxSourceRestarts  int
	RestartDelay       time.Duration
}

func DefaultConfig() Config {
	return Config{PerSourceQueue: 128, CentralQueue: 512, Workers: 2, EnqueueTimeout: 50 * time.Millisecond, ProcessingTimeout: 2 * time.Second, MaxRetries: 2, RetryDelay: 10 * time.Millisecond, DedupWindow: 30 * time.Second, DedupCapacity: 4096, MaxSourceRestarts: 2, RestartDelay: 100 * time.Millisecond}
}

type queued struct {
	source  string
	event   Event
	runtime *sourceRuntime
	prior   <-chan struct{}
	done    chan struct{}
}
type sourceRuntime struct {
	source            Source
	queue             chan Event
	previous          chan struct{}
	checkpointBlocked atomic.Bool
}

// MaxRetainedGaps bounds the in-memory gap ring; persisted gaps live in storage.
const MaxRetainedGaps = 256

type Engine struct {
	cfg         Config
	processor   Processor
	checkpoints CheckpointStore
	durable     bool
	sources     []*sourceRuntime
	central     chan queued
	wake        chan struct{}
	health      *healthBook
	dedup       *deduplicator
	gapsMu      sync.Mutex
	gaps        []Gap
	processed   atomic.Uint64
	gapSink     GapSink
}

func (e *Engine) SetGapSink(sink GapSink) { e.gapSink = sink }

func New(cfg Config, processor Processor, checkpoints CheckpointStore, sources ...Source) (*Engine, error) {
	if cfg.PerSourceQueue <= 0 || cfg.CentralQueue <= 0 || cfg.Workers <= 0 || cfg.EnqueueTimeout <= 0 || cfg.ProcessingTimeout <= 0 || cfg.MaxRetries < 0 || cfg.DedupCapacity < 0 || cfg.MaxSourceRestarts < 0 {
		return nil, errors.New("invalid monitoring configuration")
	}
	if cfg.DurableCheckpoints {
		if _, ok := checkpoints.(CheckpointBlocker); !ok {
			return nil, errors.New("durable checkpoints require a checkpoint-blocking store")
		}
	}
	if processor == nil {
		return nil, errors.New("processor is required")
	}
	if checkpoints == nil {
		if cfg.DurableCheckpoints {
			return nil, errors.New("durable checkpoints require a checkpoint-blocking store")
		}
		checkpoints = NoCheckpointStore{}
	}
	e := &Engine{cfg: cfg, processor: processor, checkpoints: checkpoints, durable: cfg.DurableCheckpoints, central: make(chan queued, cfg.CentralQueue), wake: make(chan struct{}, 1), health: newHealthBook(), dedup: newDeduplicator(cfg.DedupWindow, cfg.DedupCapacity)}
	ids := map[string]bool{}
	for _, s := range sources {
		if s == nil {
			return nil, ErrInvalidSource
		}
		d := s.Descriptor()
		if err := d.Validate(); err != nil || ids[d.ID] {
			return nil, ErrInvalidSource
		}
		ids[d.ID] = true
		ready := make(chan struct{})
		close(ready)
		e.sources = append(e.sources, &sourceRuntime{source: s, queue: make(chan Event, cfg.PerSourceQueue), previous: ready})
		e.health.update(d.ID, func(h *Health) { h.State = HealthStopped })
	}
	return e, nil
}
func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, rt := range e.sources {
		wg.Add(1)
		go func(r *sourceRuntime) { defer wg.Done(); e.runSource(ctx, r) }(rt)
	}
	wg.Add(1)
	go func() { defer wg.Done(); e.dispatch(ctx) }()
	for i := 0; i < e.cfg.Workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.work(ctx) }()
	}
	<-ctx.Done()
	wg.Wait()
	interrupted := make(map[string]bool)
	for _, rt := range e.sources {
		if len(rt.queue) > 0 {
			interrupted[rt.source.Descriptor().ID] = true
		}
	}
	for len(e.central) > 0 {
		interrupted[(<-e.central).source] = true
	}
	for id := range interrupted {
		e.recordGap(id, time.Now(), time.Now(), "shutdown interrupted queued processing")
	}
	for _, rt := range e.sources {
		id := rt.source.Descriptor().ID
		e.health.update(id, func(h *Health) {
			if h.State != HealthFailed && h.State != HealthPermissionDenied && h.State != HealthUnsupported {
				h.State = HealthStopped
			}
		})
	}
	return nil
}
func (e *Engine) runSource(ctx context.Context, rt *sourceRuntime) {
	d := rt.source.Descriptor()
	id := d.ID
	e.health.update(id, func(h *Health) { h.State = HealthStarting })
	checkpoint, err := e.checkpoints.Load(ctx, id)
	if err != nil && !errors.Is(err, ErrCheckpointNotFound) {
		e.fail(id, err)
		return
	}
	if errors.Is(err, ErrCheckpointNotFound) {
		checkpoint = nil
	}
	reporter := sourceReporter{engine: e, id: id}
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			checkpoint, err = e.checkpoints.Load(ctx, id)
			if err != nil && !errors.Is(err, ErrCheckpointNotFound) {
				e.fail(id, err)
				return
			}
			if errors.Is(err, ErrCheckpointNotFound) {
				checkpoint = nil
			}
		}
		err = rt.source.Run(ctx, checkpoint, func(callCtx context.Context, event Event) error { return e.enqueue(callCtx, rt, event) }, reporter)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrPermissionDenied) {
			e.health.update(id, func(h *Health) {
				h.State = HealthPermissionDenied
				h.LastFailure = time.Now()
				h.LastError = ErrPermissionDenied.Error()
			})
			return
		}
		if errors.Is(err, ErrUnsupported) {
			e.health.update(id, func(h *Health) {
				h.State = HealthUnsupported
				h.LastFailure = time.Now()
				h.LastError = ErrUnsupported.Error()
			})
			return
		}
		if errors.Is(err, ErrInvalidCheckpoint) {
			if invalidateErr := e.checkpoints.Invalidate(ctx, id, checkpoint); invalidateErr != nil {
				e.recordGap(id, time.Now(), time.Now(), "checkpoint invalidation failed")
				e.fail(id, invalidateErr)
				return
			}
			checkpoint = nil
			e.recordGap(id, time.Now(), time.Now(), "invalid checkpoint")
			e.health.update(id, func(h *Health) { h.State = HealthRecovering; h.RestartCount++ })
			if attempt < e.cfg.MaxSourceRestarts {
				if !wait(ctx, e.cfg.RestartDelay) {
					return
				}
				continue
			}
		}
		if err == nil {
			e.health.update(id, func(h *Health) { h.State = HealthStopped })
			return
		}
		e.fail(id, err)
		if attempt >= e.cfg.MaxSourceRestarts {
			return
		}
		e.health.update(id, func(h *Health) { h.State = HealthRecovering; h.RestartCount++ })
		e.recordGap(id, time.Now(), time.Now(), "source restart")
		if !wait(ctx, e.cfg.RestartDelay) {
			return
		}
	}
}
func (e *Engine) enqueue(ctx context.Context, rt *sourceRuntime, event Event) error {
	id := rt.source.Descriptor().ID
	if event.Source != id || event.Validate(time.Now()) != nil {
		e.blockCheckpoint(rt, "invalid event interrupted source ordering")
		e.health.update(id, func(h *Health) {
			h.DroppedEvents++
			h.State = HealthDegraded
			h.LastFailure = time.Now()
			h.LastError = "invalid event"
		})
		return errors.New("invalid event")
	}
	t := time.NewTimer(e.cfg.EnqueueTimeout)
	defer t.Stop()
	select {
	case rt.queue <- event:
		select {
		case e.wake <- struct{}{}:
		default:
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		e.blockCheckpoint(rt, "queue overflow interrupted source ordering")
		e.health.update(id, func(h *Health) {
			h.QueueOverflows++
			h.DroppedEvents++
			h.State = HealthDegraded
			h.LastFailure = time.Now()
			h.LastError = ErrQueueFull.Error()
		})
		return ErrQueueFull
	}
}

// dispatch visits each source once per pass so a busy source cannot permanently starve another.
func (e *Engine) dispatch(ctx context.Context) {
	for {
		moved := false
		for _, rt := range e.sources {
			select {
			case event := <-rt.queue:
				moved = true
				done := make(chan struct{})
				item := queued{source: rt.source.Descriptor().ID, event: event, runtime: rt, prior: rt.previous, done: done}
				rt.previous = done
				select {
				case e.central <- item:
				case <-ctx.Done():
					return
				}
			default:
			}
		}
		if !moved {
			select {
			case <-ctx.Done():
				return
			case <-e.wake:
			}
		}
	}
}
func (e *Engine) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-e.central:
			select {
			case <-item.prior:
			case <-ctx.Done():
				close(item.done)
				return
			}
			e.process(ctx, item)
			close(item.done)
		}
	}
}
func (e *Engine) process(parent context.Context, item queued) {
	now := time.Now()
	if e.dedup.duplicate(item.event, now) {
		e.health.update(item.source, func(h *Health) { h.Duplicates++ })
		return
	}
	var err error
	for attempt := 0; attempt <= e.cfg.MaxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(parent, e.cfg.ProcessingTimeout)
		err = e.processor(callCtx, item.event)
		cancel()
		if parent.Err() != nil {
			e.recordGap(item.source, time.Now(), time.Now(), "shutdown interrupted event processing")
			return
		}
		if err == nil {
			e.processed.Add(1)
			e.health.update(item.source, func(h *Health) {
				// A source whose ordering broke stays degraded: its replay
				// position is frozen even though later events still persist.
				if !item.runtime.checkpointBlocked.Load() && (h.State == HealthStarting || h.State == HealthHealthy) {
					h.State = HealthHealthy
					h.LastError = ""
					h.LastConfirmation = time.Now()
				}
				h.LastSuccessfulEvent = time.Now()
			})
			// A durable processor already committed the checkpoint inside the same
			// transaction as the event; a separate write would break atomicity.
			if !e.durable && len(item.event.Checkpoint) > 0 && !item.runtime.checkpointBlocked.Load() {
				if saveErr := e.checkpoints.Save(parent, item.source, item.event.Checkpoint); saveErr != nil {
					e.fail(item.source, fmt.Errorf("save checkpoint: %w", saveErr))
				}
			}
			return
		}
		if attempt < e.cfg.MaxRetries && !wait(parent, e.cfg.RetryDelay) {
			return
		}
	}
	e.health.update(item.source, func(h *Health) {
		h.DroppedEvents++
		h.State = HealthDegraded
		h.LastFailure = time.Now()
		h.LastError = boundedError(err)
	})
	e.blockCheckpoint(item.runtime, "processing failure interrupted source ordering")
}

func (e *Engine) blockCheckpoint(rt *sourceRuntime, reason string) {
	if rt.checkpointBlocked.CompareAndSwap(false, true) {
		// A durable processor must stop advancing this source's checkpoint too,
		// otherwise a later event could commit a checkpoint past the failure.
		if e.durable {
			if blocker, ok := e.checkpoints.(CheckpointBlocker); ok {
				blocker.BlockCheckpoints(rt.source.Descriptor().ID)
			}
		}
		e.recordGap(rt.source.Descriptor().ID, time.Now(), time.Now(), reason)
	}
}
func (e *Engine) fail(id string, err error) {
	e.health.update(id, func(h *Health) { h.State = HealthFailed; h.LastFailure = time.Now(); h.LastError = safeError(err) })
}
func (e *Engine) recordGap(id string, start, end time.Time, reason string) {
	if len(reason) > 256 {
		reason = reason[:256]
	}
	e.gapsMu.Lock()
	e.gaps = append(e.gaps, Gap{Source: id, Start: start, End: end, Reason: reason})
	if len(e.gaps) > MaxRetainedGaps {
		e.gaps = append([]Gap(nil), e.gaps[len(e.gaps)-MaxRetainedGaps:]...)
	}
	e.gapsMu.Unlock()
	e.health.update(id, func(h *Health) { h.MonitoringGaps++ })
	if e.gapSink != nil {
		if err := e.gapSink.RecordGap(context.Background(), Gap{Source: id, Start: start, End: end, Reason: reason}); err != nil {
			e.health.update(id, func(h *Health) {
				h.State = HealthDegraded
				h.LastFailure = time.Now()
				h.LastError = "gap persistence failed"
			})
		}
	}
}
func boundedError(err error) string {
	msg := strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
	if len(msg) > 160 {
		msg = msg[:160]
	}
	if msg == "" {
		return "processing failed"
	}
	return msg
}
func safeError(err error) string {
	switch {
	case errors.Is(err, ErrInvalidCheckpoint):
		return ErrInvalidCheckpoint.Error()
	case errors.Is(err, ErrPermissionDenied):
		return ErrPermissionDenied.Error()
	case errors.Is(err, ErrUnsupported):
		return ErrUnsupported.Error()
	default:
		return "source failure"
	}
}
func (e *Engine) Health() map[string]Health { return e.health.snapshot() }

// Descriptors returns the registered source descriptors keyed by source ID.
func (e *Engine) Descriptors() map[string]Descriptor {
	out := make(map[string]Descriptor, len(e.sources))
	for _, rt := range e.sources {
		d := rt.source.Descriptor()
		out[d.ID] = d
	}
	return out
}
func (e *Engine) Gaps() []Gap {
	e.gapsMu.Lock()
	defer e.gapsMu.Unlock()
	return append([]Gap(nil), e.gaps...)
}
func (e *Engine) Processed() uint64 { return e.processed.Load() }
func wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type sourceReporter struct {
	engine *Engine
	id     string
}

func (r sourceReporter) ConfirmHealthy() {
	r.engine.health.update(r.id, func(h *Health) { h.State = HealthHealthy; h.LastConfirmation = time.Now(); h.LastError = "" })
}
func (r sourceReporter) ReportGap(start, end time.Time, reason string) {
	r.engine.recordGap(r.id, start, end, reason)
}
