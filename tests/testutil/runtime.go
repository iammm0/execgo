package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/httpserver"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/store/jsonfile"
)

// Runtime bundles store/scheduler/metrics for tests.
type Runtime struct {
	Store     store.Store
	Scheduler *scheduler.Scheduler
	Metrics   *observability.Metrics
	Logger    *slog.Logger
}

func NewRuntime(t *testing.T, maxConcurrency int) *Runtime {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := jsonfile.NewManager(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("init state manager: %v", err)
	}
	metrics := observability.NewMetrics()
	s := scheduler.New(mgr, metrics, logger, maxConcurrency)
	s.Start(context.Background())
	t.Cleanup(s.Stop)

	return &Runtime{
		Store:     mgr,
		Scheduler: s,
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
		if ok && (task.Status == models.StatusSuccess || task.Status == models.StatusFailed || task.Status == models.StatusSkipped) {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal status within %v", taskID, timeout)
	return nil
}
