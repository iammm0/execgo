package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

type MCPExecutor struct {
	ext     ExecutorExtension
	mu      sync.RWMutex
	handles map[string]*Result
}

func NewMCPExecutor(ext ExecutorExtension) *MCPExecutor {
	if ext == nil {
		ext = mcpDefaultExtension{}
	}
	return &MCPExecutor{
		ext:     ext,
		handles: make(map[string]*Result),
	}
}

func (e *MCPExecutor) Name() string     { return "mcp" }
func (e *MCPExecutor) Category() string { return "mcp" }

func (e *MCPExecutor) Execute(ctx context.Context, task *models.Task) (*Result, error) {
	if err := e.ext.BeforeExecute(ctx, task); err != nil {
		return nil, err
	}

	startedAt := time.Now()
	handleID := task.HandleID
	if handleID == "" {
		handleID = fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	initial := &Result{
		TaskID:    task.ID,
		Status:    models.RuntimeAccepted,
		HandleID:  handleID,
		StartedAt: &startedAt,
		Progress:  []ProgressEvent{{Timestamp: startedAt, Message: "task accepted"}},
	}

	e.mu.Lock()
	e.handles[handleID] = initial
	e.mu.Unlock()

	go func() {
		e.mu.Lock()
		cur := e.handles[handleID]
		if cur != nil {
			cur.Status = models.RuntimeRunning
			cur.Progress = append(cur.Progress, ProgressEvent{Timestamp: time.Now(), Message: "task started"})
		}
		e.mu.Unlock()

		out, err := e.ext.ExecuteMethod(ctx, task)
		e.mu.Lock()
		defer e.mu.Unlock()
		cur = e.handles[handleID]
		if cur == nil {
			return
		}
		finishedAt := time.Now()
		cur.FinishedAt = &finishedAt
		if cur.StartedAt != nil {
			cur.DurationMS = finishedAt.Sub(*cur.StartedAt).Milliseconds()
		}
		if err != nil {
			cur.Status = models.RuntimeFailed
			cur.Error = &models.RuntimeError{
				Code:    models.ErrorExternalFailure,
				Message: err.Error(),
				Source:  "executor",
			}
			cur.Progress = append(cur.Progress, ProgressEvent{Timestamp: time.Now(), Message: err.Error()})
			_ = e.ext.OnError(ctx, err, task)
			return
		}
		if out != nil {
			cur.Output = out.Output
			cur.Details = out.Details
			if out.Error != nil {
				cur.Error = out.Error
			}
		}
		cur.Status = models.RuntimeSuccess
		cur.Progress = append(cur.Progress, ProgressEvent{Timestamp: time.Now(), Message: "task completed"})
		_ = e.ext.AfterExecute(ctx, cur)
	}()

	return initial, nil
}

func (e *MCPExecutor) ListTools(ctx context.Context) ([]Tool, error) {
	_ = ctx
	return []Tool{
		{
			Name:        "mcp.execute",
			Category:    "mcp",
			Description: "Execute MCP task and return handle",
			InputSchema: map[string]any{"type": "object"},
		},
	}, nil
}

func (e *MCPExecutor) HealthCheck() error { return nil }
func (e *MCPExecutor) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func (e *MCPExecutor) GetHandle(handleID string) (*Result, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	r, ok := e.handles[handleID]
	if !ok {
		return nil, false
	}
	cp := *r
	if len(r.Output) > 0 {
		cp.Output = append(json.RawMessage{}, r.Output...)
	}
	if len(r.Details) > 0 {
		cp.Details = append(json.RawMessage{}, r.Details...)
	}
	if r.StartedAt != nil {
		startedAt := *r.StartedAt
		cp.StartedAt = &startedAt
	}
	if r.FinishedAt != nil {
		finishedAt := *r.FinishedAt
		cp.FinishedAt = &finishedAt
	}
	if r.Error != nil {
		errCopy := *r.Error
		cp.Error = &errCopy
	}
	return &cp, true
}

type mcpDefaultExtension struct{ NopExtension }

func (mcpDefaultExtension) ExecuteMethod(ctx context.Context, task *models.Task) (*Result, error) {
	_ = ctx
	payload := task.Input
	if len(payload) == 0 {
		payload = task.Params
	}
	return &Result{
		TaskID: task.ID,
		Status: models.RuntimeSuccess,
		Output: mustJSONMarshal(map[string]any{
			"tool_name": task.ToolName,
			"echo":      json.RawMessage(payload),
		}),
	}, nil
}
