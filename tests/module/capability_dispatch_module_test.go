package module_test

import (
	"context"
	"io"
	"log/slog"
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

type capabilityHarness struct {
	ctx     context.Context
	cancel  context.CancelFunc
	store   *eventsourced.Manager
	queue   *taskqueue.MemoryQueue
	sched   *scheduler.Scheduler
	metrics *observability.Metrics
	logger  *slog.Logger
}

func newCapabilityHarness(t *testing.T) *capabilityHarness {
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

	return &capabilityHarness{
		ctx:     ctx,
		cancel:  cancel,
		store:   st,
		queue:   q,
		sched:   s,
		metrics: metrics,
		logger:  logger,
	}
}

func (h *capabilityHarness) startWorker(t *testing.T, id string, caps map[string]string) *worker.Worker {
	t.Helper()
	w := worker.New(worker.Config{
		ID:                id,
		Capabilities:      caps,
		Concurrency:       1,
		PollWait:          20 * time.Millisecond,
		LeaseDuration:     time.Second,
		HeartbeatInterval: time.Hour,
		RetryBaseBackoff:  20 * time.Millisecond,
		RetryMaxBackoff:   100 * time.Millisecond,
	}, h.store, h.sched, h.logger)
	w.Start(h.ctx)
	t.Cleanup(w.Stop)
	return w
}

func TestCapabilityDispatch_MismatchedWorkerDoesNotExecute(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCapabilityHarness(t)
	h.startWorker(t, "worker-os-only", map[string]string{"executor": "os", "sandbox": "local"})

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "needs-noop", Type: "noop"}}})
	waitCapabilityMismatches(t, h.metrics, 1, time.Second)

	task := mustCapabilityTask(t, h.store, "needs-noop")
	if task.Status != models.StatusReady {
		t.Fatalf("status=%s want ready", task.Status)
	}
	_, _, dead, err := h.queue.Depth(context.Background())
	if err != nil {
		t.Fatalf("queue depth: %v", err)
	}
	if dead != 0 {
		t.Fatalf("dead depth=%d want 0", dead)
	}
}

func TestCapabilityDispatch_TaskRunsOnlyOnMatchingWorker(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCapabilityHarness(t)
	h.startWorker(t, "worker-os-only", map[string]string{"executor": "os", "sandbox": "local"})
	h.startWorker(t, "worker-noop", map[string]string{"executor": "noop", "sandbox": "local"})

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "match-noop", Type: "noop"}}})
	waitCapabilityTaskStatus(t, h.store, "match-noop", models.StatusSuccess, 2*time.Second)

	if workerID := firstTaskStartedWorker(t, h.store, "match-noop"); workerID != "worker-noop" {
		t.Fatalf("task_started worker=%q want worker-noop", workerID)
	}
}

func TestCapabilityDispatch_DependentTaskStillRequiresCapabilities(t *testing.T) {
	executor.RegisterBuiltins()
	h := newCapabilityHarness(t)
	h.startWorker(t, "worker-local", map[string]string{"executor": "noop", "sandbox": "local"})

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{
		{ID: "cap-parent", Type: "noop"},
		{
			ID:                   "cap-child",
			Type:                 "noop",
			DependsOn:            []string{"cap-parent"},
			RequiredCapabilities: map[string]string{"sandbox": "docker"},
		},
	}})

	waitCapabilityTaskStatus(t, h.store, "cap-parent", models.StatusSuccess, 2*time.Second)
	waitCapabilityMismatches(t, h.metrics, 1, time.Second)
	child := mustCapabilityTask(t, h.store, "cap-child")
	if child.Status != models.StatusReady {
		t.Fatalf("child status=%s want ready", child.Status)
	}
}

func waitCapabilityMismatches(t *testing.T, metrics *observability.Metrics, wantAtLeast int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if metrics.DispatchCapabilityMismatches.Load() >= wantAtLeast {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("DispatchCapabilityMismatches=%d want >= %d", metrics.DispatchCapabilityMismatches.Load(), wantAtLeast)
}

func waitCapabilityTaskStatus(t *testing.T, st *eventsourced.Manager, taskID string, status models.TaskStatus, timeout time.Duration) *models.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task := mustCapabilityTask(t, st, taskID)
		if task.Status == status {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task := mustCapabilityTask(t, st, taskID)
	t.Fatalf("task %s status=%s want %s within %v", taskID, task.Status, status, timeout)
	return nil
}

func mustCapabilityTask(t *testing.T, st *eventsourced.Manager, taskID string) *models.Task {
	t.Helper()
	task, ok := st.Get(taskID)
	if !ok {
		t.Fatalf("missing task %s", taskID)
	}
	return task
}

func firstTaskStartedWorker(t *testing.T, st *eventsourced.Manager, taskID string) string {
	t.Helper()
	events, err := st.EventStore().LoadGlobal(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	for _, ev := range events {
		if ev.AggregateID == taskID && ev.Type == models.RuntimeEventStarted {
			return ev.Metadata.WorkerID
		}
	}
	t.Fatalf("task_started event not found for %s", taskID)
	return ""
}
