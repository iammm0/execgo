package taskqueue

import (
	"context"
	"time"
)

// Message is one queue item lease handed to worker.
type Message struct {
	MessageID   string
	TaskID      string
	Priority    int
	Attempt     int
	ScheduledAt time.Time
	EnqueuedAt  time.Time
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
}
