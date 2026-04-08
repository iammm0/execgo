// Package store defines task state storage abstractions.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/models"
)

// Store is the legacy minimal interface used by scheduler and HTTP layer.
type Store interface {
	Put(task *models.Task)
	Get(id string) (*models.Task, bool)
	GetAll() []*models.Task
	Delete(id string) bool
	UpdateStatus(id string, status models.TaskStatus, result json.RawMessage, errMsg string) bool
}

// Flusher optionally persists snapshot state.
type Flusher interface {
	Persist() error
}

// Tx defines a transaction scope for state mutations.
type Tx interface {
	Append(stream string, expectedVersion int64, events ...models.RuntimeEvent) ([]models.RuntimeEvent, error)
	Commit() error
	Rollback() error
}

// TransactionalStore supports transactional event/state updates.
type TransactionalStore interface {
	BeginTx(ctx context.Context) (Tx, error)
}

// SubmitOptions controls workflow submission behavior.
type SubmitOptions struct {
	WorkflowID     string
	IdempotencyKey string
	Metadata       models.RuntimeEventMetadata
}

// SubmitResult is returned from SubmitGraph.
type SubmitResult struct {
	WorkflowID    string
	TaskIDs       []string
	IdempotentHit bool
}

// TransitionOptions provides metadata for task state transition.
type TransitionOptions struct {
	EventType  models.RuntimeEventType
	Result     json.RawMessage
	Error      string
	HandleID   string
	Progress   json.RawMessage
	LeaseOwner string
	LeaseUntil time.Time
	Attempt    int
	Metadata   models.RuntimeEventMetadata
	Payload    map[string]any
}

// EventBackedStore exposes event-sourcing capabilities on top of Store.
type EventBackedStore interface {
	Store
	SubmitGraph(ctx context.Context, graph *models.TaskGraph, options SubmitOptions) (*SubmitResult, error)
	TransitionTask(ctx context.Context, taskID string, to models.TaskStatus, opts TransitionOptions) (*models.Task, error)
	AppendAudit(ctx context.Context, taskID string, audit any, metadata models.RuntimeEventMetadata) error
	Replay(ctx context.Context) error
	EventStore() events.EventStore
	TaskDependents(taskID string) []string
	WorkflowTasks(workflowID string) []string
	RegisterWorker(ctx context.Context, workerID string, capabilities map[string]string, metadata models.RuntimeEventMetadata) error
	Heartbeat(ctx context.Context, workerID string, metadata models.RuntimeEventMetadata) error
	MarkWorkerOffline(ctx context.Context, workerID string, metadata models.RuntimeEventMetadata) error
	ListWorkers() []*models.WorkerNode
}
