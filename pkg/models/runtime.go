package models

import "time"

// RuntimeStatus describes the lifecycle of an executable task run.
// It is finer-grained than TaskStatus and is intended for agent runtime semantics.
type RuntimeStatus string

const (
	RuntimeAccepted  RuntimeStatus = "accepted"
	RuntimeRunning   RuntimeStatus = "running"
	RuntimeSuccess   RuntimeStatus = "success"
	RuntimeFailed    RuntimeStatus = "failed"
	RuntimeCancelled RuntimeStatus = "cancelled"
)

// IsTerminal reports whether the runtime state is terminal.
func (s RuntimeStatus) IsTerminal() bool {
	switch s {
	case RuntimeSuccess, RuntimeFailed, RuntimeCancelled:
		return true
	default:
		return false
	}
}

// ErrorCode identifies a stable machine-readable failure category.
type ErrorCode string

const (
	ErrorUnknown         ErrorCode = "unknown"
	ErrorInvalidInput    ErrorCode = "invalid_input"
	ErrorLaunchFailed    ErrorCode = "launch_failed"
	ErrorTimeout         ErrorCode = "timeout"
	ErrorCancelled       ErrorCode = "cancelled"
	ErrorMemoryLimit     ErrorCode = "memory_limit_exceeded"
	ErrorCPULimit        ErrorCode = "cpu_limit_exceeded"
	ErrorResourceLimit   ErrorCode = "resource_limit_exceeded"
	ErrorSandboxSetup    ErrorCode = "sandbox_setup_failed"
	ErrorExitNonZero     ErrorCode = "exit_nonzero"
	ErrorDenied          ErrorCode = "denied"
	ErrorNotFound        ErrorCode = "not_found"
	ErrorExternalFailure ErrorCode = "external_failure"
	ErrorInternal        ErrorCode = "internal"
)

// RuntimeError is the normalized failure envelope returned by executors and APIs.
type RuntimeError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable,omitempty"`
	Source    string    `json:"source,omitempty"`
	Details   any       `json:"details,omitempty"`
}

// RuntimeResult is the normalized task run output envelope.
// The executor-specific payload should live under Output or Details.
type RuntimeResult struct {
	Status     RuntimeStatus `json:"status"`
	HandleID   string        `json:"handle_id,omitempty"`
	Output     any           `json:"output,omitempty"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
	DurationMS int64         `json:"duration_ms,omitempty"`
	Attempt    int           `json:"attempt,omitempty"`
	Details    any           `json:"details,omitempty"`
	Error      *RuntimeError `json:"error,omitempty"`
}

// RuntimeEventType defines structured lifecycle events emitted by the runtime.
type RuntimeEventType string

const (
	RuntimeEventSubmitted RuntimeEventType = "task_submitted"
	RuntimeEventAccepted  RuntimeEventType = "task_accepted"
	RuntimeEventStarted   RuntimeEventType = "task_started"
	RuntimeEventProgress  RuntimeEventType = "task_progressed"
	RuntimeEventRetried   RuntimeEventType = "task_retried"
	RuntimeEventSucceeded RuntimeEventType = "task_succeeded"
	RuntimeEventFailed    RuntimeEventType = "task_failed"
	RuntimeEventCancelled RuntimeEventType = "task_cancelled"
)

// RuntimeEvent is the normalized event model for execution lifecycle streaming or audit.
type RuntimeEvent struct {
	Type      RuntimeEventType `json:"type"`
	TaskID    string           `json:"task_id"`
	HandleID  string           `json:"handle_id,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
	Message   string           `json:"message,omitempty"`
	Data      any              `json:"data,omitempty"`
}
