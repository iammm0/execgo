package executor

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

func TestNoopExecutor_Execute(t *testing.T) {
	e := &NoopExecutor{}
	ctx := context.Background()
	task := &models.Task{Params: json.RawMessage(`{"message":"hi"}`)}
	raw, err := e.Execute(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true {
		t.Fatalf("expected ok true, got %v", m["ok"])
	}
	if m["message"] != "hi" {
		t.Fatalf("message: %v", m["message"])
	}
}

func TestNoopExecutor_Execute_emptyParams(t *testing.T) {
	e := &NoopExecutor{}
	raw, err := e.Execute(context.Background(), &models.Task{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["ok"] != true {
		t.Fatal("expected ok")
	}
}

func TestSleepExecutor_Execute_zero(t *testing.T) {
	e := &SleepExecutor{}
	task := &models.Task{Params: json.RawMessage(`{"duration_ms":0}`)}
	raw, err := e.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if int64(m["slept_ms"].(float64)) != 0 {
		t.Fatalf("slept_ms: %v", m["slept_ms"])
	}
}

func TestSleepExecutor_Execute_short(t *testing.T) {
	e := &SleepExecutor{}
	task := &models.Task{Params: json.RawMessage(`{"duration_ms":1}`)}
	start := time.Now()
	_, err := e.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < time.Millisecond {
		t.Fatalf("expected at least 1ms, got %v", d)
	}
}

func TestSleepExecutor_Execute_cancel(t *testing.T) {
	e := &SleepExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task := &models.Task{Params: json.RawMessage(`{"duration_ms":60000}`)}
	_, err := e.Execute(ctx, task)
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestSleepExecutor_Execute_exceedsMax(t *testing.T) {
	e := &SleepExecutor{}
	ms := SleepMaxDuration.Milliseconds() + 1
	task := &models.Task{Params: json.RawMessage(`{"duration_ms":` + strconv.FormatInt(ms, 10) + `}`)}
	_, err := e.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestShellExecutor_DirectSuccess(t *testing.T) {
	e := &ShellExecutor{}
	task := &models.Task{
		Params: json.RawMessage(`{"command":"hostname"}`),
	}
	raw, err := e.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["exit_code"].(float64) != 0 {
		t.Fatalf("exit_code: %v", m["exit_code"])
	}
	if strings.TrimSpace(m["stdout"].(string)) == "" {
		t.Fatalf("stdout should not be empty, got %q", m["stdout"])
	}
}

func TestShellExecutor_ScriptAutoRunner(t *testing.T) {
	e := &ShellExecutor{}
	task := &models.Task{
		Params: json.RawMessage(`{"runner":"auto","script":"echo hello"}`),
	}
	raw, err := e.Execute(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["exit_code"].(float64) != 0 {
		t.Fatalf("exit_code: %v", m["exit_code"])
	}
	wantRunner := "sh"
	if runtime.GOOS == "windows" {
		wantRunner = "powershell"
	}
	if m["runner"] != wantRunner {
		t.Fatalf("runner: got %v want %s", m["runner"], wantRunner)
	}
}

func TestShellExecutor_InvalidRunner(t *testing.T) {
	e := &ShellExecutor{}
	task := &models.Task{
		Params: json.RawMessage(`{"runner":"bad","script":"echo hello"}`),
	}
	_, err := e.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected invalid runner error")
	}
}

func TestShellExecutor_PolicyDifference(t *testing.T) {
	e := &ShellExecutor{}
	old := os.Getenv(ShellPolicyEnv)
	defer os.Setenv(ShellPolicyEnv, old)

	_ = os.Unsetenv(ShellPolicyEnv)
	task := &models.Task{
		Params: json.RawMessage(`{"command":"not-a-real-whitelisted-command"}`),
	}
	_, err := e.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected whitelist error")
	}

	if err := os.Setenv(ShellPolicyEnv, ShellPolicyOpen); err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(context.Background(), task)
	if err == nil {
		t.Fatal("expected command run failure under open mode")
	}
	if !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
