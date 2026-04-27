package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateShellAliasToOSTask(t *testing.T) {
	kernel := NewAdapterKernel()
	resp, err := kernel.Translate(AgentActionRequest{
		Adapter:  "codex",
		AgentID:  "agent-1",
		ActionID: "list-files",
		Action: AgentAction{
			Kind:  "shell",
			Input: json.RawMessage(`{"command":"ls","args":["-la"]}`),
		},
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	if len(resp.TaskGraph.Tasks) != 1 {
		t.Fatalf("task count=%d want=1", len(resp.TaskGraph.Tasks))
	}
	task := resp.TaskGraph.Tasks[0]
	if task.ID != "list-files" {
		t.Fatalf("id=%q want=list-files", task.ID)
	}
	if task.Type != "os" || task.ToolName != "shell" || task.Category != "os" {
		t.Fatalf("unexpected task routing: type=%q tool=%q category=%q", task.Type, task.ToolName, task.Category)
	}
	if task.Annotations["adapter"] != "codex" || task.Annotations["agent_id"] != "agent-1" {
		t.Fatalf("unexpected annotations: %#v", task.Annotations)
	}
}

func TestTranslateRuntimeCommand(t *testing.T) {
	kernel := NewAdapterKernel()
	resp, err := kernel.Translate(AgentActionRequest{
		Adapter:   "codex",
		AgentID:   "agent-1",
		SessionID: "session-1",
		ActionID:  "build-test",
		Action: AgentAction{
			Kind: "runtime.command",
			Input: json.RawMessage(`{
				"program":"go",
				"args":["test","./..."],
				"limits":{"wall_time_ms":300000,"memory_bytes":1073741824},
				"sandbox":{"profile":"process"},
				"control_context":{"tenant":"default","owner":"agent-1","requires_resource_reservation":true}
			}`),
			Retry:   1,
			Timeout: 300000,
		},
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	task := resp.TaskGraph.Tasks[0]
	if task.Type != "runtime" {
		t.Fatalf("type=%q want=runtime", task.Type)
	}
	if task.Retry != 1 || task.Timeout != 300000 {
		t.Fatalf("retry/timeout=%d/%d", task.Retry, task.Timeout)
	}

	var payload map[string]any
	if err := json.Unmarshal(task.Input, &payload); err != nil {
		t.Fatalf("unmarshal runtime input: %v", err)
	}
	execution := payload["execution"].(map[string]any)
	if execution["kind"] != "command" || execution["program"] != "go" {
		t.Fatalf("unexpected execution: %#v", execution)
	}
	if payload["task_id"] != "build-test" {
		t.Fatalf("task_id=%v want build-test", payload["task_id"])
	}
	if _, ok := payload["limits"].(map[string]any); !ok {
		t.Fatalf("expected limits in payload: %#v", payload)
	}
	if _, ok := payload["sandbox"].(map[string]any); !ok {
		t.Fatalf("expected sandbox in payload: %#v", payload)
	}
	cc := payload["control_context"].(map[string]any)
	if cc["tenant"] != "default" || cc["owner"] != "agent-1" {
		t.Fatalf("unexpected control_context: %#v", cc)
	}
	metadata := payload["metadata"].(map[string]any)
	if metadata["adapter"] != "codex" || metadata["agent_id"] != "agent-1" || metadata["action_id"] != "build-test" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestTranslateTaskGraphSubmitPassThrough(t *testing.T) {
	kernel := NewAdapterKernel()
	resp, err := kernel.Translate(AgentActionRequest{
		Action: AgentAction{
			Kind:  "task_graph.submit",
			Input: json.RawMessage(`{"tasks":[{"id":"direct","type":"noop","params":{"message":"hi"}}]}`),
		},
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	task := resp.TaskGraph.Tasks[0]
	if task.ID != "direct" || task.Type != "noop" {
		t.Fatalf("unexpected passthrough task: %#v", task)
	}
	if task.Annotations != nil {
		t.Fatalf("expected passthrough graph to remain unannotated, got %#v", task.Annotations)
	}
}

func TestTranslateUnknownKind(t *testing.T) {
	kernel := NewAdapterKernel()
	_, err := kernel.Translate(AgentActionRequest{Action: AgentAction{Kind: "not.real"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown action kind") {
		t.Fatalf("error=%q", err.Error())
	}
}

func TestTranslateGeneratesTaskID(t *testing.T) {
	kernel := NewAdapterKernel()
	resp, err := kernel.Translate(AgentActionRequest{
		Action: AgentAction{
			Kind:  "os.noop",
			Input: json.RawMessage(`{"message":"hi"}`),
		},
	})
	if err != nil {
		t.Fatalf("Translate error: %v", err)
	}
	if resp.TaskGraph.Tasks[0].ID == "" {
		t.Fatal("expected generated task id")
	}
	if resp.TaskGraph.Tasks[0].ID != resp.TaskIDs[0] {
		t.Fatalf("response task id mismatch: %#v", resp.TaskIDs)
	}
}
