package unit_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
)

func TestNoopExecutor_Blackbox(t *testing.T) {
	e := &executor.NoopExecutor{}
	raw, err := e.Execute(context.Background(), &models.Task{
		Params: json.RawMessage(`{"message":"hello"}`),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("expected ok=true, got %v", got["ok"])
	}
	if got["message"] != "hello" {
		t.Fatalf("expected message=hello, got %v", got["message"])
	}
}

func TestNoopExecutor_InvalidParams(t *testing.T) {
	e := &executor.NoopExecutor{}
	_, err := e.Execute(context.Background(), &models.Task{
		Params: json.RawMessage(`{"message":`),
	})
	if err == nil {
		t.Fatal("expected parse error for invalid params JSON")
	}
}

