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
	"github.com/iammm0/execgo/pkg/worker"
)

func TestEventReplay_RebuildsStateConsistently(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	es := events.NewMemoryStore()
	mgr, err := eventsourced.NewManager(es, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	executor.RegisterBuiltins()
	metrics := observability.NewMetrics()
	s := scheduler.New(mgr, metrics, logger, 1)
	s.Start(context.Background())
	defer s.Stop()

	w := worker.New(worker.Config{ID: "replay-worker", Concurrency: 1, PollWait: 30 * time.Millisecond}, mgr, s, logger)
	w.Start(context.Background())
	defer w.Stop()

	graph := &models.TaskGraph{WorkflowID: "wf-replay", Tasks: []*models.Task{{ID: "r1", Type: "noop"}}}
	s.Submit(graph)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := mgr.Get("r1")
		if ok && task.Status == models.StatusSuccess {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	replayed, err := eventsourced.NewManager(es, logger)
	if err != nil {
		t.Fatalf("new replay manager: %v", err)
	}
	task, ok := replayed.Get("r1")
	if !ok {
		t.Fatal("expected replayed task r1")
	}
	if task.Status != models.StatusSuccess {
		t.Fatalf("expected replayed status success, got %s", task.Status)
	}
	if task.Version == 0 {
		t.Fatal("expected replayed task version > 0")
	}
	if task.WorkflowID != "wf-replay" {
		t.Fatalf("expected workflow_id wf-replay, got %s", task.WorkflowID)
	}
}
