package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/httpserver"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
	"github.com/iammm0/execgo/pkg/worker"
)

// Runtime bundles store/scheduler/metrics/worker for tests.
type Runtime struct {
	Store     store.Store
	Scheduler *scheduler.Scheduler
	Worker    *worker.Worker
	Metrics   *observability.Metrics
	Logger    *slog.Logger
}

func NewRuntime(t *testing.T, maxConcurrency int) *Runtime {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("init state manager: %v", err)
	}
	metrics := observability.NewMetrics()
	s := scheduler.New(mgr, metrics, logger, maxConcurrency)
	s.Start(context.Background())

	w := worker.New(worker.Config{
		ID:                "test-worker",
		Concurrency:       maxConcurrency,
		PollWait:          50 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 200 * time.Millisecond,
		RetryBaseBackoff:  20 * time.Millisecond,
		RetryMaxBackoff:   200 * time.Millisecond,
	}, mgr, s, logger)
	w.Start(context.Background())

	t.Cleanup(func() {
		w.Stop()
		s.Stop()
	})

	return &Runtime{
		Store:     mgr,
		Scheduler: s,
		Worker:    w,
		Metrics:   metrics,
		Logger:    logger,
	}
}

func NewHTTPServer(t *testing.T, rt *Runtime) *httptest.Server {
	t.Helper()
	engine := httpserver.NewEngine(rt.Store, rt.Scheduler, rt.Metrics, rt.Logger)
	srv := httptest.NewServer(engine.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func WaitTaskInStore(t *testing.T, st store.Store, taskID string, timeout time.Duration) *models.Task {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, ok := st.Get(taskID)
		if ok && task.Status.IsTerminal() {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal status within %v", taskID, timeout)
	return nil
}
