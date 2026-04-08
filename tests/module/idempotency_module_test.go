package module_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
)

func TestEventStore_IdempotencyKeyHit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	graph := &models.TaskGraph{
		WorkflowID:     "wf-idem",
		IdempotencyKey: "idem-key-1",
		Tasks:          []*models.Task{{ID: "idem-task", Type: "noop"}},
	}
	first, err := mgr.SubmitGraph(context.Background(), graph, store.SubmitOptions{})
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := mgr.SubmitGraph(context.Background(), graph, store.SubmitOptions{})
	if err != nil {
		t.Fatalf("second submit: %v", err)
	}
	if second.WorkflowID != first.WorkflowID {
		t.Fatalf("expected same workflow id, got %s vs %s", second.WorkflowID, first.WorkflowID)
	}
	if !second.IdempotentHit {
		t.Fatal("expected idempotent hit")
	}
	all := mgr.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 task in read model, got %d", len(all))
	}
}
