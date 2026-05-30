package module_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
	"github.com/iammm0/execgo/pkg/taskqueue"
	"github.com/iammm0/execgo/pkg/worker"
)

type cancellationHarness struct {
	ctx     context.Context
	cancel  context.CancelFunc
	store   *eventsourced.Manager
	queue   *taskqueue.MemoryQueue
	sched   *scheduler.Scheduler
	metrics *observability.Metrics
	logger  *slog.Logger
}

func newCancellationHarness(t *testing.T) *cancellationHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	q := taskqueue.NewMemoryQueue()
	metrics := observability.NewMetrics()
	s := scheduler.NewWithQueueAndRecovery(st, metrics, logger, q, scheduler.RecoveryConfig{
		Enabled:            false,
		LeaseSweepInterval: time.Hour,
		WorkerStaleAfter:   time.Hour,
		MaxRecoverBatch:    10,
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() {
		s.Stop()
		cancel()
	})

	return &cancellationHarness{
		ctx:     ctx,
		cancel:  cancel,
		store:   st,
		queue:   q,
		sched:   s,
		metrics: metrics,
		logger:  logger,
	}
}

func (h *cancellationHarness) startWorker(t *testing.T) *worker.Worker {
	t.Helper()
	w := worker.New(worker.Config{
		ID:                "cancel-worker",
		Concurrency:       1,
		PollWait:          20 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: time.Hour,
		RetryBaseBackoff:  20 * time.Millisecond,
		RetryMaxBackoff:   100 * time.Millisecond,
	}, h.store, h.sched, h.logger)
	w.Start(h.ctx)
	t.Cleanup(w.Stop)
	return w
}

func TestCancellation_ReadyTaskCancelledBeforeExecution(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCancellationHarness(t)

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "ready-cancel", Type: "noop"}}})
	result, err := h.sched.CancelTask(h.ctx, "ready-cancel", "", "test")
	if err != nil {
		t.Fatalf("cancel ready task: %v", err)
	}
	if !result.Cancelled || result.PreviousStatus != models.StatusReady || result.Status != models.StatusCancelled {
		t.Fatalf("unexpected cancel result: %+v", result)
	}

	h.startWorker(t)
	waitQueueDepth(t, h.queue, 0, 0, 0, time.Second)
	task := mustTask(t, h.store, "ready-cancel")
	if task.Status != models.StatusCancelled {
		t.Fatalf("status=%s want cancelled", task.Status)
	}
	if h.metrics.TasksCancelled.Load() != 1 {
		t.Fatalf("TasksCancelled=%d want 1", h.metrics.TasksCancelled.Load())
	}
}

func TestCancellation_RunningSleepStopsAndStaysCancelled(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCancellationHarness(t)
	h.startWorker(t)

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{sleepTask("running-cancel", 5000)}})
	waitTaskStatus(t, h.store, "running-cancel", models.StatusRunning, 2*time.Second)

	result, err := h.sched.CancelTask(h.ctx, "running-cancel", "test cancel", "test")
	if err != nil {
		t.Fatalf("cancel running task: %v", err)
	}
	if !result.Cancelled || result.PreviousStatus != models.StatusRunning {
		t.Fatalf("unexpected cancel result: %+v", result)
	}

	waitTaskStatus(t, h.store, "running-cancel", models.StatusCancelled, 2*time.Second)
	time.Sleep(80 * time.Millisecond)
	task := mustTask(t, h.store, "running-cancel")
	if task.Status != models.StatusCancelled {
		t.Fatalf("late status=%s want cancelled", task.Status)
	}
	if task.Runtime == nil || task.Runtime.Status != models.RuntimeCancelled {
		t.Fatalf("runtime=%+v want cancelled", task.Runtime)
	}
	if task.Runtime.Error == nil || task.Runtime.Error.Code != models.ErrorCancelled {
		t.Fatalf("runtime error=%+v want cancelled code", task.Runtime.Error)
	}
	if got := h.metrics.TasksRunning.Load(); got != 0 {
		t.Fatalf("TasksRunning=%d want 0", got)
	}
	if got := h.metrics.TasksCancelled.Load(); got != 1 {
		t.Fatalf("TasksCancelled=%d want 1", got)
	}
}

func TestCancellation_DelayedTaskCancelledBeforeDue(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCancellationHarness(t)

	runAt := time.Now().UTC().Add(80 * time.Millisecond)
	task := sleepTask("delayed-cancel", 1)
	task.ScheduledAt = &runAt
	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{task}})
	if _, err := h.sched.CancelTask(h.ctx, "delayed-cancel", "", "test"); err != nil {
		t.Fatalf("cancel delayed task: %v", err)
	}

	h.startWorker(t)
	time.Sleep(180 * time.Millisecond)
	got := mustTask(t, h.store, "delayed-cancel")
	if got.Status != models.StatusCancelled {
		t.Fatalf("status=%s want cancelled", got.Status)
	}
	waitQueueDepth(t, h.queue, 0, 0, 0, time.Second)
}

func TestCancellation_CascadesSkipToDependents(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCancellationHarness(t)

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{
		{ID: "cancel-parent", Type: "noop"},
		{ID: "cancel-child", Type: "noop", DependsOn: []string{"cancel-parent"}},
	}})
	if _, err := h.sched.CancelTask(h.ctx, "cancel-parent", "", "test"); err != nil {
		t.Fatalf("cancel parent: %v", err)
	}

	parent := mustTask(t, h.store, "cancel-parent")
	child := mustTask(t, h.store, "cancel-child")
	if parent.Status != models.StatusCancelled {
		t.Fatalf("parent status=%s want cancelled", parent.Status)
	}
	if child.Status != models.StatusSkipped {
		t.Fatalf("child status=%s want skipped", child.Status)
	}
	if child.Error != "upstream task cancelled: cancel-parent" {
		t.Fatalf("child error=%q", child.Error)
	}
}

func TestCancellation_LateWorkerReportsAreNoops(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCancellationHarness(t)

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "late-cancel", Type: "noop"}}})
	if _, err := h.sched.CancelTask(h.ctx, "late-cancel", "", "test"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}

	h.sched.OnTaskLeased("late-cancel", "late-worker", time.Now().UTC().Add(time.Second), 1)
	h.sched.OnTaskStarted("late-cancel", "late-worker", 1)
	h.sched.OnTaskProgress("late-cancel", "late-worker", json.RawMessage(`{"step":"late"}`), 1)
	h.sched.OnTaskSucceeded("late-cancel", "late-worker", json.RawMessage(`{"ok":true}`), 1, "")
	h.sched.OnTaskFailed("late-cancel", "late-worker", nil, 1, "late failure", "")
	h.sched.OnTaskRetry("late-cancel", "late-worker", 2, time.Now().UTC(), "late retry")

	task := mustTask(t, h.store, "late-cancel")
	if task.Status != models.StatusCancelled {
		t.Fatalf("status=%s want cancelled", task.Status)
	}
	if h.metrics.TasksSucceeded.Load() != 0 || h.metrics.TasksFailed.Load() != 0 || h.metrics.TasksRunning.Load() != 0 {
		t.Fatalf("unexpected metrics succeeded=%d failed=%d running=%d", h.metrics.TasksSucceeded.Load(), h.metrics.TasksFailed.Load(), h.metrics.TasksRunning.Load())
	}
}

func sleepTask(id string, durationMS int64) *models.Task {
	return &models.Task{
		ID:       id,
		Type:     "os",
		ToolName: "sleep",
		Params:   json.RawMessage(`{"duration_ms":` + strconv.FormatInt(durationMS, 10) + `}`),
	}
}

func mustTask(t *testing.T, st *eventsourced.Manager, taskID string) *models.Task {
	t.Helper()
	task, ok := st.Get(taskID)
	if !ok {
		t.Fatalf("missing task %s", taskID)
	}
	return task
}

func waitTaskStatus(t *testing.T, st *eventsourced.Manager, taskID string, status models.TaskStatus, timeout time.Duration) *models.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := mustTask(t, st, taskID)
		if task.Status == status {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := mustTask(t, st, taskID)
	t.Fatalf("task %s status=%s want %s within %v", taskID, task.Status, status, timeout)
	return nil
}

func waitQueueDepth(t *testing.T, q taskqueue.Queue, wantReady, wantDelayed, wantDead int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready, delayed, dead, err := q.Depth(context.Background())
		if err != nil {
			t.Fatalf("queue depth: %v", err)
		}
		if ready == wantReady && delayed == wantDelayed && dead == wantDead {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	ready, delayed, dead, _ := q.Depth(context.Background())
	t.Fatalf("queue depth ready=%d delayed=%d dead=%d want %d/%d/%d", ready, delayed, dead, wantReady, wantDelayed, wantDead)
}
