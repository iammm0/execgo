package module_test

import (
	"context"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/taskqueue"
)

func TestMemoryQueue_PriorityOrdering(t *testing.T) {
	q := taskqueue.NewMemoryQueue()
	ctx := context.Background()
	if err := q.Enqueue(ctx, "low", 1, 1); err != nil {
		t.Fatalf("enqueue low: %v", err)
	}
	if err := q.Enqueue(ctx, "high", 9, 1); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}

	msg, err := q.Poll(ctx, "w", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if msg == nil || msg.TaskID != "high" {
		t.Fatalf("expected high priority first, got %+v", msg)
	}
	_ = q.Ack(ctx, "w", msg.MessageID)

	msg2, err := q.Poll(ctx, "w", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if msg2 == nil || msg2.TaskID != "low" {
		t.Fatalf("expected low second, got %+v", msg2)
	}
}
