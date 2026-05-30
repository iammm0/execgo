package taskqueue

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestRedisQueue_PendingReclaimOptional(t *testing.T) {
	addr := os.Getenv("EXECGO_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("EXECGO_TEST_REDIS_ADDR is not set")
	}

	ctx := context.Background()
	prefix := fmt.Sprintf("execgo-test-%d", time.Now().UnixNano())
	q, err := NewRedisQueue(RedisConfig{
		Addr:         addr,
		Prefix:       prefix,
		Group:        "test-workers",
		ClaimMinIdle: 10 * time.Millisecond,
		ClaimBatch:   10,
	})
	if err != nil {
		t.Fatalf("new redis queue: %v", err)
	}
	defer q.client.Close()
	defer func() {
		keys := []string{q.delayedStream(), q.deadStream()}
		for p := 0; p <= 9; p++ {
			keys = append(keys, q.readyStream(p))
		}
		_ = q.client.Del(context.Background(), keys...).Err()
	}()

	if err := q.Enqueue(ctx, "redis-reclaim", 7, 1); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := q.Poll(ctx, "worker-a", time.Second)
	if err != nil {
		t.Fatalf("poll first: %v", err)
	}
	if first == nil || first.TaskID != "redis-reclaim" {
		t.Fatalf("unexpected first poll: %+v", first)
	}

	time.Sleep(20 * time.Millisecond)
	reclaimed, err := q.Poll(ctx, "worker-b", time.Second)
	if err != nil {
		t.Fatalf("poll reclaimed: %v", err)
	}
	if reclaimed == nil || reclaimed.TaskID != "redis-reclaim" {
		t.Fatalf("unexpected reclaimed message: %+v", reclaimed)
	}
	if reclaimed.MessageID != first.MessageID {
		t.Fatalf("reclaimed message id=%q want %q", reclaimed.MessageID, first.MessageID)
	}
	if err := q.Ack(ctx, "worker-b", reclaimed.MessageID); err != nil {
		t.Fatalf("ack reclaimed: %v", err)
	}
}
