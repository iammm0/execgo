// Package adapter translates mature-agent actions into ExecGo Task DSL.
// It is the companion kernel between higher-level agents and the execution kernel.
package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

const SchemaVersion = "adapter.v1"

// AdapterKernel validates agent actions and translates them into TaskGraph values.
type AdapterKernel struct{}

// NewAdapterKernel creates the default deterministic adapter kernel.
func NewAdapterKernel() *AdapterKernel {
	return &AdapterKernel{}
}

// AgentActionRequest is the public envelope accepted from mature agents.
type AgentActionRequest struct {
	Adapter   string            `json:"adapter,omitempty"`
	AgentID   string            `json:"agent_id,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	ActionID  string            `json:"action_id,omitempty"`
	Action    AgentAction       `json:"action"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// AgentAction is the framework-neutral action shape translated by AdapterKernel.
type AgentAction struct {
	Kind      string          `json:"kind"`
	ToolName  string          `json:"tool_name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	DependsOn []string        `json:"depends_on,omitempty"`
	Retry     int             `json:"retry,omitempty"`
	Timeout   int64           `json:"timeout,omitempty"`
}

// AgentActionResponse returns the translated graph and trace metadata.
type AgentActionResponse struct {
	Accepted         int               `json:"accepted"`
	TaskIDs          []string          `json:"task_ids"`
	TaskGraph        *models.TaskGraph `json:"task_graph"`
	TranslationTrace map[string]any    `json:"translation_trace,omitempty"`
}

// AdapterCapabilitiesResponse describes the adapter contract and supported profiles.
type AdapterCapabilitiesResponse struct {
	SchemaVersion      string   `json:"schema_version"`
	Profiles           []string `json:"profiles"`
	ActionKinds        []string `json:"action_kinds"`
	CompatibilityNotes []string `json:"compatibility_notes,omitempty"`
}

// ToolManifestResponse exposes ExecGo tools in an agent-friendly form.
type ToolManifestResponse struct {
	SchemaVersion string          `json:"schema_version"`
	Tools         []AgentToolSpec `json:"tools"`
}

// AgentToolSpec describes one adapter-level tool/action.
type AgentToolSpec struct {
	Name        string         `json:"name"`
	ActionKind  string         `json:"action_kind"`
	Category    string         `json:"category"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Aliases     []string       `json:"aliases,omitempty"`
}

var supportedKinds = []string{
	"os.shell",
	"os.file",
	"os.http",
	"os.dns",
	"os.tcp",
	"os.sleep",
	"os.noop",
	"runtime.command",
	"runtime.script",
	"mcp.call",
	"cli.run",
	"task_graph.submit",
}

// Capabilities returns the adapter profiles and stable action vocabulary.
func (k *AdapterKernel) Capabilities() AdapterCapabilitiesResponse {
	_ = k
	return AdapterCapabilitiesResponse{
		SchemaVersion: SchemaVersion,
		Profiles:      []string{"generic", "claudecode", "codex", "openclaw"},
		ActionKinds:   append([]string{}, supportedKinds...),
		CompatibilityNotes: []string{
			"POST /tasks remains the direct Task DSL path.",
			"POST /adapters/actions translates structured mature-agent actions into TaskGraph submissions.",
			"runtime.command and runtime.script use the runtime executor for process scheduling, resource limits, and sandbox policy.",
		},
	}
}

// ToolManifest returns a stable manifest that mature agents can expose as tools or skills.
func (k *AdapterKernel) ToolManifest() ToolManifestResponse {
	_ = k
	objectSchema := map[string]any{"type": "object"}
	return ToolManifestResponse{
		SchemaVersion: SchemaVersion,
		Tools: []AgentToolSpec{
			{Name: "execgo.os.shell", ActionKind: "os.shell", Category: "os", Description: "Run an allowlisted shell command or script", InputSchema: objectSchema, Aliases: []string{"shell", "bash", "terminal.command"}},
			{Name: "execgo.os.file", ActionKind: "os.file", Category: "os", Description: "Read, write, append, delete, or stat a file", InputSchema: objectSchema, Aliases: []string{"file.read", "file.write"}},
			{Name: "execgo.os.http", ActionKind: "os.http", Category: "os", Description: "Issue an HTTP request", InputSchema: objectSchema, Aliases: []string{"http.request"}},
			{Name: "execgo.os.dns", ActionKind: "os.dns", Category: "os", Description: "Perform a DNS lookup", InputSchema: objectSchema},
			{Name: "execgo.os.tcp", ActionKind: "os.tcp", Category: "os", Description: "Probe a TCP endpoint", InputSchema: objectSchema},
			{Name: "execgo.os.sleep", ActionKind: "os.sleep", Category: "os", Description: "Delay execution for a duration", InputSchema: objectSchema},
			{Name: "execgo.os.noop", ActionKind: "os.noop", Category: "os", Description: "No-op action for testing and placeholders", InputSchema: objectSchema},
			{Name: "execgo.runtime.command", ActionKind: "runtime.command", Category: "runtime", Description: "Submit a command to execgo-runtime with resource and sandbox policy", InputSchema: objectSchema, Aliases: []string{"command"}},
			{Name: "execgo.runtime.script", ActionKind: "runtime.script", Category: "runtime", Description: "Submit a script to execgo-runtime with resource and sandbox policy", InputSchema: objectSchema, Aliases: []string{"script"}},
			{Name: "execgo.mcp.call", ActionKind: "mcp.call", Category: "mcp", Description: "Call an MCP tool through ExecGo", InputSchema: objectSchema},
			{Name: "execgo.cli.run", ActionKind: "cli.run", Category: "cli-skills", Description: "Run a local CLI skill command", InputSchema: objectSchema},
			{Name: "execgo.task_graph.submit", ActionKind: "task_graph.submit", Category: "task-dsl", Description: "Submit a prebuilt ExecGo TaskGraph", InputSchema: objectSchema},
		},
	}
}

// Translate converts one agent action request into an ExecGo TaskGraph.
func (k *AdapterKernel) Translate(req AgentActionRequest) (*AgentActionResponse, error) {
	_ = k
	rawKind := strings.TrimSpace(req.Action.Kind)
	kind, err := NormalizeActionKind(rawKind)
	if err != nil {
		return nil, err
	}

	var graph *models.TaskGraph
	switch kind {
	case "task_graph.submit":
		graph, err = graphFromActionInput(req.Action.Input)
	case "runtime.command", "runtime.script":
		var task *models.Task
		task, err = runtimeTask(req, kind)
		graph = &models.TaskGraph{Tasks: []*models.Task{task}}
	case "mcp.call":
		var task *models.Task
		task, err = mcpTask(req, kind)
		graph = &models.TaskGraph{Tasks: []*models.Task{task}}
	case "cli.run":
		var task *models.Task
		task, err = cliTask(req, kind)
		graph = &models.TaskGraph{Tasks: []*models.Task{task}}
	default:
		if strings.HasPrefix(kind, "os.") {
			var task *models.Task
			task, err = osTask(req, rawKind, kind)
			graph = &models.TaskGraph{Tasks: []*models.Task{task}}
		} else {
			err = fmt.Errorf("invalid adapter action: unknown action kind %q", rawKind)
		}
	}
	if err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("invalid adapter action: translation produced no task graph")
	}
	if err := graph.Validate(); err != nil {
		return nil, fmt.Errorf("invalid adapter action: %w", err)
	}

	taskIDs := taskIDs(graph)
	return &AgentActionResponse{
		TaskIDs:   taskIDs,
		TaskGraph: graph,
		TranslationTrace: map[string]any{
			"schema_version":  SchemaVersion,
			"adapter":         adapterName(req.Adapter),
			"agent_id":        req.AgentID,
			"session_id":      req.SessionID,
			"action_id":       req.ActionID,
			"source_kind":     rawKind,
			"normalized_kind": kind,
			"task_ids":        taskIDs,
		},
	}, nil
}

// NormalizeActionKind maps mature-agent aliases onto the stable v1 action vocabulary.
func NormalizeActionKind(kind string) (string, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case "":
		return "", fmt.Errorf("invalid adapter action: action.kind is required")
	case "shell", "bash", "terminal.command":
		return "os.shell", nil
	case "file.read", "file.write":
		return "os.file", nil
	case "http.request":
		return "os.http", nil
	case "command":
		return "runtime.command", nil
	case "script":
		return "runtime.script", nil
	case "noop":
		return "os.noop", nil
	case "dns":
		return "os.dns", nil
	case "tcp":
		return "os.tcp", nil
	case "sleep":
		return "os.sleep", nil
	}
	for _, supported := range supportedKinds {
		if k == supported {
			return k, nil
		}
	}
	return "", fmt.Errorf("invalid adapter action: unknown action kind %q", kind)
}

func osTask(req AgentActionRequest, rawKind, kind string) (*models.Task, error) {
	input, err := normalizeOSInput(req.Action.Input, rawKind)
	if err != nil {
		return nil, err
	}
	return &models.Task{
		ID:          taskID(req.ActionID),
		Type:        "os",
		ToolName:    strings.TrimPrefix(kind, "os."),
		Input:       input,
		Category:    "os",
		DependsOn:   append([]string{}, req.Action.DependsOn...),
		Retry:       req.Action.Retry,
		Timeout:     req.Action.Timeout,
		Annotations: provenance(req, kind),
	}, nil
}

func runtimeTask(req AgentActionRequest, kind string) (*models.Task, error) {
	payload, taskIDValue, err := runtimePayload(req, kind)
	if err != nil {
		return nil, err
	}
	return &models.Task{
		ID:          taskIDValue,
		Type:        "runtime",
		Input:       payload,
		Category:    "runtime",
		DependsOn:   append([]string{}, req.Action.DependsOn...),
		Retry:       req.Action.Retry,
		Timeout:     req.Action.Timeout,
		Annotations: provenance(req, kind),
	}, nil
}

func mcpTask(req AgentActionRequest, kind string) (*models.Task, error) {
	toolName := strings.TrimSpace(req.Action.ToolName)
	if toolName == "" {
		input, err := rawObject(req.Action.Input)
		if err != nil {
			return nil, err
		}
		toolName, _ = input["tool_name"].(string)
	}
	if strings.TrimSpace(toolName) == "" {
		return nil, fmt.Errorf("invalid adapter action: tool_name is required for mcp.call")
	}
	return &models.Task{
		ID:          taskID(req.ActionID),
		Type:        "mcp",
		ToolName:    toolName,
		Input:       cloneRaw(req.Action.Input),
		Category:    "mcp",
		DependsOn:   append([]string{}, req.Action.DependsOn...),
		Retry:       req.Action.Retry,
		Timeout:     req.Action.Timeout,
		Annotations: provenance(req, kind),
	}, nil
}

func cliTask(req AgentActionRequest, kind string) (*models.Task, error) {
	return &models.Task{
		ID:          taskID(req.ActionID),
		Type:        "cli-skills",
		ToolName:    "cli.run",
		Input:       cloneRaw(req.Action.Input),
		Category:    "cli-skills",
		DependsOn:   append([]string{}, req.Action.DependsOn...),
		Retry:       req.Action.Retry,
		Timeout:     req.Action.Timeout,
		Annotations: provenance(req, kind),
	}, nil
}

func runtimePayload(req AgentActionRequest, kind string) (json.RawMessage, string, error) {
	in, err := rawObject(req.Action.Input)
	if err != nil {
		return nil, "", err
	}

	taskIDValue := taskID(req.ActionID)
	if req.ActionID == "" {
		if existing, ok := stringValue(in["task_id"]); ok {
			taskIDValue = existing
		}
	}

	payload := make(map[string]any)
	payload["task_id"] = taskIDValue

	if execution, ok := in["execution"].(map[string]any); ok {
		execution = cloneMap(execution)
		if _, exists := execution["kind"]; !exists {
			execution["kind"] = strings.TrimPrefix(kind, "runtime.")
		}
		payload["execution"] = execution
	} else {
		execution, err := runtimeExecution(in, kind)
		if err != nil {
			return nil, "", err
		}
		payload["execution"] = execution
	}

	for _, key := range []string{"limits", "sandbox", "policy", "control_context"} {
		if v, ok := in[key]; ok {
			payload[key] = v
		}
	}
	metadata := mergeStringMaps(stringMapValue(in["metadata"]), req.Metadata, provenance(req, kind))
	if len(metadata) > 0 {
		payload["metadata"] = metadata
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("invalid adapter action: marshal runtime payload: %w", err)
	}
	return data, taskIDValue, nil
}

func runtimeExecution(in map[string]any, kind string) (map[string]any, error) {
	execution := map[string]any{"kind": strings.TrimPrefix(kind, "runtime.")}
	if kind == "runtime.command" {
		program, ok := stringValue(in["program"])
		if !ok {
			return nil, fmt.Errorf("invalid adapter action: input.program is required for runtime.command")
		}
		execution["program"] = program
		if args, ok := stringSliceValue(in["args"]); ok {
			execution["args"] = args
		}
	} else {
		script, ok := stringValue(in["script"])
		if !ok {
			return nil, fmt.Errorf("invalid adapter action: input.script is required for runtime.script")
		}
		execution["script"] = script
		if interpreter, ok := stringSliceValue(in["interpreter"]); ok {
			execution["interpreter"] = interpreter
		}
	}
	if env := stringMapValue(in["env"]); len(env) > 0 {
		execution["env"] = env
	}
	return execution, nil
}

func graphFromActionInput(raw json.RawMessage) (*models.TaskGraph, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("invalid adapter action: input is required for task_graph.submit")
	}
	var env struct {
		TaskGraph *models.TaskGraph `json:"task_graph,omitempty"`
		Tasks     []*models.Task    `json:"tasks,omitempty"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("invalid adapter action: parse task graph input: %w", err)
	}
	if env.TaskGraph != nil {
		return env.TaskGraph, nil
	}
	if env.Tasks != nil {
		return &models.TaskGraph{Tasks: env.Tasks}, nil
	}
	return nil, fmt.Errorf("invalid adapter action: input must contain tasks or task_graph")
}

func normalizeOSInput(raw json.RawMessage, rawKind string) (json.RawMessage, error) {
	input := cloneRaw(raw)
	switch strings.ToLower(strings.TrimSpace(rawKind)) {
	case "file.read":
		return inputWithDefaultAction(input, "read")
	case "file.write":
		return inputWithDefaultAction(input, "write")
	default:
		return input, nil
	}
}

func inputWithDefaultAction(raw json.RawMessage, action string) (json.RawMessage, error) {
	obj, err := rawObject(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := obj["action"]; !ok {
		obj["action"] = action
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("invalid adapter action: marshal file input: %w", err)
	}
	return data, nil
}

func rawObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("invalid adapter action: input must be a JSON object: %w", err)
	}
	if obj == nil {
		return map[string]any{}, nil
	}
	return obj, nil
}

func taskID(id string) string {
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	return fmt.Sprintf("adapter-%d", time.Now().UnixNano())
}

func taskIDs(graph *models.TaskGraph) []string {
	out := make([]string, 0, len(graph.Tasks))
	for _, task := range graph.Tasks {
		out = append(out, task.ID)
	}
	return out
}

func provenance(req AgentActionRequest, kind string) map[string]string {
	out := mergeStringMaps(req.Metadata, map[string]string{
		"adapter_schema": SchemaVersion,
		"adapter":        adapterName(req.Adapter),
		"action_kind":    kind,
	})
	if strings.TrimSpace(req.AgentID) != "" {
		out["agent_id"] = strings.TrimSpace(req.AgentID)
	}
	if strings.TrimSpace(req.SessionID) != "" {
		out["session_id"] = strings.TrimSpace(req.SessionID)
	}
	if strings.TrimSpace(req.ActionID) != "" {
		out["action_id"] = strings.TrimSpace(req.ActionID)
	}
	return out
}

func adapterName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "generic"
	}
	return name
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeStringMaps(items ...map[string]string) map[string]string {
	out := make(map[string]string)
	for _, item := range items {
		for k, v := range item {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				out[k] = v
			}
		}
	}
	return out
}

func stringValue(v any) (string, bool) {
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && s != ""
}

func stringSliceValue(v any) ([]string, bool) {
	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func stringMapValue(v any) map[string]string {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, item := range raw {
		if s, ok := item.(string); ok {
			out[k] = s
		}
	}
	return out
}
