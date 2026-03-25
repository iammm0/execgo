package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iammm0/execgo/internal/models"
)

// 安全白名单: 仅允许以下命令执行 / security whitelist: only these commands are allowed.
var allowedCommands = map[string]bool{
	"echo": true, "cat": true, "ls": true, "date": true,
	"whoami": true, "hostname": true, "uname": true, "pwd": true,
	"curl": true, "wget": true, "ping": true, "dig": true,
	"grep": true, "awk": true, "sed": true, "head": true, "tail": true,
	"wc": true, "sort": true, "uniq": true, "find": true,
	"dir": true, "where": true, "type": true, // Windows 常用命令
}

// ShellParams Shell 执行器参数 / Shell executor parameters.
type ShellParams struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Dir     string   `json:"dir,omitempty"`
}

// ShellExecutor 执行白名单内的 Shell 命令 / executes whitelisted shell commands.
type ShellExecutor struct{}

func (e *ShellExecutor) Type() string { return "shell" }

func (e *ShellExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p ShellParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse shell params: %w", err)
	}

	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}

	// 提取基础命令名用于白名单校验 / extract base command name for whitelist check
	base := p.Command
	if idx := strings.LastIndexAny(base, "/\\"); idx >= 0 {
		base = base[idx+1:]
	}

	if !allowedCommands[base] {
		return nil, fmt.Errorf("command %q is not in the allowed whitelist", base)
	}

	cmd := exec.CommandContext(ctx, p.Command, p.Args...)
	if p.Dir != "" {
		cmd.Dir = p.Dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": cmd.ProcessState.ExitCode(),
	}

	data, _ := json.Marshal(result)

	if err != nil {
		return data, fmt.Errorf("command failed: %w", err)
	}
	return data, nil
}
