// Package executor 定义执行器接口和全局注册表
// defines the Executor interface and global registry.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/iammm0/execgo/internal/models"
)

// Executor 是所有执行器必须实现的接口 / interface that all executors must implement.
type Executor interface {
	// Type 返回执行器类型标识 / returns the executor type identifier.
	Type() string
	// Execute 执行任务并返回结果 / executes a task and returns the result.
	Execute(ctx context.Context, task *models.Task) (json.RawMessage, error)
}

// ----------------------------------------------------------------
// Registry 全局执行器注册表 / global executor registry
// ----------------------------------------------------------------

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Executor)
)

// Register 注册一个执行器 / registers an executor.
func Register(e Executor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[e.Type()] = e
}

// Get 按类型获取执行器 / retrieves an executor by type.
func Get(taskType string) (Executor, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	e, ok := registry[taskType]
	if !ok {
		return nil, fmt.Errorf("no executor registered for type %q", taskType)
	}
	return e, nil
}

// RegisteredTypes 返回所有已注册的类型 / returns all registered types.
func RegisteredTypes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	types := make([]string, 0, len(registry))
	for k := range registry {
		types = append(types, k)
	}
	return types
}

// RegisterBuiltins 注册所有内置执行器 / registers all built-in executors.
func RegisterBuiltins() {
	Register(&HTTPExecutor{})
	Register(&ShellExecutor{})
	Register(&FileExecutor{})
}
