package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/sandbox"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/taskqueue"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Config controls worker runtime behavior.
type Config struct {
	ID                string
	Concurrency       int
	PollWait          time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	RetryBaseBackoff  time.Duration
	RetryMaxBackoff   time.Duration
	Runner            sandbox.Runner
}

// Worker consumes queued tasks and executes them.
type Worker struct {
	cfg       Config
	state     store.Store
	evented   store.EventBackedStore
	scheduler *scheduler.Scheduler
	logger    *slog.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new worker instance.
func New(cfg Config, st store.Store, sched *scheduler.Scheduler, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ID == "" {
		cfg.ID = "worker-local"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = 2 * time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 30 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.RetryBaseBackoff <= 0 {
		cfg.RetryBaseBackoff = 100 * time.Millisecond
	}
	if cfg.RetryMaxBackoff <= 0 {
		cfg.RetryMaxBackoff = 30 * time.Second
	}
	if cfg.Runner == nil {
		cfg.Runner = sandbox.LocalRunner{}
	}

	w := &Worker{
		cfg:       cfg,
		state:     st,
		scheduler: sched,
		logger:    logger,
	}
	if es, ok := st.(store.EventBackedStore); ok {
		w.evented = es
	}
	return w
}

// Start starts worker polling and heartbeat loops.
func (w *Worker) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	if w.evented != nil {
		_ = w.evented.RegisterWorker(runCtx, w.cfg.ID, map[string]string{"sandbox": w.cfg.Runner.Name()}, models.RuntimeEventMetadata{WorkerID: w.cfg.ID, Producer: "worker"})
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.heartbeatLoop(runCtx)
		}()
	}

	for i := 0; i < w.cfg.Concurrency; i++ {
		w.wg.Add(1)
		go func(slot int) {
			defer w.wg.Done()
			w.runLoop(runCtx, slot)
		}(i)
	}
	w.logger.Info("worker started", "worker_id", w.cfg.ID, "concurrency", w.cfg.Concurrency, "runner", w.cfg.Runner.Name())
}

// Stop gracefully stops worker.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if w.evented != nil {
		_ = w.evented.MarkWorkerOffline(context.Background(), w.cfg.ID, models.RuntimeEventMetadata{WorkerID: w.cfg.ID, Producer: "worker"})
	}
	w.logger.Info("worker stopped", "worker_id", w.cfg.ID)
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.evented != nil {
				_ = w.evented.Heartbeat(ctx, w.cfg.ID, models.RuntimeEventMetadata{WorkerID: w.cfg.ID, Producer: "worker"})
			}
		}
	}
}

func (w *Worker) runLoop(ctx context.Context, slot int) {
	logger := w.logger.With("worker_id", w.cfg.ID, "slot", slot)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := w.scheduler.Queue().Poll(ctx, w.cfg.ID, w.cfg.PollWait)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("queue poll failed", "error", err)
			}
			continue
		}
		if msg == nil {
			continue
		}

		if err := w.handleMessage(ctx, msg); err != nil {
			logger.Warn("handle message failed", "task_id", msg.TaskID, "error", err)
		}
	}
}

func (w *Worker) handleMessage(ctx context.Context, msg *taskqueue.Message) error {
	task, ok := w.state.Get(msg.TaskID)
	if !ok {
		return w.scheduler.Queue().Ack(ctx, w.cfg.ID, msg.MessageID)
	}
	attempt := msg.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	ctx, span := observability.StartSpan(ctx, "worker.handle_message",
		trace.WithAttributes(
			attribute.String("execgo.worker_id", w.cfg.ID),
			attribute.String("execgo.task_id", msg.TaskID),
			attribute.Int("execgo.attempt", attempt),
		),
	)
	defer span.End()
	span.SetAttributes(attribute.String("execgo.task_type", task.Type))

	w.scheduler.OnTaskLeased(task.ID, w.cfg.ID, time.Now().UTC().Add(w.cfg.LeaseDuration), attempt)
	w.scheduler.OnTaskStarted(task.ID, w.cfg.ID, attempt)

	executor.NormalizeTask(task)
	execImpl, err := executor.Get(task.Type)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		w.scheduler.OnTaskFailed(task.ID, w.cfg.ID, nil, attempt, err.Error(), "")
		return w.scheduler.Queue().Ack(ctx, w.cfg.ID, msg.MessageID)
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if task.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(task.Timeout)*time.Millisecond)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	res, runErr, audit := w.cfg.Runner.Run(runCtx, execImpl, task)
	if res != nil && len(res.Progress) > 0 {
		prog, _ := json.Marshal(res.Progress)
		w.scheduler.OnTaskProgress(task.ID, w.cfg.ID, prog, attempt)
	}
	if runErr == nil && res != nil && !res.Status.IsTerminal() {
		res, runErr = w.awaitRuntimeResult(runCtx, execImpl, res)
	}

	if audit != nil {
		audit.WorkerID = w.cfg.ID
		if w.evented != nil {
			_ = w.evented.AppendAudit(ctx, task.ID, audit, models.RuntimeEventMetadata{TaskID: task.ID, WorkerID: w.cfg.ID, Attempt: attempt, Producer: "worker"})
		}
	}

	output := resultOutput(res)
	handleID := ""
	if res != nil {
		handleID = res.HandleID
	}
	if runErr == nil {
		span.SetStatus(codes.Ok, "success")
		w.scheduler.OnTaskSucceeded(task.ID, w.cfg.ID, output, attempt, handleID)
		return w.scheduler.Queue().Ack(ctx, w.cfg.ID, msg.MessageID)
	}

	maxAttempts := task.Retry + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if attempt < maxAttempts && shouldRetry(runErr, res) {
		span.RecordError(runErr)
		span.SetStatus(codes.Error, "retry_scheduled")
		nextAttempt := attempt + 1
		runAt := time.Now().UTC().Add(computeBackoff(w.cfg.RetryBaseBackoff, w.cfg.RetryMaxBackoff, nextAttempt))
		w.scheduler.OnTaskRetry(task.ID, w.cfg.ID, nextAttempt, runAt, runErr.Error())
		return w.scheduler.Queue().Ack(ctx, w.cfg.ID, msg.MessageID)
	}

	span.RecordError(runErr)
	span.SetStatus(codes.Error, runErr.Error())
	w.scheduler.OnTaskFailed(task.ID, w.cfg.ID, output, attempt, runErr.Error(), handleID)
	return w.scheduler.Queue().Ack(ctx, w.cfg.ID, msg.MessageID)
}

func resultOutput(r *executor.Result) json.RawMessage {
	if r == nil {
		return nil
	}
	return r.Output
}

func shouldRetry(err error, res *executor.Result) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if res != nil && res.Error != nil {
		return res.Error.Retryable
	}
	return true
}

func computeBackoff(base, max time.Duration, attempt int) time.Duration {
	if attempt <= 1 {
		return base
	}
	factor := math.Pow(2, float64(attempt-2))
	backoff := time.Duration(float64(base) * factor)
	if backoff > max {
		backoff = max
	}
	jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
	return backoff/2 + jitter
}

func (w *Worker) awaitRuntimeResult(ctx context.Context, execImpl executor.Executor, initial *executor.Result) (*executor.Result, error) {
	if initial == nil || initial.HandleID == "" {
		return initial, nil
	}
	reader, ok := execImpl.(executor.HandleReader)
	if !ok {
		return initial, nil
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	current := initial
	for {
		if current != nil && current.Status.IsTerminal() {
			return current, runtimeResultError(current)
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-ticker.C:
			next, found := reader.GetHandle(initial.HandleID)
			if !found {
				return current, errors.New("handle not found")
			}
			current = next
		}
	}
}

func runtimeResultError(res *executor.Result) error {
	if res == nil {
		return nil
	}
	switch res.Status {
	case models.RuntimeSuccess:
		return nil
	case models.RuntimeCancelled:
		if res.Error != nil && res.Error.Message != "" {
			return errors.New(res.Error.Message)
		}
		return errors.New("task cancelled")
	case models.RuntimeFailed:
		if res.Error != nil && res.Error.Message != "" {
			return errors.New(res.Error.Message)
		}
		return errors.New("task failed")
	default:
		return nil
	}
}
