package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iammm0/execgo/pkg/models"
)

type CLISkillsExecutor struct {
	ext ExecutorExtension
}

func NewCLISkillsExecutor(ext ExecutorExtension) *CLISkillsExecutor {
	if ext == nil {
		ext = NopExtension{}
	}
	return &CLISkillsExecutor{ext: ext}
}

func (e *CLISkillsExecutor) Name() string     { return "cli-skills" }
func (e *CLISkillsExecutor) Category() string { return "cli-skills" }

type CLISkillInput struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

func (e *CLISkillsExecutor) Execute(ctx context.Context, task *models.Task) (*Result, error) {
	if err := e.ext.BeforeExecute(ctx, task); err != nil {
		return nil, err
	}

	var in CLISkillInput
	raw := task.Input
	if len(raw) == 0 {
		raw = task.Params
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse cli-skills input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("command is required")
	}

	cmd := exec.CommandContext(ctx, in.Command, in.Args...)
	out, err := cmd.CombinedOutput()
	res := &Result{
		TaskID: task.ID,
		Status: "success",
		Output: mustJSONMarshal(map[string]any{
			"command": in.Command,
			"args":    in.Args,
			"output":  string(out),
		}),
	}
	if err != nil {
		res.Status = "failed"
		_ = e.ext.OnError(ctx, err, task)
		return res, fmt.Errorf("cli skill command failed: %w", err)
	}
	_ = e.ext.AfterExecute(ctx, res)
	return res, nil
}

func (e *CLISkillsExecutor) ListTools(ctx context.Context) ([]Tool, error) {
	_ = ctx
	return []Tool{
		{
			Name:        "cli.run",
			Category:    "cli-skills",
			Description: "Run local CLI command",
			InputSchema: map[string]any{"type": "object"},
		},
	}, nil
}

func (e *CLISkillsExecutor) HealthCheck() error            { return nil }
func (e *CLISkillsExecutor) Shutdown(ctx context.Context) error { _ = ctx; return nil }

func mustJSONMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

