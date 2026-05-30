package taskqueue

import (
	"context"
	"errors"
	"time"
)

// ErrMessageNotFound is returned when a queue message id no longer exists.
var ErrMessageNotFound = errors.New("queue message not found")

// Message is one queue item lease handed to worker.
type Message struct {
	MessageID   string    `json:"message_id"`
	TaskID      string    `json:"task_id"`
	Priority    int       `json:"priority"`
	Attempt     int       `json:"attempt"`
	ScheduledAt time.Time `json:"scheduled_at"`
	EnqueuedAt  time.Time `json:"enqueued_at"`
}

// Queue represents ready/delayed/retry/dead-letter task queue semantics.
type Queue interface {
	Start(ctx context.Context) error
	Enqueue(ctx context.Context, taskID string, priority int, attempt int) error
	EnqueueDelayed(ctx context.Context, taskID string, priority int, attempt int, runAt time.Time) error
	Poll(ctx context.Context, workerID string, wait time.Duration) (*Message, error)
	Ack(ctx context.Context, workerID, messageID string) error
	Nack(ctx context.Context, workerID, messageID string, requeueAt time.Time, deadLetter bool) error
	Depth(ctx context.Context) (ready int64, delayed int64, dead int64, err error)
	ListDead(ctx context.Context, limit int) ([]Message, error)
	RequeueDead(ctx context.Context, messageID string, runAt time.Time) error
}
