package taskqueue

import (
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisMessageToMessage_ParsesStreamFields(t *testing.T) {
	scheduled := time.Now().UTC().Add(time.Minute).UnixMilli()
	enqueued := time.Now().UTC().UnixMilli()

	msg := redis.XMessage{
		ID: "1710000000000-0",
		Values: map[string]any{
			"task_id":         []byte("redis-task"),
			"priority":        "9",
			"attempt":         int64(2),
			"scheduled_at_ms": scheduled,
			"enqueued_at_ms":  enqueued,
		},
	}

	got := redisMessageToMessage("execgo:ready:p9", msg)
	if got.MessageID != "execgo:ready:p9|1710000000000-0" {
		t.Fatalf("MessageID=%q", got.MessageID)
	}
	if got.TaskID != "redis-task" {
		t.Fatalf("TaskID=%q", got.TaskID)
	}
	if got.Priority != 9 {
		t.Fatalf("Priority=%d", got.Priority)
	}
	if got.Attempt != 2 {
		t.Fatalf("Attempt=%d", got.Attempt)
	}
	if got.ScheduledAt.UnixMilli() != scheduled {
		t.Fatalf("ScheduledAt=%d want %d", got.ScheduledAt.UnixMilli(), scheduled)
	}
	if got.EnqueuedAt.UnixMilli() != enqueued {
		t.Fatalf("EnqueuedAt=%d want %d", got.EnqueuedAt.UnixMilli(), enqueued)
	}
}

func TestClampPriority(t *testing.T) {
	if got := clampPriority(-1); got != 0 {
		t.Fatalf("clampPriority(-1)=%d want 0", got)
	}
	if got := clampPriority(12); got != 9 {
		t.Fatalf("clampPriority(12)=%d want 9", got)
	}
	if got := clampPriority(4); got != 4 {
		t.Fatalf("clampPriority(4)=%d want 4", got)
	}
}

func TestRedisQueue_ReadGroupStreamsUsesRedisLayout(t *testing.T) {
	q := &RedisQueue{prefix: "execgo-test"}

	got := q.readGroupStreams()
	if len(got) != 20 {
		t.Fatalf("len(readGroupStreams)=%d want 20", len(got))
	}
	for i := 0; i < 10; i++ {
		want := fmt.Sprintf("execgo-test:ready:p%d", 9-i)
		if got[i] != want {
			t.Fatalf("stream[%d]=%q want %q", i, got[i], want)
		}
	}
	for i := 10; i < 20; i++ {
		if got[i] != ">" {
			t.Fatalf("stream[%d]=%q want >", i, got[i])
		}
	}
}
