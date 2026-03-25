package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/iammm0/execgo/pkg/models"
)

const (
	// ShellPolicyEnv 控制 shell 执行策略 / controls shell executor policy.
	ShellPolicyEnv = "EXECGO_SHELL_POLICY"
	// ShellPolicyOpen 跳过命令白名单 / bypasses command whitelist.
	ShellPolicyOpen = "open"
	// ShellRunnerAuto automatically picks runner by OS.
	ShellRunnerAuto = "auto"
	// ShellRunnerDirect executes command/args directly.
	ShellRunnerDirect = "direct"
	// ShellRunnerPowerShell executes script through PowerShell.
	ShellRunnerPowerShell = "powershell"
	// ShellRunnerCMD executes script through cmd.exe.
	ShellRunnerCMD = "cmd"
	// ShellRunnerSh executes script through /bin/sh.
	ShellRunnerSh = "sh"
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
	Runner  string   `json:"runner,omitempty"` // auto | direct | powershell | cmd | sh
	Script  string   `json:"script,omitempty"`
}

// ShellExecutor 执行白名单内的 Shell 命令 / executes whitelisted shell commands.
type ShellExecutor struct{}

func (e *ShellExecutor) Type() string { return "shell" }

func isOpenShellPolicy() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(ShellPolicyEnv)), ShellPolicyOpen)
}

func resolveScriptRunner(runner string) (string, []string, string, error) {
	r := strings.ToLower(strings.TrimSpace(runner))
	if r == "" || r == ShellRunnerAuto {
		if runtime.GOOS == "windows" {
			return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command"}, ShellRunnerPowerShell, nil
		}
		return "/bin/sh", []string{"-c"}, ShellRunnerSh, nil
	}
	switch r {
	case ShellRunnerPowerShell:
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command"}, ShellRunnerPowerShell, nil
	case ShellRunnerCMD:
		return "cmd", []string{"/C"}, ShellRunnerCMD, nil
	case ShellRunnerSh:
		return "/bin/sh", []string{"-c"}, ShellRunnerSh, nil
	default:
		return "", nil, "", fmt.Errorf("invalid runner %q (supported for script: auto, powershell, cmd, sh)", runner)
	}
}

func (e *ShellExecutor) Execute(ctx context.Context, task *models.Task) (json.RawMessage, error) {
	var p ShellParams
	if err := json.Unmarshal(task.Params, &p); err != nil {
		return nil, fmt.Errorf("parse shell params: %w", err)
	}

	if strings.TrimSpace(p.Script) == "" && strings.TrimSpace(p.Command) == "" {
		return nil, fmt.Errorf("either script or command is required")
	}

	openMode := isOpenShellPolicy()
	executedRunner := ShellRunnerDirect

	var cmd *exec.Cmd
	script := strings.TrimSpace(p.Script)
	if script != "" {
		if strings.EqualFold(strings.TrimSpace(p.Runner), ShellRunnerDirect) {
			return nil, fmt.Errorf("runner %q cannot be used with script", p.Runner)
		}
		bin, prefixArgs, normalizedRunner, err := resolveScriptRunner(p.Runner)
		if err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, bin, append(prefixArgs, p.Script)...)
		executedRunner = normalizedRunner
	} else {
		if strings.TrimSpace(p.Command) == "" {
			return nil, fmt.Errorf("command is required")
		}
		if !openMode {
			// 提取基础命令名用于白名单校验 / extract base command name for whitelist check
			base := p.Command
			if idx := strings.LastIndexAny(base, "/\\"); idx >= 0 {
				base = base[idx+1:]
			}

			if !allowedCommands[base] {
				return nil, fmt.Errorf("command %q is not in the allowed whitelist", base)
			}
		}
		cmd = exec.CommandContext(ctx, p.Command, p.Args...)
	}

	if p.Dir != "" {
		cmd.Dir = p.Dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	result := map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
		"runner":    executedRunner,
	}

	data, _ := json.Marshal(result)

	if err != nil {
		return data, fmt.Errorf("command failed: %w", err)
	}
	return data, nil
}
