package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	execpkg "github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
)

// Runner executes task with isolation and returns execution audit.
type Runner interface {
	Name() string
	Run(ctx context.Context, execImpl execpkg.Executor, task *models.Task) (*execpkg.Result, error, *models.ExecutionAudit)
}

// LocalRunner executes directly without external sandboxing.
type LocalRunner struct{}

func (LocalRunner) Name() string { return "local" }

func (LocalRunner) Run(ctx context.Context, execImpl execpkg.Executor, task *models.Task) (*execpkg.Result, error, *models.ExecutionAudit) {
	start := time.Now().UTC()
	res, err := execImpl.Execute(ctx, task)
	finish := time.Now().UTC()
	audit := &models.ExecutionAudit{
		TaskID:     task.ID,
		Executor:   execImpl.Name(),
		Sandbox:    "local",
		StartedAt:  start,
		FinishedAt: finish,
		DurationMS: finish.Sub(start).Milliseconds(),
		Success:    err == nil,
	}
	if err != nil {
		audit.Error = err.Error()
	}
	if res != nil {
		audit.StdoutSHA256 = hashBytes(res.Output)
		audit.StderrSHA256 = hashBytes(res.Details)
	}
	return res, err, audit
}

// DockerConfig controls docker sandbox resources.
type DockerConfig struct {
	Image     string
	CPUs      string
	Memory    string
	PidsLimit int
	NoNetwork bool
	WorkDir   string
}

// DockerRunner executes shell tasks inside Docker and falls back to local for unsupported task kinds.
type DockerRunner struct {
	cfg   DockerConfig
	local LocalRunner
}

// NewDockerRunner creates a docker sandbox runner.
func NewDockerRunner(cfg DockerConfig) *DockerRunner {
	if strings.TrimSpace(cfg.Image) == "" {
		cfg.Image = "alpine:3.21"
	}
	return &DockerRunner{cfg: cfg}
}

func (d *DockerRunner) Name() string { return "docker" }

func (d *DockerRunner) Run(ctx context.Context, execImpl execpkg.Executor, task *models.Task) (*execpkg.Result, error, *models.ExecutionAudit) {
	if execImpl.Name() != "os" || strings.TrimSpace(task.ToolName) != "shell" {
		return d.local.Run(ctx, execImpl, task)
	}

	start := time.Now().UTC()
	cmdStr, err := shellCommandFromTask(task)
	if err != nil {
		res, localErr, audit := d.local.Run(ctx, execImpl, task)
		if audit != nil {
			audit.Sandbox = "docker-fallback-local"
		}
		if localErr == nil {
			localErr = err
		}
		return res, localErr, audit
	}

	args := []string{"run", "--rm"}
	if d.cfg.CPUs != "" {
		args = append(args, "--cpus", d.cfg.CPUs)
	}
	if d.cfg.Memory != "" {
		args = append(args, "--memory", d.cfg.Memory)
	}
	if d.cfg.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", d.cfg.PidsLimit))
	}
	if d.cfg.NoNetwork {
		args = append(args, "--network", "none")
	}
	if d.cfg.WorkDir != "" {
		args = append(args, "-w", d.cfg.WorkDir)
	}
	args = append(args, d.cfg.Image, "sh", "-lc", cmdStr)

	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	out, runErr := dockerCmd.CombinedOutput()
	finish := time.Now().UTC()

	status := models.RuntimeSuccess
	if runErr != nil {
		status = models.RuntimeFailed
	}
	result := &execpkg.Result{
		TaskID:     task.ID,
		Status:     status,
		StartedAt:  &start,
		FinishedAt: &finish,
		DurationMS: finish.Sub(start).Milliseconds(),
		Output: mustMarshal(map[string]any{
			"sandbox":    "docker",
			"image":      d.cfg.Image,
			"command":    cmdStr,
			"output":     string(out),
			"exit_error": fmt.Sprint(runErr),
		}),
	}
	audit := &models.ExecutionAudit{
		TaskID:       task.ID,
		Executor:     execImpl.Name(),
		Sandbox:      "docker",
		Image:        d.cfg.Image,
		Command:      cmdStr,
		StartedAt:    start,
		FinishedAt:   finish,
		DurationMS:   finish.Sub(start).Milliseconds(),
		StdoutSHA256: hashBytes(out),
		Success:      runErr == nil,
	}
	if runErr != nil {
		audit.Error = runErr.Error()
	}
	return result, runErr, audit
}

func shellCommandFromTask(task *models.Task) (string, error) {
	var in struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Script  string   `json:"script"`
	}
	raw := task.Params
	if len(raw) == 0 {
		raw = task.Input
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("shell task params are empty")
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Script) != "" {
		return in.Script, nil
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("shell command is required")
	}
	if len(in.Args) == 0 {
		return in.Command, nil
	}
	return in.Command + " " + strings.Join(in.Args, " "), nil
}

func hashBytes(v any) string {
	var b []byte
	switch vv := v.(type) {
	case nil:
		return ""
	case []byte:
		b = vv
	case json.RawMessage:
		b = vv
	case string:
		b = []byte(vv)
	default:
		enc, _ := json.Marshal(v)
		b = enc
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
