// Tests for RuntimeExecutor / RuntimeExecutor 测试。
// Author: iammm0; Last edited: 2026-04-23
package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iammm0/execgo/pkg/models"
)

// TestRuntimeExecutorExecuteInjectsTaskIDAndReturnsAcceptedHandle verifies submit payload injection and handle semantics / 验证提交时 task_id 注入与 handle 语义。
func TestRuntimeExecutorExecuteInjectsTaskIDAndReturnsAcceptedHandle(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id":   "runtime-task",
			"handle_id": "runtime-task",
			"status":    "accepted",
		})
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	res, err := exec.Execute(context.Background(), &models.Task{
		ID:    "runtime-task",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"echo","args":["ok"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if res.Status != models.RuntimeAccepted {
		t.Fatalf("expected status=%s, got %s", models.RuntimeAccepted, res.Status)
	}
	if res.HandleID != "runtime-task" {
		t.Fatalf("expected handle_id=runtime-task, got %s", res.HandleID)
	}
	if got["task_id"] != "runtime-task" {
		t.Fatalf("expected injected task_id, got %#v", got["task_id"])
	}
}

// TestRuntimeExecutorGetHandleMapsStructuredRuntimeError verifies structured error mapping / 验证结构化错误映射。
func TestRuntimeExecutorGetHandleMapsStructuredRuntimeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks/task-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id":     "task-1",
			"handle_id":   "task-1",
			"status":      "failed",
			"duration_ms": 123,
			"error": map[string]any{
				"code":    "timeout",
				"message": "task exceeded wall_time_ms",
			},
		})
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	res, ok := exec.GetHandle("task-1")
	if !ok {
		t.Fatal("expected handle lookup to succeed")
	}
	if res.Status != models.RuntimeFailed {
		t.Fatalf("expected status=%s, got %s", models.RuntimeFailed, res.Status)
	}
	if res.Error == nil {
		t.Fatal("expected structured runtime error")
	}
	if res.Error.Code != models.ErrorTimeout {
		t.Fatalf("expected error code=%s, got %s", models.ErrorTimeout, res.Error.Code)
	}
	if res.DurationMS != 123 {
		t.Fatalf("expected duration_ms=123, got %d", res.DurationMS)
	}
}

// TestRuntimeExecutorGetHandleFallsBackToTaskIDWhenHandleNotFound verifies handle->task_id fallback / 验证 handle 查询失败时回退 task_id。
func TestRuntimeExecutorGetHandleFallsBackToTaskIDWhenHandleNotFound(t *testing.T) {
	var (
		seenSubmit bool
		seenHandle bool
		seenTask   bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			seenSubmit = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "task-1",
				"handle_id": "handle-1",
				"status":    "accepted",
			})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/handle-1":
			seenHandle = true
			http.NotFound(w, r)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/task-1":
			seenTask = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "task-1",
				"handle_id": "handle-1",
				"status":    "running",
			})
			return
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "client-task",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"echo","args":["ok"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	res, ok := exec.GetHandle("handle-1")
	if !ok {
		t.Fatal("expected handle lookup to succeed via task_id fallback")
	}
	if res.Status != models.RuntimeRunning {
		t.Fatalf("expected status=%s, got %s", models.RuntimeRunning, res.Status)
	}
	if !seenSubmit || !seenHandle || !seenTask {
		t.Fatalf("expected submit+handle404+taskFallback, got submit=%v handle=%v task=%v", seenSubmit, seenHandle, seenTask)
	}
}

// TestRuntimeExecutorCancelHandleFallsBackToTaskIDWhenHandleNotFound verifies cancel fallback / 验证取消操作的 task_id 回退。
func TestRuntimeExecutorCancelHandleFallsBackToTaskIDWhenHandleNotFound(t *testing.T) {
	var (
		seenSubmit bool
		seenKillH  bool
		seenKillT  bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			seenSubmit = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "task-1",
				"handle_id": "handle-1",
				"status":    "accepted",
			})
			return
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks/handle-1/kill":
			seenKillH = true
			http.NotFound(w, r)
			return
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks/task-1/kill":
			seenKillT = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "task-1",
				"handle_id": "handle-1",
				"status":    "cancelled",
			})
			return
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "client-task",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"sleep","args":["1"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	res, ok := exec.CancelHandle("handle-1")
	if !ok {
		t.Fatal("expected cancel to succeed via task_id fallback")
	}
	if res.Status != models.RuntimeCancelled {
		t.Fatalf("expected status=%s, got %s", models.RuntimeCancelled, res.Status)
	}
	if !seenSubmit || !seenKillH || !seenKillT {
		t.Fatalf("expected submit+kill404+killFallback, got submit=%v killH=%v killT=%v", seenSubmit, seenKillH, seenKillT)
	}
}

// TestRuntimeExecutorGetEventsFallsBackToTaskIDWhenHandleNotFound verifies events fallback / 验证事件查询的 task_id 回退。
func TestRuntimeExecutorGetEventsFallsBackToTaskIDWhenHandleNotFound(t *testing.T) {
	var (
		seenSubmit  bool
		seenEventsH bool
		seenEventsT bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			seenSubmit = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "task-1",
				"handle_id": "handle-1",
				"status":    "accepted",
			})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/handle-1/events":
			seenEventsH = true
			http.NotFound(w, r)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/task-1/events":
			seenEventsT = true
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"type":      "task_started",
					"task_id":   "task-1",
					"handle_id": "handle-1",
					"timestamp": "2026-04-21T00:00:00Z",
					"message":   "started",
					"data":      map[string]any{"k": "v"},
				},
			})
			return
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "client-task",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"echo","args":["ok"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}

	evs, ok := exec.GetEvents("handle-1")
	if !ok {
		t.Fatal("expected events lookup to succeed via task_id fallback")
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Type != models.RuntimeEventStarted {
		t.Fatalf("expected event type=%s, got %s", models.RuntimeEventStarted, evs[0].Type)
	}
	if !seenSubmit || !seenEventsH || !seenEventsT {
		t.Fatalf("expected submit+events404+eventsFallback, got submit=%v eventsH=%v eventsT=%v", seenSubmit, seenEventsH, seenEventsT)
	}
}

// TestRuntimeExecutorIntrospectionEndpoints verifies runtime introspection endpoints / 验证 runtime 自省端点。
func TestRuntimeExecutorIntrospectionEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/runtime/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "rt-1"})
		case "/api/v1/runtime/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"mode": "adaptive"})
		case "/api/v1/runtime/resources":
			_ = json.NewEncoder(w).Encode(map[string]any{"cpu": map[string]any{"available": 1}})
		case "/api/v1/runtime/config":
			_ = json.NewEncoder(w).Encode(map[string]any{"queue": map[string]any{"max": 100}})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	ctx := context.Background()

	if raw, err := exec.GetRuntimeInfo(ctx); err != nil || len(raw) == 0 {
		t.Fatalf("GetRuntimeInfo err=%v raw=%s", err, string(raw))
	}
	if raw, err := exec.GetRuntimeCapabilities(ctx); err != nil || len(raw) == 0 {
		t.Fatalf("GetRuntimeCapabilities err=%v raw=%s", err, string(raw))
	}
	if raw, err := exec.GetRuntimeResources(ctx); err != nil || len(raw) == 0 {
		t.Fatalf("GetRuntimeResources err=%v raw=%s", err, string(raw))
	}
	if raw, err := exec.GetRuntimeConfig(ctx); err != nil || len(raw) == 0 {
		t.Fatalf("GetRuntimeConfig err=%v raw=%s", err, string(raw))
	}
}

// TestRuntimeExecutorSubmitInjectsControlContextTenantOwner verifies that tenant/owner are injected into control_context /
// 验证 tenant/owner 被注入到 control_context。
func TestRuntimeExecutorSubmitInjectsControlContextTenantOwner(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id":   "t1",
			"handle_id": "t1",
			"status":    "accepted",
		})
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "acme", "alice")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "t1",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"echo","args":["hi"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	cc, ok := got["control_context"].(map[string]any)
	if !ok {
		t.Fatalf("expected control_context in payload, got %#v", got["control_context"])
	}
	if cc["tenant"] != "acme" {
		t.Fatalf("expected tenant=acme, got %#v", cc["tenant"])
	}
	if cc["owner"] != "alice" {
		t.Fatalf("expected owner=alice, got %#v", cc["owner"])
	}
}

// TestRuntimeExecutorSubmitPreservesExistingControlContext verifies that task-supplied control_context values are not overwritten /
// 验证任务已有的 control_context 字段不会被覆盖。
func TestRuntimeExecutorSubmitPreservesExistingControlContext(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id":   "t2",
			"handle_id": "t2",
			"status":    "accepted",
		})
	}))
	defer srv.Close()

	// task already supplies control_context.owner; executor tenant/owner should only fill missing fields.
	// 任务已提供 control_context.owner；executor 仅补全缺失字段。
	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "acme", "executor-owner")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:   "t2",
		Type: "runtime",
		Input: json.RawMessage(`{
			"execution":{"kind":"command","program":"echo","args":["hi"]},
			"control_context":{"owner":"task-owner","extra":"x"}
		}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	cc, ok := got["control_context"].(map[string]any)
	if !ok {
		t.Fatalf("expected control_context in payload, got %#v", got["control_context"])
	}
	// task-supplied owner must not be overwritten.
	if cc["owner"] != "task-owner" {
		t.Fatalf("expected owner=task-owner (task-supplied), got %#v", cc["owner"])
	}
	// tenant was absent in task payload; executor should have filled it.
	if cc["tenant"] != "acme" {
		t.Fatalf("expected tenant=acme (executor-injected), got %#v", cc["tenant"])
	}
	// extra field from task payload must be preserved.
	if cc["extra"] != "x" {
		t.Fatalf("expected extra=x preserved, got %#v", cc["extra"])
	}
}

// TestRuntimeExecutorKillCarriesOwnerHeader verifies that the kill request includes X-Execgo-Owner when owner is set /
// 验证取消请求在设置 owner 时携带 X-Execgo-Owner 头。
func TestRuntimeExecutorKillCarriesOwnerHeader(t *testing.T) {
	var killHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "t3",
				"handle_id": "t3",
				"status":    "accepted",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks/t3/kill":
			killHeader = r.Header.Get("X-Execgo-Owner")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "t3",
				"handle_id": "t3",
				"status":    "cancelled",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "alice")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "t3",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"sleep","args":["1"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	res, ok := exec.CancelHandle("t3")
	if !ok {
		t.Fatal("expected cancel to succeed")
	}
	if res.Status != models.RuntimeCancelled {
		t.Fatalf("expected status=cancelled, got %s", res.Status)
	}
	if killHeader != "alice" {
		t.Fatalf("expected X-Execgo-Owner=alice on kill request, got %q", killHeader)
	}
}

// TestRuntimeExecutorKillOmitsOwnerHeaderWhenUnset verifies that no X-Execgo-Owner header is sent when owner is empty /
// 验证 owner 为空时取消请求不携带 X-Execgo-Owner 头。
func TestRuntimeExecutorKillOmitsOwnerHeaderWhenUnset(t *testing.T) {
	var killHeader string
	var killHeaderPresent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "t4",
				"handle_id": "t4",
				"status":    "accepted",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks/t4/kill":
			_, killHeaderPresent = r.Header["X-Execgo-Owner"]
			killHeader = r.Header.Get("X-Execgo-Owner")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id":   "t4",
				"handle_id": "t4",
				"status":    "cancelled",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	exec := NewRuntimeExecutor(srv.URL, srv.Client(), "", "")
	_, err := exec.Execute(context.Background(), &models.Task{
		ID:    "t4",
		Type:  "runtime",
		Input: json.RawMessage(`{"execution":{"kind":"command","program":"sleep","args":["1"]}}`),
	})
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	_, ok := exec.CancelHandle("t4")
	if !ok {
		t.Fatal("expected cancel to succeed")
	}
	if killHeaderPresent {
		t.Fatalf("expected no X-Execgo-Owner header, but got %q", killHeader)
	}
}
