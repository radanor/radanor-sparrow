package monitoring

import (
	"context"
	"errors"
	"time"
)

var ErrCheckpointNotFound = errors.New("checkpoint not found")

// CheckpointStore persists per-source replay positions. CE-4 provides a durable
// SQLite implementation in internal/storage; NoCheckpointStore remains the
// default for sources or tests without persistence.
type CheckpointStore interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte) error
	Invalidate(context.Context, string, []byte) error
}
type NoCheckpointStore struct{}

func (NoCheckpointStore) Load(context.Context, string) ([]byte, error) {
	return nil, ErrCheckpointNotFound
}
func (NoCheckpointStore) Save(context.Context, string, []byte) error       { return nil }
func (NoCheckpointStore) Invalidate(context.Context, string, []byte) error { return nil }

// CheckpointBlocker is implemented by stores that persist an event and its
// checkpoint in one atomic transaction (CE-4 storage). When configured with
// Config.DurableCheckpoints, the engine stops writing checkpoints itself and
// calls BlockCheckpoints whenever a source's ordering is broken, so a durable
// checkpoint can never advance past an earlier event that was not durably
// handled.
type CheckpointBlocker interface {
	CheckpointStore
	// BlockCheckpoints permanently stops checkpoint advancement for a source.
	BlockCheckpoints(source string)
}

type Gap struct {
	Source string
	Start  time.Time
	End    time.Time
	Reason string
}
