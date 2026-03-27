package executor

import (
	"context"

	"github.com/iammm0/execgo/pkg/models"
)

// ExecutorExtension 允许自定义执行生命周期逻辑。
type ExecutorExtension interface {
	ExecuteMethod(ctx context.Context, task *models.Task) (*Result, error)
	BeforeExecute(ctx context.Context, task *models.Task) error
	AfterExecute(ctx context.Context, result *Result) error
	OnError(ctx context.Context, err error, task *models.Task) error
	Metadata() map[string]any
}

// NopExtension 默认扩展实现。
type NopExtension struct{}

func (NopExtension) ExecuteMethod(ctx context.Context, task *models.Task) (*Result, error) {
	_ = ctx
	_ = task
	return nil, nil
}
func (NopExtension) BeforeExecute(ctx context.Context, task *models.Task) error {
	_ = ctx
	_ = task
	return nil
}
func (NopExtension) AfterExecute(ctx context.Context, result *Result) error {
	_ = ctx
	_ = result
	return nil
}
func (NopExtension) OnError(ctx context.Context, err error, task *models.Task) error {
	_ = ctx
	_ = err
	_ = task
	return nil
}
func (NopExtension) Metadata() map[string]any { return map[string]any{} }

