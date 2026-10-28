package monitoring

import (
	"sync"
	"time"
)

type HealthState string

const (
	HealthStarting         HealthState = "starting"
	HealthHealthy          HealthState = "healthy"
	HealthDegraded         HealthState = "degraded"
	HealthPermissionDenied HealthState = "permission_denied"
	HealthUnsupported      HealthState = "unsupported"
	HealthStopped          HealthState = "stopped"
	HealthFailed           HealthState = "failed"
	HealthRecovering       HealthState = "recovering"
)

type Health struct {
	Source              string
	State               HealthState
	LastSuccessfulEvent time.Time
	LastConfirmation    time.Time
	LastFailure         time.Time
	QueueOverflows      uint64
	DroppedEvents       uint64
	Duplicates          uint64
	RestartCount        uint64
	MonitoringGaps      uint64
	LastError           string
}
type healthBook struct {
	mu sync.RWMutex
	m  map[string]Health
}

func newHealthBook() *healthBook { return &healthBook{m: make(map[string]Health)} }
func (b *healthBook) update(id string, fn func(*Health)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.m[id]
	h.Source = id
	fn(&h)
	b.m[id] = h
}
func (b *healthBook) snapshot() map[string]Health {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]Health, len(b.m))
	for id, h := range b.m {
		out[id] = h
	}
	return out
}
