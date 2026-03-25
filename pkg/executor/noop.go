package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iammm0/execgo/pkg/models"
)

// NoopParams 无操作执行器参数（均可选）/ noop executor parameters (all optional).
type NoopParams struct {
	Message string `json:"message,omitempty"`
}

// NoopExecutor 占位与测试，无外部 IO / placeholder for DAGs and tests; no external I/O.
type NoopExecutor struct{}

func (e *NoopExecutor) Type() string { return "noop" }

func (e *NoopExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var p NoopParams
	if len(task.Params) > 0 {
		if err := json.Unmarshal(task.Params, &p); err != nil {
			return nil, fmt.Errorf("parse noop params: %w", err)
		}
	}

	return json.Marshal(map[string]any{
		"ok":      true,
		"message": p.Message,
	})
}
