package models

import (
	"encoding/json"
	"time"
)

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
	ErrorTimeout         ErrorCode = "timeout"
	ErrorCancelled       ErrorCode = "cancelled"
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
	RuntimeEventSubmitted                 RuntimeEventType = "task_submitted"
	RuntimeEventAccepted                  RuntimeEventType = "task_accepted"
	RuntimeEventReady                     RuntimeEventType = "task_ready"
	RuntimeEventLeased                    RuntimeEventType = "task_leased"
	RuntimeEventStarted                   RuntimeEventType = "task_started"
	RuntimeEventProgress                  RuntimeEventType = "task_progressed"
	RuntimeEventRetried                   RuntimeEventType = "task_retried"
	RuntimeEventRetryScheduled            RuntimeEventType = "task_retry_scheduled"
	RuntimeEventSucceeded                 RuntimeEventType = "task_succeeded"
	RuntimeEventFailed                    RuntimeEventType = "task_failed"
	RuntimeEventCancelled                 RuntimeEventType = "task_cancelled"
	RuntimeEventTimedOut                  RuntimeEventType = "task_timed_out"
	RuntimeEventCompensationTriggered     RuntimeEventType = "task_compensation_triggered"
	RuntimeEventCompensated               RuntimeEventType = "task_compensated"
	RuntimeEventInvalidTransitionRejected RuntimeEventType = "task_invalid_transition_rejected"
	RuntimeEventWorkerRegistered          RuntimeEventType = "worker_registered"
	RuntimeEventWorkerHeartbeat           RuntimeEventType = "worker_heartbeat"
	RuntimeEventWorkerHeartbeatMissed     RuntimeEventType = "worker_heartbeat_missed"
	RuntimeEventWorkerOffline             RuntimeEventType = "worker_offline"
	RuntimeEventWorkerAudit               RuntimeEventType = "worker_audit"
)

// RuntimeEventMetadata carries tracing/causality metadata.
type RuntimeEventMetadata struct {
	TraceID       string `json:"trace_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Producer      string `json:"producer,omitempty"`
	NodeID        string `json:"node_id,omitempty"`
	WorkflowID    string `json:"workflow_id,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	WorkerID      string `json:"worker_id,omitempty"`
	Attempt       int    `json:"attempt,omitempty"`
}

// RuntimeEvent is the immutable event contract for event sourcing.
type RuntimeEvent struct {
	EventID          string               `json:"event_id"`
	GlobalOffset     int64                `json:"global_offset,omitempty"`
	Stream           string               `json:"stream,omitempty"`
	AggregateID      string               `json:"aggregate_id"`
	AggregateVersion int64                `json:"aggregate_version"`
	Type             RuntimeEventType     `json:"event_type"`
	Payload          json.RawMessage      `json:"payload,omitempty"`
	Metadata         RuntimeEventMetadata `json:"metadata,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`

	// Legacy compatibility fields.
	TaskID    string    `json:"task_id,omitempty"`
	HandleID  string    `json:"handle_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Message   string    `json:"message,omitempty"`
	Data      any       `json:"data,omitempty"`
}

// NormalizeLegacyFields back-fills old RuntimeEvent fields for compatibility.
func (e *RuntimeEvent) NormalizeLegacyFields() {
	if e == nil {
		return
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = e.CreatedAt
	}
	if e.TaskID == "" {
		e.TaskID = e.Metadata.TaskID
		if e.TaskID == "" {
			e.TaskID = e.AggregateID
		}
	}
}
