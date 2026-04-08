package events

import (
	"context"
	"errors"

	"github.com/iammm0/execgo/pkg/models"
)

var (
	ErrVersionConflict = errors.New("event store: optimistic concurrency conflict")
)

// EventStore is append-only runtime event storage.
type EventStore interface {
	Append(ctx context.Context, stream string, expectedVersion int64, events ...models.RuntimeEvent) ([]models.RuntimeEvent, error)
	LoadStream(ctx context.Context, stream string, fromVersion int64, limit int) ([]models.RuntimeEvent, error)
	LoadGlobal(ctx context.Context, afterOffset int64, limit int) ([]models.RuntimeEvent, error)
	Replay(ctx context.Context, aggregateID string) ([]models.RuntimeEvent, error)
}
