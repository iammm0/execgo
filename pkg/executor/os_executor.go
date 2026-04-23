// OS built-in executor aggregation / OS 内置工具聚合执行器。
// Author: iammm0; Last edited: 2026-04-23
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

// OSExecutor 聚合本地 OS 能力 / aggregates local OS capabilities.
type OSExecutor struct {
	tools map[string]func(context.Context, *models.Task) (json.RawMessage, error)
}

// NewOSExecutor 创建 OSExecutor 实例并注册内置工具 / creates an OSExecutor and registers builtin tools.
func NewOSExecutor() *OSExecutor {
	shell := &ShellExecutor{}
	file := &FileExecutor{}
	dns := &DNSExecutor{}
	tcp := &TCPExecutor{}
	sleep := &SleepExecutor{}
	noop := &NoopExecutor{}
	httpExec := &HTTPExecutor{}
	return &OSExecutor{
		tools: map[string]func(context.Context, *models.Task) (json.RawMessage, error){
			"shell": shell.Execute,
			"file":  file.Execute,
			"dns":   dns.Execute,
			"tcp":   tcp.Execute,
			"sleep": sleep.Execute,
			"noop":  noop.Execute,
			"http":  httpExec.Execute,
		},
	}
}

// IsOSTool 判断给定名称是否为内置 OS 工具 / reports whether name is a builtin OS tool.
func IsOSTool(name string) bool {
	_, ok := NewOSExecutor().tools[strings.TrimSpace(name)]
	return ok
}

// Name 返回执行器注册名 / returns the executor registry name.
func (e *OSExecutor) Name() string { return "os" }

// Category 返回执行器分类 / returns the executor category.
func (e *OSExecutor) Category() string { return "os" }

// Execute 选择内置工具并执行任务 / selects a builtin tool and executes the task.
func (e *OSExecutor) Execute(ctx context.Context, task *models.Task) (*Result, error) {
	startedAt := time.Now()
	tool := strings.TrimSpace(task.ToolName)
	if tool == "" {
		tool = strings.TrimSpace(task.Type)
	}
	fn, ok := e.tools[tool]
	if !ok {
		return nil, fmt.Errorf("unknown os tool %q", tool)
	}
	if len(task.Input) > 0 && len(task.Params) == 0 {
		task.Params = task.Input
	}
	raw, err := fn(ctx, task)
	finishedAt := time.Now()
	res := &Result{
		TaskID:     task.ID,
		Status:     models.RuntimeSuccess,
		Output:     raw,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
	}
	if err != nil {
		res.Status = models.RuntimeFailed
		return res, err
	}
	return res, nil
}

// ListTools 返回 OS executor 可发现的工具清单 / returns discoverable tools for the OS executor.
func (e *OSExecutor) ListTools(ctx context.Context) ([]Tool, error) {
	_ = ctx
	return []Tool{
		{Name: "shell", Category: "os", Description: "Run shell command/script"},
		{Name: "file", Category: "os", Description: "Read/write file"},
		{Name: "dns", Category: "os", Description: "DNS lookup"},
		{Name: "tcp", Category: "os", Description: "TCP probe"},
		{Name: "sleep", Category: "os", Description: "Delay execution"},
		{Name: "noop", Category: "os", Description: "No-op tool"},
		{Name: "http", Category: "os", Description: "HTTP request"},
	}, nil
}

// HealthCheck 检查执行器可用性 / checks executor health.
func (e *OSExecutor) HealthCheck() error { return nil }

// Shutdown 释放执行器资源 / releases executor resources.
func (e *OSExecutor) Shutdown(ctx context.Context) error { _ = ctx; return nil }
