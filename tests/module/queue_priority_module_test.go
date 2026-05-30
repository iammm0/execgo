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

func TestMemoryQueue_DeadLetterListAndRequeue(t *testing.T) {
	q := taskqueue.NewMemoryQueue()
	ctx := context.Background()
	if err := q.Enqueue(ctx, "dead-task", 5, 1); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	msg, err := q.Poll(ctx, "w", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if err := q.Nack(ctx, "w", msg.MessageID, time.Time{}, true); err != nil {
		t.Fatalf("dead-letter nack: %v", err)
	}

	ready, delayed, dead, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if ready != 0 || delayed != 0 || dead != 1 {
		t.Fatalf("depth ready=%d delayed=%d dead=%d, want 0/0/1", ready, delayed, dead)
	}

	deadMessages, err := q.ListDead(ctx, 10)
	if err != nil {
		t.Fatalf("list dead: %v", err)
	}
	if len(deadMessages) != 1 || deadMessages[0].TaskID != "dead-task" {
		t.Fatalf("unexpected dead messages: %+v", deadMessages)
	}

	if err := q.RequeueDead(ctx, deadMessages[0].MessageID, time.Time{}); err != nil {
		t.Fatalf("requeue dead: %v", err)
	}
	ready, delayed, dead, err = q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth after requeue: %v", err)
	}
	if ready != 1 || delayed != 0 || dead != 0 {
		t.Fatalf("depth after requeue ready=%d delayed=%d dead=%d, want 1/0/0", ready, delayed, dead)
	}

	requeued, err := q.Poll(ctx, "w2", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("poll requeued: %v", err)
	}
	if requeued == nil || requeued.TaskID != "dead-task" {
		t.Fatalf("expected requeued dead-task, got %+v", requeued)
	}
}
