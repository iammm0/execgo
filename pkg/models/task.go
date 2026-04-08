// Package models defines ExecGo core data structures.
package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// TaskStatus defines task lifecycle statuses.
type TaskStatus string

const (
	StatusPending      TaskStatus = "pending"
	StatusReady        TaskStatus = "ready"
	StatusLeased       TaskStatus = "leased"
	StatusRunning      TaskStatus = "running"
	StatusRetrying     TaskStatus = "retrying"
	StatusSuccess      TaskStatus = "success"
	StatusFailed       TaskStatus = "failed"
	StatusCancelled    TaskStatus = "cancelled"
	StatusTimedOut     TaskStatus = "timed_out"
	StatusCompensating TaskStatus = "compensating"
	StatusCompensated  TaskStatus = "compensated"
	StatusSkipped      TaskStatus = "skipped"
)

// IsTerminal reports whether task status is terminal.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusCancelled, StatusTimedOut, StatusCompensated, StatusSkipped:
		return true
	default:
		return false
	}
}

// Task is the core execution unit between AI and runtime.
type Task struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id,omitempty"`
	Type       string          `json:"type"`
	Params     json.RawMessage `json:"params,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Category   string          `json:"execution_category,omitempty"` // mcp | cli-skills | os | plugin

	DependsOn      []string `json:"depends_on,omitempty"`
	CompensateWith []string `json:"compensate_with,omitempty"`

	Retry       int        `json:"retry,omitempty"`
	Priority    int        `json:"priority,omitempty"` // 0~9, 9 highest
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	Timeout     int64      `json:"timeout,omitempty"` // milliseconds

	Status    TaskStatus      `json:"status"`
	RunStatus string          `json:"run_status,omitempty"`
	HandleID  string          `json:"handle_id,omitempty"`
	Progress  json.RawMessage `json:"progress,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Runtime   *RuntimeResult  `json:"runtime,omitempty"`
	Error     string          `json:"error,omitempty"`

	Attempt         int       `json:"attempt,omitempty"`
	Version         int64     `json:"version,omitempty"`
	ExpectedVersion int64     `json:"expected_version,omitempty"`
	LeaseOwner      string    `json:"lease_owner,omitempty"`
	LeaseUntil      time.Time `json:"lease_until,omitempty"`

	RemainingDeps int       `json:"remaining_deps,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TaskGraph defines one submitted DAG batch.
type TaskGraph struct {
	WorkflowID     string  `json:"workflow_id,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
	Tasks          []*Task `json:"tasks"`
}

// Validate checks graph correctness.
func (g *TaskGraph) Validate() error {
	if len(g.Tasks) == 0 {
		return fmt.Errorf("task graph is empty")
	}

	ids := make(map[string]bool, len(g.Tasks))
	for _, t := range g.Tasks {
		if t.ID == "" {
			return fmt.Errorf("task id is required")
		}
		if t.Type == "" {
			return fmt.Errorf("task %q: type is required", t.ID)
		}
		if ids[t.ID] {
			return fmt.Errorf("duplicate task id: %q", t.ID)
		}
		ids[t.ID] = true

		if t.Priority < 0 || t.Priority > 9 {
			return fmt.Errorf("task %q: priority must be in [0,9]", t.ID)
		}
		if t.Retry < 0 {
			return fmt.Errorf("task %q: retry cannot be negative", t.ID)
		}
		if t.Timeout < 0 {
			return fmt.Errorf("task %q: timeout cannot be negative", t.ID)
		}
	}

	for _, t := range g.Tasks {
		for _, dep := range t.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
			if dep == t.ID {
				return fmt.Errorf("task %q cannot depend on itself", t.ID)
			}
		}
		for _, comp := range t.CompensateWith {
			if !ids[comp] {
				return fmt.Errorf("task %q compensation references unknown task %q", t.ID, comp)
			}
			if comp == t.ID {
				return fmt.Errorf("task %q cannot compensate itself", t.ID)
			}
		}
	}

	if _, err := TopologicalOrder(g.Tasks); err != nil {
		return err
	}

	return nil
}

// TopologicalOrder returns DAG topological order using Kahn algorithm.
func TopologicalOrder(tasks []*Task) ([]string, error) {
	inDegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))

	for _, t := range tasks {
		if _, ok := inDegree[t.ID]; !ok {
			inDegree[t.ID] = 0
		}
		for _, dep := range t.DependsOn {
			inDegree[t.ID]++
			dependents[dep] = append(dependents[dep], t.ID)
		}
	}

	queue := make([]string, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	order := make([]string, 0, len(tasks))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		for _, child := range dependents[node] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
		sort.Strings(queue)
	}

	if len(order) != len(tasks) {
		return nil, fmt.Errorf("cycle detected in task graph")
	}
	return order, nil
}

// SubmitResponse is returned after /tasks submission.
type SubmitResponse struct {
	Accepted      int      `json:"accepted"`
	TaskIDs       []string `json:"task_ids"`
	WorkflowID    string   `json:"workflow_id,omitempty"`
	IdempotentHit bool     `json:"idempotent_hit,omitempty"`
}

// ErrorResponse is unified error payload.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse is /health payload.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

// MetricsResponse is legacy JSON metrics payload.
type MetricsResponse struct {
	TasksTotal     int64            `json:"tasks_total"`
	TasksRunning   int64            `json:"tasks_running"`
	TasksSucceeded int64            `json:"tasks_succeeded"`
	TasksFailed    int64            `json:"tasks_failed"`
	ByType         map[string]int64 `json:"by_type"`
}

// WorkerNode tracks worker liveness/capabilities.
type WorkerNode struct {
	ID           string            `json:"id"`
	Capabilities map[string]string `json:"capabilities,omitempty"`
	Status       string            `json:"status"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}
