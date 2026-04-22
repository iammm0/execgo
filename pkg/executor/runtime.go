package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

const (
	RuntimeBaseURLEnv     = "EXECGO_RUNTIME_URL"
	DefaultRuntimeBaseURL = "http://127.0.0.1:8080"
)

type RuntimeExecutor struct {
	baseURL string
	client  *http.Client

	mu           sync.RWMutex
	handleToTask map[string]string
}

type runtimeSubmitResponse struct {
	TaskID   string               `json:"task_id"`
	HandleID string               `json:"handle_id"`
	Status   models.RuntimeStatus `json:"status"`
}

type runtimeTaskResponse struct {
	TaskID     string               `json:"task_id"`
	HandleID   string               `json:"handle_id"`
	Status     models.RuntimeStatus `json:"status"`
	StartedAt  *time.Time           `json:"started_at,omitempty"`
	FinishedAt *time.Time           `json:"finished_at,omitempty"`
	DurationMS int64                `json:"duration_ms,omitempty"`
	Error      *runtimeAPIError     `json:"error,omitempty"`
}

type runtimeAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type runtimeErrorEnvelope struct {
	Error *runtimeAPIError `json:"error,omitempty"`
}

type runtimeEventsEnvelope struct {
	Events []models.RuntimeEvent `json:"events"`
}

func NewRuntimeExecutor(baseURL string, client *http.Client) *RuntimeExecutor {
	baseURL = strings.TrimSpace(strings.TrimRight(baseURL, "/"))
	if baseURL == "" {
		baseURL = DefaultRuntimeBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &RuntimeExecutor{
		baseURL:       baseURL,
		client:        client,
		handleToTask:  make(map[string]string),
	}
}

func NewRuntimeExecutorFromEnv() *RuntimeExecutor {
	return NewRuntimeExecutor(os.Getenv(RuntimeBaseURLEnv), nil)
}

func (e *RuntimeExecutor) Name() string     { return "runtime" }
func (e *RuntimeExecutor) Category() string { return "runtime" }

func (e *RuntimeExecutor) Execute(ctx context.Context, task *models.Task) (*Result, error) {
	payload, err := runtimePayload(task)
	if err != nil {
		return &Result{
			TaskID: task.ID,
			Status: models.RuntimeFailed,
			Error: &models.RuntimeError{
				Code:      models.ErrorInvalidInput,
				Message:   err.Error(),
				Retryable: false,
				Source:    "runtime-executor",
			},
		}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/v1/tasks", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create runtime submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return &Result{
			TaskID: task.ID,
			Status: models.RuntimeFailed,
			Error: &models.RuntimeError{
				Code:      models.ErrorExternalFailure,
				Message:   err.Error(),
				Retryable: true,
				Source:    "runtime-executor",
			},
		}, fmt.Errorf("runtime submit request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read runtime submit response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rerr := decodeRuntimeAPIError(body)
		if rerr == nil {
			rerr = &models.RuntimeError{
				Code:      models.ErrorExternalFailure,
				Message:   fmt.Sprintf("runtime submit returned status %d", resp.StatusCode),
				Retryable: resp.StatusCode >= 500,
				Source:    "execgo-runtime",
			}
		}
		return &Result{
			TaskID: task.ID,
			Status: models.RuntimeFailed,
			Error:  rerr,
			Output: cloneJSON(body),
		}, errors.New(rerr.Message)
	}

	var accepted runtimeSubmitResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		return nil, fmt.Errorf("decode runtime submit response: %w", err)
	}
	e.rememberRuntimeMapping(accepted.HandleID, accepted.TaskID)

	handleID := strings.TrimSpace(accepted.HandleID)
	taskID := strings.TrimSpace(accepted.TaskID)
	if handleID == "" {
		handleID = taskID
	}
	return &Result{
		TaskID:   taskID,
		HandleID: handleID,
		Status:   accepted.Status,
		Output:   cloneJSON(body),
	}, nil
}

func (e *RuntimeExecutor) GetHandle(handleID string) (*Result, bool) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return nil, false
	}
	res, ok := e.getHandleByRef(handleID)
	if ok {
		return res, true
	}

	// Backward/compat: some runtimes might expose polling endpoints keyed by task_id
	// even if the submit response returns a different handle_id.
	if taskID, ok := e.lookupTaskID(handleID); ok && taskID != "" && taskID != handleID {
		return e.getHandleByRef(taskID)
	}
	return nil, false
}

func (e *RuntimeExecutor) CancelHandle(handleID string) (*Result, bool) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return nil, false
	}
	res, ok := e.cancelByRef(handleID)
	if ok {
		return res, true
	}
	if taskID, ok := e.lookupTaskID(handleID); ok && taskID != "" && taskID != handleID {
		return e.cancelByRef(taskID)
	}
	return nil, false
}

func (e *RuntimeExecutor) GetEvents(handleID string) ([]models.RuntimeEvent, bool) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return nil, false
	}
	evs, ok := e.getEventsByRef(handleID)
	if ok {
		return evs, true
	}
	if taskID, ok := e.lookupTaskID(handleID); ok && taskID != "" && taskID != handleID {
		return e.getEventsByRef(taskID)
	}
	return nil, false
}

func (e *RuntimeExecutor) getHandleByRef(ref string) (*Result, bool) {
	req, err := http.NewRequest(http.MethodGet, e.baseURL+"/api/v1/tasks/"+ref, nil)
	if err != nil {
		return nil, false
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rerr := decodeRuntimeAPIError(body)
		res := &Result{
			TaskID:   ref,
			HandleID: ref,
			Status:   models.RuntimeFailed,
			Output:   cloneJSON(body),
		}
		if rerr != nil {
			res.Error = rerr
		}
		return res, true
	}

	var task runtimeTaskResponse
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, false
	}
	e.rememberRuntimeMapping(task.HandleID, task.TaskID)

	outHandle := strings.TrimSpace(task.HandleID)
	if outHandle == "" {
		outHandle = ref
	}
	res := &Result{
		TaskID:     task.TaskID,
		HandleID:   outHandle,
		Status:     task.Status,
		StartedAt:  task.StartedAt,
		FinishedAt: task.FinishedAt,
		DurationMS: task.DurationMS,
		Output:     cloneJSON(body),
	}
	if task.Error != nil {
		res.Error = convertRuntimeAPIError(task.Error)
	}
	return res, true
}

func (e *RuntimeExecutor) getEventsByRef(ref string) ([]models.RuntimeEvent, bool) {
	req, err := http.NewRequest(http.MethodGet, e.baseURL+"/api/v1/tasks/"+ref+"/events", nil)
	if err != nil {
		return nil, false
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// We currently only expose a bool for GetEvents; the caller can re-fetch task
		// status for structured error if needed.
		return nil, true
	}

	// Tolerate both: `[ {...}, ... ]` and `{ "events": [ ... ] }`
	var direct []models.RuntimeEvent
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct, true
	}
	var env runtimeEventsEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Events != nil {
		return env.Events, true
	}
	return nil, true
}

func (e *RuntimeExecutor) cancelByRef(ref string) (*Result, bool) {
	req, err := http.NewRequest(http.MethodPost, e.baseURL+"/api/v1/tasks/"+ref+"/kill", nil)
	if err != nil {
		return nil, false
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return &Result{
			TaskID:   ref,
			HandleID: ref,
			Status:   models.RuntimeFailed,
			Error: &models.RuntimeError{
				Code:      models.ErrorExternalFailure,
				Message:   err.Error(),
				Retryable: true,
				Source:    "runtime-executor",
			},
		}, true
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rerr := decodeRuntimeAPIError(body)
		if rerr == nil {
			rerr = &models.RuntimeError{
				Code:      models.ErrorExternalFailure,
				Message:   fmt.Sprintf("runtime kill returned status %d", resp.StatusCode),
				Retryable: resp.StatusCode >= 500,
				Source:    "execgo-runtime",
			}
		}
		return &Result{
			TaskID:   ref,
			HandleID: ref,
			Status:   models.RuntimeFailed,
			Error:    rerr,
			Output:   cloneJSON(body),
		}, true
	}

	// execgo-runtime is expected to return a task-like object, but we tolerate empty body.
	if len(body) == 0 {
		return &Result{
			TaskID:   ref,
			HandleID: ref,
			Status:   models.RuntimeCancelled,
		}, true
	}
	var task runtimeTaskResponse
	if err := json.Unmarshal(body, &task); err == nil && task.Status != "" {
		e.rememberRuntimeMapping(task.HandleID, task.TaskID)
		outHandle := strings.TrimSpace(task.HandleID)
		if outHandle == "" {
			outHandle = ref
		}
		res := &Result{
			TaskID:     task.TaskID,
			HandleID:   outHandle,
			Status:     task.Status,
			StartedAt:  task.StartedAt,
			FinishedAt: task.FinishedAt,
			DurationMS: task.DurationMS,
			Output:     cloneJSON(body),
		}
		if task.Error != nil {
			res.Error = convertRuntimeAPIError(task.Error)
		}
		return res, true
	}

	// Fallback: treat it as cancelled if runtime returned 2xx but unknown body.
	return &Result{
		TaskID:   ref,
		HandleID: ref,
		Status:   models.RuntimeCancelled,
		Output:   cloneJSON(body),
	}, true
}

func (e *RuntimeExecutor) ListTools(ctx context.Context) ([]Tool, error) {
	_ = ctx
	return []Tool{
		{
			Name:        "runtime.execute",
			Category:    "runtime",
			Description: "Submit task to execgo-runtime and poll/cancel by handle",
			InputSchema: map[string]any{"type": "object"},
		},
	}, nil
}

func (e *RuntimeExecutor) GetRuntimeInfo(ctx context.Context) (json.RawMessage, error) {
	return e.getRuntimeJSON(ctx, "/api/v1/runtime/info")
}

func (e *RuntimeExecutor) GetRuntimeCapabilities(ctx context.Context) (json.RawMessage, error) {
	return e.getRuntimeJSON(ctx, "/api/v1/runtime/capabilities")
}

func (e *RuntimeExecutor) GetRuntimeResources(ctx context.Context) (json.RawMessage, error) {
	return e.getRuntimeJSON(ctx, "/api/v1/runtime/resources")
}

func (e *RuntimeExecutor) GetRuntimeConfig(ctx context.Context) (json.RawMessage, error) {
	return e.getRuntimeJSON(ctx, "/api/v1/runtime/config")
}

func (e *RuntimeExecutor) HealthCheck() error {
	req, err := http.NewRequest(http.MethodGet, e.baseURL+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("runtime readiness returned status %d", resp.StatusCode)
	}
	return nil
}

func (e *RuntimeExecutor) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func (e *RuntimeExecutor) getRuntimeJSON(ctx context.Context, path string) (json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("runtime path is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rerr := decodeRuntimeAPIError(body)
		if rerr == nil {
			rerr = &models.RuntimeError{
				Code:      models.ErrorExternalFailure,
				Message:   fmt.Sprintf("runtime endpoint %s returned status %d", path, resp.StatusCode),
				Retryable: resp.StatusCode >= 500,
				Source:    "execgo-runtime",
				Details: map[string]any{
					"path": path,
				},
			}
		}
		return nil, errors.New(rerr.Message)
	}
	return cloneJSON(body), nil
}

func runtimePayload(task *models.Task) ([]byte, error) {
	raw := task.Input
	if len(raw) == 0 {
		raw = task.Params
	}
	var payload map[string]any
	if len(raw) == 0 {
		payload = make(map[string]any)
	} else if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse runtime input: %w", err)
	}
	if strings.TrimSpace(task.ID) != "" {
		if _, ok := payload["task_id"]; !ok {
			payload["task_id"] = task.ID
		}
	}
	return json.Marshal(payload)
}

func (e *RuntimeExecutor) rememberRuntimeMapping(handleID, taskID string) {
	handleID = strings.TrimSpace(handleID)
	taskID = strings.TrimSpace(taskID)
	if handleID == "" || taskID == "" || handleID == taskID {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handleToTask == nil {
		e.handleToTask = make(map[string]string)
	}
	e.handleToTask[handleID] = taskID
}

func (e *RuntimeExecutor) lookupTaskID(handleID string) (string, bool) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return "", false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.handleToTask == nil {
		return "", false
	}
	taskID, ok := e.handleToTask[handleID]
	return taskID, ok
}

func decodeRuntimeAPIError(body []byte) *models.RuntimeError {
	var env runtimeErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil {
		return convertRuntimeAPIError(env.Error)
	}
	// Tolerate direct error object (without envelope).
	var direct runtimeAPIError
	if err := json.Unmarshal(body, &direct); err == nil && (direct.Code != "" || direct.Message != "") {
		return convertRuntimeAPIError(&direct)
	}
	// Tolerate string error bodies.
	var msg string
	if err := json.Unmarshal(body, &msg); err == nil && strings.TrimSpace(msg) != "" {
		return &models.RuntimeError{
			Code:      models.ErrorExternalFailure,
			Message:   msg,
			Retryable: true,
			Source:    "execgo-runtime",
		}
	}
	return nil
}

func convertRuntimeAPIError(in *runtimeAPIError) *models.RuntimeError {
	if in == nil {
		return nil
	}
	return &models.RuntimeError{
		Code:      mapRuntimeErrorCode(in.Code),
		Message:   in.Message,
		Retryable: isRetryableRuntimeCode(in.Code),
		Source:    "execgo-runtime",
		Details: map[string]any{
			"raw_error_code": in.Code,
			"details":        in.Details,
		},
	}
}

func mapRuntimeErrorCode(code string) models.ErrorCode {
	switch strings.TrimSpace(code) {
	case string(models.ErrorInvalidInput):
		return models.ErrorInvalidInput
	case string(models.ErrorLaunchFailed):
		return models.ErrorLaunchFailed
	case string(models.ErrorTimeout):
		return models.ErrorTimeout
	case string(models.ErrorCancelled):
		return models.ErrorCancelled
	case string(models.ErrorMemoryLimit):
		return models.ErrorMemoryLimit
	case string(models.ErrorCPULimit):
		return models.ErrorCPULimit
	case string(models.ErrorResourceLimit):
		return models.ErrorResourceLimit
	case string(models.ErrorSandboxSetup):
		return models.ErrorSandboxSetup
	case string(models.ErrorExitNonZero):
		return models.ErrorExitNonZero
	case string(models.ErrorDenied):
		return models.ErrorDenied
	case string(models.ErrorNotFound):
		return models.ErrorNotFound
	case string(models.ErrorInternal):
		return models.ErrorInternal
	default:
		return models.ErrorExternalFailure
	}
}

func isRetryableRuntimeCode(code string) bool {
	switch strings.TrimSpace(code) {
	case string(models.ErrorTimeout), string(models.ErrorExternalFailure), string(models.ErrorResourceLimit):
		return true
	default:
		return false
	}
}

func cloneJSON(body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	cp := make([]byte, len(body))
	copy(cp, body)
	return cp
}
