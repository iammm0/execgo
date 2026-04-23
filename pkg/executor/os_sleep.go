// Sleep tool executor / 延时工具执行器。
// Author: iammm0; Last edited: 2026-04-23
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

// SleepMaxDuration 单次 sleep 上限，防止长时间占用并发槽 / max sleep to avoid hogging worker slots.
const SleepMaxDuration = 10 * time.Minute

// SleepParams sleep 执行器参数 / sleep executor parameters.
type SleepParams struct {
	DurationMS int64 `json:"duration_ms"`
}

// SleepExecutor 按毫秒延时，可被 context 取消 / delays for a duration; cancellable via context.
type SleepExecutor struct{}

// Type 返回工具类型名 / returns the tool type name.
func (e *SleepExecutor) Type() string { return "sleep" }

// Execute 执行 sleep 延时任务 / executes a sleep (delay) task.
func (e *SleepExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p SleepParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse sleep params: %w", err)
	}
	if p.DurationMS < 0 {
		return nil, fmt.Errorf("duration_ms must be non-negative")
	}
	d := time.Duration(p.DurationMS) * time.Millisecond
	if d > SleepMaxDuration {
		return nil, fmt.Errorf("duration_ms exceeds max of %d ms", SleepMaxDuration.Milliseconds())
	}
	if d == 0 {
		return json.Marshal(map[string]any{"slept_ms": int64(0)})
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.C:
	}

	return json.Marshal(map[string]any{"slept_ms": p.DurationMS})
}
