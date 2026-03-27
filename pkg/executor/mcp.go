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

	handleID := task.HandleID
	if handleID == "" {
		handleID = fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	initial := &Result{
		TaskID:   task.ID,
		Status:   "running",
		HandleID: handleID,
		Progress: []ProgressEvent{{Timestamp: time.Now(), Message: "task accepted"}},
	}

	e.mu.Lock()
	e.handles[handleID] = initial
	e.mu.Unlock()

	go func() {
		out, err := e.ext.ExecuteMethod(ctx, task)
		e.mu.Lock()
		defer e.mu.Unlock()
		cur := e.handles[handleID]
		if cur == nil {
			return
		}
		if err != nil {
			cur.Status = "failed"
			cur.Progress = append(cur.Progress, ProgressEvent{Timestamp: time.Now(), Message: err.Error()})
			_ = e.ext.OnError(ctx, err, task)
			return
		}
		if out != nil {
			cur.Output = out.Output
		}
		cur.Status = "success"
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
		Status: "success",
		Output: mustJSONMarshal(map[string]any{
			"tool_name": task.ToolName,
			"echo":      json.RawMessage(payload),
		}),
	}, nil
}

