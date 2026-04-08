package models

import "time"

// ExecutionAudit captures security and runtime audit fields per attempt.
type ExecutionAudit struct {
	TaskID        string    `json:"task_id"`
	WorkerID      string    `json:"worker_id"`
	Executor      string    `json:"executor"`
	Sandbox       string    `json:"sandbox"`
	Image         string    `json:"image,omitempty"`
	Command       string    `json:"command,omitempty"`
	ExitCode      int       `json:"exit_code,omitempty"`
	CPUTimeMS     int64     `json:"cpu_time_ms,omitempty"`
	MemoryBytes   int64     `json:"memory_bytes,omitempty"`
	NetworkEgress int64     `json:"network_egress_bytes,omitempty"`
	StdoutSHA256  string    `json:"stdout_sha256,omitempty"`
	StderrSHA256  string    `json:"stderr_sha256,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
	Success       bool      `json:"success"`
	Error         string    `json:"error,omitempty"`
}
