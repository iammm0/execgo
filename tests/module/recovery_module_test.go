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
	"github.com/iammm0/execgo/tests/testutil"
)

type recoveryHarness struct {
	ctx    context.Context
	cancel context.CancelFunc
	store  *eventsourced.Manager
	queue  *taskqueue.MemoryQueue
	sched  *scheduler.Scheduler
	logger *slog.Logger
}

func newRecoveryHarness(t *testing.T, cfg scheduler.RecoveryConfig) *recoveryHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	q := taskqueue.NewMemoryQueue()
	metrics := observability.NewMetrics()
	s := scheduler.NewWithQueueAndRecovery(st, metrics, logger, q, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() {
		s.Stop()
		cancel()
	})

	return &recoveryHarness{
		ctx:    ctx,
		cancel: cancel,
		store:  st,
		queue:  q,
		sched:  s,
		logger: logger,
	}
}

func TestRecovery_LeasedTaskExpiresAndRequeuesToSuccess(t *testing.T) {
	executor.RegisterBuiltins()
	h := newRecoveryHarness(t, scheduler.RecoveryConfig{
		Enabled:            false,
		LeaseSweepInterval: time.Hour,
		WorkerStaleAfter:   time.Hour,
		MaxRecoverBatch:    10,
	})

	h.submitAndExpireLease(t, "lease-requeue", 1, false)
	report, err := h.sched.RecoverExpiredLeases(h.ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("recover expired leases: %v", err)
	}
	if report.Requeued != 1 || report.TimedOut != 0 {
		t.Fatalf("report=%+v want requeued=1 timed_out=0", report)
	}

	task, ok := h.store.Get("lease-requeue")
	if !ok {
		t.Fatal("missing task after recovery")
	}
	if task.Status != models.StatusReady {
		t.Fatalf("status=%s want ready", task.Status)
	}
	if task.Attempt != 2 {
		t.Fatalf("attempt=%d want 2", task.Attempt)
	}

	w := worker.New(worker.Config{
		ID:                "recovery-worker",
		Concurrency:       1,
		PollWait:          20 * time.Millisecond,
		LeaseDuration:     time.Second,
		HeartbeatInterval: time.Hour,
	}, h.store, h.sched, h.logger)
	w.Start(h.ctx)
	defer w.Stop()

	done := testutil.WaitTaskInStore(t, h.store, "lease-requeue", 3*time.Second)
	if done.Status != models.StatusSuccess {
		t.Fatalf("status=%s want success error=%s", done.Status, done.Error)
	}
	if done.Attempt != 2 {
		t.Fatalf("final attempt=%d want 2", done.Attempt)
	}
}

func TestRecovery_RunningTaskExpiresAndRequeuesToSuccess(t *testing.T) {
	executor.RegisterBuiltins()
	h := newRecoveryHarness(t, scheduler.RecoveryConfig{
		Enabled:            false,
		LeaseSweepInterval: time.Hour,
		WorkerStaleAfter:   time.Hour,
		MaxRecoverBatch:    10,
	})

	h.submitAndExpireLease(t, "running-requeue", 1, true)
	report, err := h.sched.RecoverExpiredLeases(h.ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("recover expired leases: %v", err)
	}
	if report.Requeued != 1 || report.TimedOut != 0 {
		t.Fatalf("report=%+v want requeued=1 timed_out=0", report)
	}

	w := worker.New(worker.Config{
		ID:                "running-recovery-worker",
		Concurrency:       1,
		PollWait:          20 * time.Millisecond,
		LeaseDuration:     time.Second,
		HeartbeatInterval: time.Hour,
	}, h.store, h.sched, h.logger)
	w.Start(h.ctx)
	defer w.Stop()

	done := testutil.WaitTaskInStore(t, h.store, "running-requeue", 3*time.Second)
	if done.Status != models.StatusSuccess {
		t.Fatalf("status=%s want success error=%s", done.Status, done.Error)
	}
	if done.Attempt != 2 {
		t.Fatalf("final attempt=%d want 2", done.Attempt)
	}
}

func TestRecovery_RunningTaskExpiresAndTimesOutWhenRetriesExhausted(t *testing.T) {
	executor.RegisterBuiltins()
	h := newRecoveryHarness(t, scheduler.RecoveryConfig{
		Enabled:            false,
		LeaseSweepInterval: time.Hour,
		WorkerStaleAfter:   time.Hour,
		MaxRecoverBatch:    10,
	})

	h.submitAndExpireLease(t, "running-timeout", 0, true)
	report, err := h.sched.RecoverExpiredLeases(h.ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("recover expired leases: %v", err)
	}
	if report.Requeued != 0 || report.TimedOut != 1 {
		t.Fatalf("report=%+v want requeued=0 timed_out=1", report)
	}

	task, ok := h.store.Get("running-timeout")
	if !ok {
		t.Fatal("missing task after timeout")
	}
	if task.Status != models.StatusTimedOut {
		t.Fatalf("status=%s want timed_out", task.Status)
	}
	if task.Runtime == nil || task.Runtime.Error == nil {
		t.Fatalf("expected runtime timeout error, got %+v", task.Runtime)
	}
	if task.Runtime.Error.Code != models.ErrorTimeout {
		t.Fatalf("runtime error code=%s want %s", task.Runtime.Error.Code, models.ErrorTimeout)
	}
}

func TestRecovery_WorkerStaleThenHeartbeatOnline(t *testing.T) {
	h := newRecoveryHarness(t, scheduler.RecoveryConfig{
		Enabled:            false,
		LeaseSweepInterval: time.Hour,
		WorkerStaleAfter:   time.Second,
		MaxRecoverBatch:    10,
	})

	if err := h.store.RegisterWorker(h.ctx, "stale-worker", map[string]string{"sandbox": "local"}, models.RuntimeEventMetadata{WorkerID: "stale-worker"}); err != nil {
		t.Fatalf("register worker: %v", err)
	}
	report, err := h.sched.RecoverExpiredLeases(h.ctx, time.Now().UTC().Add(2*time.Second))
	if err != nil {
		t.Fatalf("recover expired leases: %v", err)
	}
	if report.WorkersStale != 1 {
		t.Fatalf("workers stale=%d want 1", report.WorkersStale)
	}
	workers := h.store.ListWorkers()
	if len(workers) != 1 || workers[0].Status != "stale" {
		t.Fatalf("workers after stale=%+v", workers)
	}

	if err := h.store.Heartbeat(h.ctx, "stale-worker", models.RuntimeEventMetadata{WorkerID: "stale-worker"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	workers = h.store.ListWorkers()
	if len(workers) != 1 || workers[0].Status != "online" {
		t.Fatalf("workers after heartbeat=%+v", workers)
	}
}

func (h *recoveryHarness) submitAndExpireLease(t *testing.T, taskID string, retry int, running bool) {
	t.Helper()
	h.sched.Submit(&models.TaskGraph{
		Tasks: []*models.Task{{ID: taskID, Type: "noop", Retry: retry}},
	})
	msg, err := h.queue.Poll(h.ctx, "lost-worker", time.Second)
	if err != nil {
		t.Fatalf("poll queued task: %v", err)
	}
	if msg == nil {
		t.Fatal("expected queued task")
	}
	h.sched.OnTaskLeased(taskID, "lost-worker", time.Now().UTC().Add(-time.Second), 1)
	if running {
		h.sched.OnTaskStarted(taskID, "lost-worker", 1)
	}
}
