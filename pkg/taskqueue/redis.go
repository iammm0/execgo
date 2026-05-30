package taskqueue

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisQueue implements Queue using Redis Streams.
type RedisQueue struct {
	client       *redis.Client
	prefix       string
	group        string
	claimMinIdle time.Duration
	claimBatch   int64
}

// RedisConfig configures Redis queue backend.
type RedisConfig struct {
	Addr         string
	Password     string
	DB           int
	Prefix       string
	Group        string
	ClaimMinIdle time.Duration
	ClaimBatch   int64
}

// NewRedisQueue creates a Redis stream queue.
func NewRedisQueue(cfg RedisConfig) (*RedisQueue, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("redis addr is required")
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "execgo"
	}
	if cfg.Group == "" {
		cfg.Group = "execgo-workers"
	}
	if cfg.ClaimMinIdle <= 0 {
		cfg.ClaimMinIdle = 30 * time.Second
	}
	if cfg.ClaimBatch <= 0 {
		cfg.ClaimBatch = 10
	}
	cli := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cli.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	q := &RedisQueue{
		client:       cli,
		prefix:       cfg.Prefix,
		group:        cfg.Group,
		claimMinIdle: cfg.ClaimMinIdle,
		claimBatch:   cfg.ClaimBatch,
	}
	if err := q.Start(ctx); err != nil {
		return nil, err
	}
	return q, nil
}

var _ Queue = (*RedisQueue)(nil)

func (q *RedisQueue) Start(ctx context.Context) error {
	for p := 0; p <= 9; p++ {
		stream := q.readyStream(p)
		if err := q.client.XGroupCreateMkStream(ctx, stream, q.group, "$").Err(); err != nil && !isBusyGroupErr(err) {
			return err
		}
	}
	if err := q.client.XGroupCreateMkStream(ctx, q.delayedStream(), q.group, "$").Err(); err != nil && !isBusyGroupErr(err) {
		return err
	}
	return nil
}

func (q *RedisQueue) Enqueue(ctx context.Context, taskID string, priority int, attempt int) error {
	if priority < 0 {
		priority = 0
	}
	if priority > 9 {
		priority = 9
	}
	fields := map[string]any{
		"task_id":         taskID,
		"priority":        priority,
		"attempt":         attempt,
		"scheduled_at_ms": time.Now().UnixMilli(),
		"enqueued_at_ms":  time.Now().UnixMilli(),
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.readyStream(priority), Values: fields}).Err()
}

func (q *RedisQueue) EnqueueDelayed(ctx context.Context, taskID string, priority int, attempt int, runAt time.Time) error {
	if priority < 0 {
		priority = 0
	}
	if priority > 9 {
		priority = 9
	}
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	fields := map[string]any{
		"task_id":         taskID,
		"priority":        priority,
		"attempt":         attempt,
		"scheduled_at_ms": runAt.UnixMilli(),
		"enqueued_at_ms":  time.Now().UnixMilli(),
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.delayedStream(), Values: fields}).Err()
}

func (q *RedisQueue) Poll(ctx context.Context, workerID string, wait time.Duration) (*Message, error) {
	_ = q.migrateDueDelayed(ctx, 200)
	claimed, err := q.claimPending(ctx, workerID)
	if err != nil {
		return nil, err
	}
	if claimed != nil {
		return claimed, nil
	}

	res, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    q.group,
		Consumer: workerID,
		Streams:  q.readGroupStreams(),
		Count:    1,
		Block:    wait,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	for _, stream := range res {
		if len(stream.Messages) == 0 {
			continue
		}
		return redisMessageToMessage(stream.Stream, stream.Messages[0]), nil
	}
	return nil, nil
}

func (q *RedisQueue) Ack(ctx context.Context, workerID, messageID string) error {
	_ = workerID
	stream, id, err := splitMessageID(messageID)
	if err != nil {
		return err
	}
	if err := q.client.XAck(ctx, stream, q.group, id).Err(); err != nil {
		return err
	}
	return q.client.XDel(ctx, stream, id).Err()
}

func (q *RedisQueue) Nack(ctx context.Context, workerID, messageID string, requeueAt time.Time, deadLetter bool) error {
	_ = workerID
	stream, id, err := splitMessageID(messageID)
	if err != nil {
		return err
	}
	entries, err := q.client.XRangeN(ctx, stream, id, id, 1).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	fields := map[string]any{}
	if len(entries) > 0 {
		for k, v := range entries[0].Values {
			fields[k] = v
		}
	}
	if len(fields) == 0 {
		fields["task_id"] = ""
	}

	if err := q.client.XAck(ctx, stream, q.group, id).Err(); err != nil {
		return err
	}
	if err := q.client.XDel(ctx, stream, id).Err(); err != nil {
		return err
	}

	if deadLetter {
		return q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.deadStream(), Values: fields}).Err()
	}

	priority := parseInt(fields["priority"])
	if priority < 0 {
		priority = 0
	}
	if priority > 9 {
		priority = 9
	}
	if !requeueAt.IsZero() && requeueAt.After(time.Now().UTC()) {
		fields["scheduled_at_ms"] = requeueAt.UnixMilli()
		return q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.delayedStream(), Values: fields}).Err()
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.readyStream(priority), Values: fields}).Err()
}

func (q *RedisQueue) Depth(ctx context.Context) (ready int64, delayed int64, dead int64, err error) {
	for p := 0; p <= 9; p++ {
		n, e := q.client.XLen(ctx, q.readyStream(p)).Result()
		if e != nil && e != redis.Nil {
			return 0, 0, 0, e
		}
		ready += n
	}
	delayed, err = q.client.XLen(ctx, q.delayedStream()).Result()
	if err != nil && err != redis.Nil {
		return 0, 0, 0, err
	}
	dead, err = q.client.XLen(ctx, q.deadStream()).Result()
	if err != nil && err != redis.Nil {
		return 0, 0, 0, err
	}
	return ready, delayed, dead, nil
}

func (q *RedisQueue) ListDead(ctx context.Context, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	entries, err := q.client.XRangeN(ctx, q.deadStream(), "-", "+", int64(limit)).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Message, 0, len(entries))
	for _, entry := range entries {
		out = append(out, *redisMessageToMessage(q.deadStream(), entry))
	}
	return out, nil
}

func (q *RedisQueue) RequeueDead(ctx context.Context, messageID string, runAt time.Time) error {
	stream, id, err := splitMessageID(messageID)
	if err != nil {
		return err
	}
	if stream != q.deadStream() {
		return ErrMessageNotFound
	}
	entries, err := q.client.XRangeN(ctx, stream, id, id, 1).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if len(entries) == 0 {
		return ErrMessageNotFound
	}
	fields := cloneRedisFields(entries[0].Values)
	if runAt.IsZero() {
		runAt = time.Now().UTC()
	}
	fields["scheduled_at_ms"] = runAt.UnixMilli()
	fields["enqueued_at_ms"] = time.Now().UnixMilli()

	target := q.readyStream(clampPriority(parseInt(fields["priority"])))
	if runAt.After(time.Now().UTC()) {
		target = q.delayedStream()
	}
	pipe := q.client.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: target, Values: fields})
	pipe.XDel(ctx, stream, id)
	_, err = pipe.Exec(ctx)
	return err
}

func (q *RedisQueue) claimPending(ctx context.Context, workerID string) (*Message, error) {
	if q.claimMinIdle <= 0 || workerID == "" {
		return nil, nil
	}
	batch := q.claimBatch
	if batch <= 0 {
		batch = 10
	}
	for p := 9; p >= 0; p-- {
		stream := q.readyStream(p)
		msgs, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   stream,
			Group:    q.group,
			Consumer: workerID,
			MinIdle:  q.claimMinIdle,
			Start:    "0-0",
			Count:    batch,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, err
		}
		if len(msgs) > 0 {
			return redisMessageToMessage(stream, msgs[0]), nil
		}
	}
	return nil, nil
}

func (q *RedisQueue) migrateDueDelayed(ctx context.Context, batch int64) error {
	if batch <= 0 {
		batch = 100
	}
	entries, err := q.client.XRangeN(ctx, q.delayedStream(), "-", "+", batch).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return err
	}
	now := time.Now().UnixMilli()
	for _, e := range entries {
		runAt := parseInt64(e.Values["scheduled_at_ms"])
		if runAt > now {
			continue
		}
		priority := clampPriority(parseInt(e.Values["priority"]))
		if err := q.client.XAdd(ctx, &redis.XAddArgs{Stream: q.readyStream(priority), Values: e.Values}).Err(); err != nil {
			return err
		}
		if err := q.client.XDel(ctx, q.delayedStream(), e.ID).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (q *RedisQueue) readyStream(priority int) string {
	return fmt.Sprintf("%s:ready:p%d", q.prefix, priority)
}

func (q *RedisQueue) delayedStream() string { return q.prefix + ":delayed" }
func (q *RedisQueue) deadStream() string    { return q.prefix + ":dead" }

func (q *RedisQueue) readGroupStreams() []string {
	streams := make([]string, 0, 20)
	for p := 9; p >= 0; p-- {
		streams = append(streams, q.readyStream(p))
	}
	for p := 9; p >= 0; p-- {
		streams = append(streams, ">")
	}
	return streams
}

func isBusyGroupErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "BUSYGROUP")
}

func splitMessageID(msg string) (stream string, id string, err error) {
	parts := strings.SplitN(msg, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid message id: %s", msg)
	}
	return parts[0], parts[1], nil
}

func parseString(v any) string {
	switch vv := v.(type) {
	case string:
		return vv
	case []byte:
		return string(vv)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func parseInt(v any) int {
	return int(parseInt64(v))
}

func parseInt64(v any) int64 {
	switch vv := v.(type) {
	case int64:
		return vv
	case int:
		return int64(vv)
	case float64:
		return int64(vv)
	case string:
		n, _ := strconv.ParseInt(vv, 10, 64)
		return n
	case []byte:
		n, _ := strconv.ParseInt(string(vv), 10, 64)
		return n
	default:
		return 0
	}
}

func redisMessageToMessage(stream string, msg redis.XMessage) *Message {
	m := &Message{
		MessageID: fmt.Sprintf("%s|%s", stream, msg.ID),
		TaskID:    parseString(msg.Values["task_id"]),
		Priority:  parseInt(msg.Values["priority"]),
		Attempt:   parseInt(msg.Values["attempt"]),
	}
	if v := parseInt64(msg.Values["scheduled_at_ms"]); v > 0 {
		m.ScheduledAt = time.UnixMilli(v).UTC()
	}
	if v := parseInt64(msg.Values["enqueued_at_ms"]); v > 0 {
		m.EnqueuedAt = time.UnixMilli(v).UTC()
	}
	return m
}

func cloneRedisFields(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clampPriority(priority int) int {
	if priority < 0 {
		return 0
	}
	if priority > 9 {
		return 9
	}
	return priority
}
