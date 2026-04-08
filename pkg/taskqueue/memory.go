package taskqueue

import (
	"container/heap"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryQueue is an in-process queue implementation for local runtime/testing.
type MemoryQueue struct {
	mu sync.Mutex

	seq atomic.Int64

	ready    readyHeap
	delayed  delayedHeap
	inFlight map[string]*queueItem
	deadCnt  int64
}

// NewMemoryQueue creates a new in-memory queue.
func NewMemoryQueue() *MemoryQueue {
	q := &MemoryQueue{inFlight: make(map[string]*queueItem)}
	heap.Init(&q.ready)
	heap.Init(&q.delayed)
	return q
}

var _ Queue = (*MemoryQueue)(nil)

func (q *MemoryQueue) Start(ctx context.Context) error {
	_ = ctx
	return nil
}

func (q *MemoryQueue) Enqueue(ctx context.Context, taskID string, priority int, attempt int) error {
	return q.enqueueAt(ctx, taskID, priority, attempt, time.Now().UTC(), false)
}

func (q *MemoryQueue) EnqueueDelayed(ctx context.Context, taskID string, priority int, attempt int, runAt time.Time) error {
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	return q.enqueueAt(ctx, taskID, priority, attempt, runAt.UTC(), true)
}

func (q *MemoryQueue) enqueueAt(ctx context.Context, taskID string, priority int, attempt int, runAt time.Time, delayed bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if priority < 0 {
		priority = 0
	}
	if priority > 9 {
		priority = 9
	}
	item := &queueItem{
		messageID:   fmt.Sprintf("mem-%d", time.Now().UnixNano()),
		taskID:      taskID,
		priority:    priority,
		attempt:     attempt,
		scheduledAt: runAt,
		enqueuedAt:  time.Now().UTC(),
		seq:         q.seq.Add(1),
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if delayed && runAt.After(time.Now().UTC()) {
		heap.Push(&q.delayed, item)
	} else {
		heap.Push(&q.ready, item)
	}
	return nil
}

func (q *MemoryQueue) Poll(ctx context.Context, workerID string, wait time.Duration) (*Message, error) {
	_ = workerID
	if wait < 0 {
		wait = 0
	}
	deadline := time.Now().Add(wait)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := time.Now().UTC()

		q.mu.Lock()
		q.moveDueLocked(now)
		if q.ready.Len() > 0 {
			item := heap.Pop(&q.ready).(*queueItem)
			q.inFlight[item.messageID] = item
			q.mu.Unlock()
			return item.toMessage(), nil
		}
		q.mu.Unlock()

		if wait == 0 || time.Now().After(deadline) {
			return nil, nil
		}
		sleep := 25 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep <= 0 {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
	}
}

func (q *MemoryQueue) Ack(ctx context.Context, workerID, messageID string) error {
	_ = ctx
	_ = workerID
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inFlight, messageID)
	return nil
}

func (q *MemoryQueue) Nack(ctx context.Context, workerID, messageID string, requeueAt time.Time, deadLetter bool) error {
	_ = ctx
	_ = workerID
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.inFlight[messageID]
	if !ok {
		return nil
	}
	delete(q.inFlight, messageID)

	if deadLetter {
		q.deadCnt++
		return nil
	}

	now := time.Now().UTC()
	if !requeueAt.IsZero() && requeueAt.After(now) {
		item.scheduledAt = requeueAt
		item.seq = q.seq.Add(1)
		heap.Push(&q.delayed, item)
		return nil
	}
	item.seq = q.seq.Add(1)
	heap.Push(&q.ready, item)
	return nil
}

func (q *MemoryQueue) Depth(ctx context.Context) (ready int64, delayed int64, dead int64, err error) {
	_ = ctx
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(q.ready.Len()), int64(q.delayed.Len()), q.deadCnt, nil
}

func (q *MemoryQueue) moveDueLocked(now time.Time) {
	for q.delayed.Len() > 0 {
		next := q.delayed[0]
		if next.scheduledAt.After(now) {
			break
		}
		item := heap.Pop(&q.delayed).(*queueItem)
		heap.Push(&q.ready, item)
	}
}

type queueItem struct {
	messageID   string
	taskID      string
	priority    int
	attempt     int
	scheduledAt time.Time
	enqueuedAt  time.Time
	seq         int64
}

func (i *queueItem) toMessage() *Message {
	return &Message{
		MessageID:   i.messageID,
		TaskID:      i.taskID,
		Priority:    i.priority,
		Attempt:     i.attempt,
		ScheduledAt: i.scheduledAt,
		EnqueuedAt:  i.enqueuedAt,
	}
}

type readyHeap []*queueItem

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	if !h[i].scheduledAt.Equal(h[j].scheduledAt) {
		return h[i].scheduledAt.Before(h[j].scheduledAt)
	}
	return h[i].seq < h[j].seq
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(*queueItem)) }
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type delayedHeap []*queueItem

func (h delayedHeap) Len() int { return len(h) }
func (h delayedHeap) Less(i, j int) bool {
	if !h[i].scheduledAt.Equal(h[j].scheduledAt) {
		return h[i].scheduledAt.Before(h[j].scheduledAt)
	}
	return h[i].seq < h[j].seq
}
func (h delayedHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *delayedHeap) Push(x any)   { *h = append(*h, x.(*queueItem)) }
func (h *delayedHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
