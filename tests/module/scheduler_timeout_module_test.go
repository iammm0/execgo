package module_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/tests/testutil"
)

type timeoutExecutor struct {
	taskType string
}

func (e *timeoutExecutor) Name() string     { return e.taskType }
func (e *timeoutExecutor) Category() string { return "test" }
func (e *timeoutExecutor) ListTools(ctx context.Context) ([]executor.Tool, error) {
	return nil, nil
}
func (e *timeoutExecutor) HealthCheck() error                 { return nil }
func (e *timeoutExecutor) Shutdown(ctx context.Context) error { return nil }

func (e *timeoutExecutor) Execute(ctx context.Context, task *models.Task) (*executor.Result, error) {
	_ = task
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestScheduler_TimeoutProducesStructuredRuntimeError(t *testing.T) {
	rt := testutil.NewRuntime(t, 1)
	taskType := fmt.Sprintf("timeout-%d", time.Now().UnixNano())
	executor.Register(&timeoutExecutor{taskType: taskType})

	rt.Scheduler.Submit(&models.TaskGraph{
		Tasks: []*models.Task{
			{
				ID:      "timeout-task",
				Type:    taskType,
				Timeout: 50, // ms
			},
		},
	})

	task := testutil.WaitTaskInStore(t, rt.Store, "timeout-task", 4*time.Second)
	if task.Status != models.StatusFailed {
		t.Fatalf("expected status=%s, got %s", models.StatusFailed, task.Status)
	}
	if task.Runtime == nil {
		t.Fatal("expected runtime envelope on timeout failure")
	}
	if task.Runtime.Status != models.RuntimeFailed {
		t.Fatalf("expected runtime status=%s, got %s", models.RuntimeFailed, task.Runtime.Status)
	}
	if task.Runtime.Error == nil {
		t.Fatal("expected runtime error on timeout")
	}
	if task.Runtime.Error.Code != models.ErrorTimeout {
		t.Fatalf("expected error code=%s, got %s", models.ErrorTimeout, task.Runtime.Error.Code)
	}
	if !task.Runtime.Error.Retryable {
		t.Fatal("expected timeout runtime error to be retryable")
	}
	if task.Runtime.Attempt != 1 {
		t.Fatalf("expected attempt=1, got %d", task.Runtime.Attempt)
	}
	if task.RunStatus != string(models.RuntimeFailed) {
		t.Fatalf("expected run_status=%q, got %q", models.RuntimeFailed, task.RunStatus)
	}
}
