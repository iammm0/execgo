package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iammm0/execgo/pkg/models"
)

// OSExecutor 聚合本地 OS 能力。
type OSExecutor struct {
	tools map[string]func(context.Context, *models.Task) (json.RawMessage, error)
}

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

func IsOSTool(name string) bool {
	_, ok := NewOSExecutor().tools[strings.TrimSpace(name)]
	return ok
}

func (e *OSExecutor) Name() string     { return "os" }
func (e *OSExecutor) Category() string { return "os" }

func (e *OSExecutor) Execute(ctx context.Context, task *models.Task) (*Result, error) {
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
	res := &Result{
		TaskID: task.ID,
		Status: "success",
		Output: raw,
	}
	if err != nil {
		res.Status = "failed"
		return res, err
	}
	return res, nil
}

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

func (e *OSExecutor) HealthCheck() error            { return nil }
func (e *OSExecutor) Shutdown(ctx context.Context) error { _ = ctx; return nil }

