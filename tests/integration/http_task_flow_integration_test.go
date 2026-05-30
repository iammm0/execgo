package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/taskqueue"
	"github.com/iammm0/execgo/tests/testutil"
)

func TestHTTPTaskFlow_SubmitThenQueryStatus(t *testing.T) {
	executor.RegisterBuiltins()
	rt := testutil.NewRuntime(t, 4)
	srv := testutil.NewHTTPServer(t, rt)
	client := srv.Client()

	healthResp, err := client.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health status=%d want=%d", healthResp.StatusCode, http.StatusOK)
	}
	_ = healthResp.Body.Close()

	payload := map[string]any{
		"tasks": []map[string]any{
			{"id": "first", "type": "noop", "params": map[string]any{"message": "hi"}, "required_capabilities": map[string]any{"sandbox": "local"}},
			{"id": "second", "type": "noop", "depends_on": []string{"first"}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/tasks", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /tasks error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /tasks status=%d want=%d", resp.StatusCode, http.StatusAccepted)
	}

	var submit models.SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submit); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submit.Accepted != 2 {
		t.Fatalf("accepted=%d want=2", submit.Accepted)
	}

	task := pollTaskByHTTP(t, client, srv.URL, "second", 5*time.Second)
	if task.Status != models.StatusSuccess {
		t.Fatalf("task second status=%s error=%s", task.Status, task.Error)
	}
	first := pollTaskByHTTP(t, client, srv.URL, "first", time.Second)
	if first.RequiredCapabilities["sandbox"] != "local" {
		t.Fatalf("required_capabilities=%v want sandbox=local", first.RequiredCapabilities)
	}
	if task.Runtime == nil {
		t.Fatal("expected runtime envelope in HTTP task payload")
	}
	if task.Runtime.Status != models.RuntimeSuccess {
		t.Fatalf("expected runtime success, got %s", task.Runtime.Status)
	}
	if task.RunStatus != string(models.RuntimeSuccess) {
		t.Fatalf("expected run_status=%q, got %q", models.RuntimeSuccess, task.RunStatus)
	}
	if len(task.Result) == 0 {
		t.Fatal("expected legacy result field to remain populated for compatibility")
	}

	workersResp, err := client.Get(srv.URL + "/workers")
	if err != nil {
		t.Fatalf("GET /workers error: %v", err)
	}
	defer workersResp.Body.Close()
	if workersResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /workers status=%d want=%d", workersResp.StatusCode, http.StatusOK)
	}
	var workersPayload struct {
		Workers []models.WorkerNode `json:"workers"`
	}
	if err := json.NewDecoder(workersResp.Body).Decode(&workersPayload); err != nil {
		t.Fatalf("decode workers response: %v", err)
	}
	if len(workersPayload.Workers) == 0 {
		t.Fatal("expected /workers to include the local worker")
	}
	if workersPayload.Workers[0].Capabilities["executor"] == "" {
		t.Fatalf("expected worker executor capability, got %+v", workersPayload.Workers[0].Capabilities)
	}

	eventsResp, err := client.Get(srv.URL + "/events?limit=10")
	if err != nil {
		t.Fatalf("GET /events error: %v", err)
	}
	defer eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events status=%d want=%d", eventsResp.StatusCode, http.StatusOK)
	}
	var eventsPayload struct {
		Events []models.RuntimeEvent `json:"events"`
	}
	if err := json.NewDecoder(eventsResp.Body).Decode(&eventsPayload); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if len(eventsPayload.Events) == 0 {
		t.Fatal("expected /events to expose runtime events")
	}

	deadMessageID := seedDeadQueueMessage(t, rt.Scheduler.Queue())
	queueResp, err := client.Get(srv.URL + "/queue")
	if err != nil {
		t.Fatalf("GET /queue error: %v", err)
	}
	defer queueResp.Body.Close()
	if queueResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /queue status=%d want=%d", queueResp.StatusCode, http.StatusOK)
	}
	var queueDepth struct {
		Ready   int64 `json:"ready"`
		Delayed int64 `json:"delayed"`
		Dead    int64 `json:"dead"`
	}
	if err := json.NewDecoder(queueResp.Body).Decode(&queueDepth); err != nil {
		t.Fatalf("decode queue depth: %v", err)
	}
	if queueDepth.Dead == 0 {
		t.Fatalf("expected dead queue depth > 0, got %+v", queueDepth)
	}

	deadResp, err := client.Get(srv.URL + "/queue/dead?limit=10")
	if err != nil {
		t.Fatalf("GET /queue/dead error: %v", err)
	}
	defer deadResp.Body.Close()
	if deadResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /queue/dead status=%d want=%d", deadResp.StatusCode, http.StatusOK)
	}
	var deadPayload struct {
		Messages []taskqueue.Message `json:"messages"`
	}
	if err := json.NewDecoder(deadResp.Body).Decode(&deadPayload); err != nil {
		t.Fatalf("decode dead queue: %v", err)
	}
	if len(deadPayload.Messages) == 0 || deadPayload.Messages[0].MessageID != deadMessageID {
		t.Fatalf("expected seeded dead message %q, got %+v", deadMessageID, deadPayload.Messages)
	}

	requeueBody, err := json.Marshal(map[string]any{"message_id": deadMessageID, "delay_ms": 0})
	if err != nil {
		t.Fatalf("marshal requeue body: %v", err)
	}
	requeueResp, err := client.Post(srv.URL+"/queue/dead/requeue", "application/json", bytes.NewReader(requeueBody))
	if err != nil {
		t.Fatalf("POST /queue/dead/requeue error: %v", err)
	}
	defer requeueResp.Body.Close()
	if requeueResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /queue/dead/requeue status=%d want=%d", requeueResp.StatusCode, http.StatusAccepted)
	}
}

func TestMCPHTTPFlow_ListCallPoll(t *testing.T) {
	executor.RegisterBuiltins()
	rt := testutil.NewRuntime(t, 2)
	srv := testutil.NewHTTPServer(t, rt)
	client := srv.Client()

	resp, err := client.Get(srv.URL + "/mcp/tools")
	if err != nil {
		t.Fatalf("GET /mcp/tools error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mcp/tools status=%d", resp.StatusCode)
	}
	var list map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()

	body := []byte(`{"id":"mcp-1","tool_name":"demo.echo","input":{"hello":"world"}}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/call", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	callResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/call error: %v", err)
	}
	if callResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /mcp/call status=%d", callResp.StatusCode)
	}
	var accepted map[string]any
	_ = json.NewDecoder(callResp.Body).Decode(&accepted)
	_ = callResp.Body.Close()

	handle, _ := accepted["handle_id"].(string)
	if handle == "" {
		t.Fatalf("expected handle_id")
	}
	time.Sleep(100 * time.Millisecond)
	pollResp, err := client.Get(srv.URL + "/mcp/tasks/" + handle)
	if err != nil {
		t.Fatalf("GET /mcp/tasks/{id} error: %v", err)
	}
	if pollResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mcp/tasks/{id} status=%d", pollResp.StatusCode)
	}
	var result map[string]any
	_ = json.NewDecoder(pollResp.Body).Decode(&result)
	_ = pollResp.Body.Close()
	if result["status"] != "success" && result["status"] != "running" && result["status"] != "accepted" {
		t.Fatalf("unexpected mcp task status: %v", result["status"])
	}
}

func TestQueueOpsEndpoints_DeadLetterRequeue(t *testing.T) {
	executor.RegisterBuiltins()
	rt := testutil.NewRuntime(t, 1)
	rt.Worker.Stop()
	srv := testutil.NewHTTPServer(t, rt)
	client := srv.Client()
	ctx := t.Context()

	q := rt.Scheduler.Queue()
	if err := q.Enqueue(ctx, "dead-http-task", 5, 1); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	msg, err := q.Poll(ctx, "ops-worker", time.Second)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if msg == nil {
		t.Fatal("expected queued message")
	}
	if err := q.Nack(ctx, "ops-worker", msg.MessageID, time.Time{}, true); err != nil {
		t.Fatalf("dead-letter nack: %v", err)
	}

	depthResp, err := client.Get(srv.URL + "/queue")
	if err != nil {
		t.Fatalf("GET /queue error: %v", err)
	}
	defer depthResp.Body.Close()
	if depthResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /queue status=%d want=%d", depthResp.StatusCode, http.StatusOK)
	}
	var depth map[string]int64
	if err := json.NewDecoder(depthResp.Body).Decode(&depth); err != nil {
		t.Fatalf("decode queue depth: %v", err)
	}
	if depth["dead"] != 1 {
		t.Fatalf("dead depth=%d want 1", depth["dead"])
	}

	deadResp, err := client.Get(srv.URL + "/queue/dead?limit=10")
	if err != nil {
		t.Fatalf("GET /queue/dead error: %v", err)
	}
	defer deadResp.Body.Close()
	if deadResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /queue/dead status=%d want=%d", deadResp.StatusCode, http.StatusOK)
	}
	var deadPayload struct {
		Messages []struct {
			MessageID string `json:"message_id"`
			TaskID    string `json:"task_id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(deadResp.Body).Decode(&deadPayload); err != nil {
		t.Fatalf("decode dead messages: %v", err)
	}
	if len(deadPayload.Messages) != 1 || deadPayload.Messages[0].TaskID != "dead-http-task" {
		t.Fatalf("unexpected dead payload: %+v", deadPayload)
	}

	requeueBody, err := json.Marshal(map[string]any{
		"message_id": deadPayload.Messages[0].MessageID,
		"delay_ms":   0,
	})
	if err != nil {
		t.Fatalf("marshal requeue: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/queue/dead/requeue", bytes.NewReader(requeueBody))
	if err != nil {
		t.Fatalf("new requeue request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	requeueResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /queue/dead/requeue error: %v", err)
	}
	defer requeueResp.Body.Close()
	if requeueResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /queue/dead/requeue status=%d want=%d", requeueResp.StatusCode, http.StatusAccepted)
	}

	ready, _, dead, err := q.Depth(ctx)
	if err != nil {
		t.Fatalf("depth after requeue: %v", err)
	}
	if ready != 1 || dead != 0 {
		t.Fatalf("after requeue ready=%d dead=%d want 1/0", ready, dead)
	}
}

func TestHTTPCancelEndpoint_StatusCodes(t *testing.T) {
	executor.RegisterBuiltins()
	rt := testutil.NewRuntime(t, 1)
	rt.Worker.Stop()
	srv := testutil.NewHTTPServer(t, rt)
	client := srv.Client()

	rt.Scheduler.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "http-cancel-ready", Type: "noop"}}})
	resp := putCancel(t, client, srv.URL, "http-cancel-ready", `{"reason":"client requested"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("first cancel status=%d want %d", resp.StatusCode, http.StatusAccepted)
	}
	var accepted struct {
		Cancelled      bool   `json:"cancelled"`
		Status         string `json:"status"`
		PreviousStatus string `json:"previous_status"`
		Reason         string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	_ = resp.Body.Close()
	if !accepted.Cancelled || accepted.Status != string(models.StatusCancelled) || accepted.PreviousStatus != string(models.StatusReady) || accepted.Reason != "client requested" {
		t.Fatalf("unexpected cancel response: %+v", accepted)
	}

	resp = putCancel(t, client, srv.URL, "http-cancel-ready", ``)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second cancel status=%d want %d", resp.StatusCode, http.StatusOK)
	}
	_ = resp.Body.Close()

	resp = putCancel(t, client, srv.URL, "http-missing", ``)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing cancel status=%d want %d", resp.StatusCode, http.StatusNotFound)
	}
	_ = resp.Body.Close()

	rt.Scheduler.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "http-terminal", Type: "noop"}}})
	rt.Scheduler.OnTaskLeased("http-terminal", "manual", time.Now().UTC().Add(time.Second), 1)
	rt.Scheduler.OnTaskStarted("http-terminal", "manual", 1)
	rt.Scheduler.OnTaskSucceeded("http-terminal", "manual", json.RawMessage(`{"ok":true}`), 1, "")
	resp = putCancel(t, client, srv.URL, "http-terminal", ``)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("terminal cancel status=%d want %d", resp.StatusCode, http.StatusConflict)
	}
	_ = resp.Body.Close()
}

func TestHTTPCancelEndpoint_RunningTask(t *testing.T) {
	executor.RegisterBuiltins()
	rt := testutil.NewRuntime(t, 1)
	srv := testutil.NewHTTPServer(t, rt)
	client := srv.Client()

	payload := map[string]any{
		"tasks": []map[string]any{
			{"id": "http-running-cancel", "type": "os", "tool_name": "sleep", "params": map[string]any{"duration_ms": 5000}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := client.Post(srv.URL+"/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /tasks error: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /tasks status=%d want %d", resp.StatusCode, http.StatusAccepted)
	}
	_ = resp.Body.Close()

	waitTaskStatusByHTTP(t, client, srv.URL, "http-running-cancel", models.StatusRunning, 2*time.Second)
	cancelResp := putCancel(t, client, srv.URL, "http-running-cancel", `{"reason":"stop now"}`)
	if cancelResp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel running status=%d want %d", cancelResp.StatusCode, http.StatusAccepted)
	}
	_ = cancelResp.Body.Close()

	task := waitTaskStatusByHTTP(t, client, srv.URL, "http-running-cancel", models.StatusCancelled, 2*time.Second)
	if task.RunStatus != string(models.RuntimeCancelled) {
		t.Fatalf("run_status=%q want %q", task.RunStatus, models.RuntimeCancelled)
	}
	if task.Runtime == nil || task.Runtime.Error == nil || task.Runtime.Error.Code != models.ErrorCancelled {
		t.Fatalf("runtime=%+v want cancelled error", task.Runtime)
	}
}

func pollTaskByHTTP(t *testing.T, client *http.Client, baseURL, taskID string, timeout time.Duration) *models.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/tasks/" + taskID)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		var task models.Task
		_ = json.NewDecoder(resp.Body).Decode(&task)
		_ = resp.Body.Close()
		if task.Status.IsTerminal() {
			return &task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s not terminal within %v", taskID, timeout)
	return nil
}

func waitTaskStatusByHTTP(t *testing.T, client *http.Client, baseURL, taskID string, status models.TaskStatus, timeout time.Duration) *models.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/tasks/" + taskID)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		var task models.Task
		_ = json.NewDecoder(resp.Body).Decode(&task)
		_ = resp.Body.Close()
		if task.Status == status {
			return &task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach status %s within %v", taskID, status, timeout)
	return nil
}

func putCancel(t *testing.T, client *http.Client, baseURL, taskID, body string) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/tasks/"+taskID+"/cancel", reader)
	if err != nil {
		t.Fatalf("new cancel request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT cancel error: %v", err)
	}
	return resp
}

func seedDeadQueueMessage(t *testing.T, q taskqueue.Queue) string {
	t.Helper()
	ctx := context.Background()
	if err := q.Enqueue(ctx, "dead-http", 5, 1); err != nil {
		t.Fatalf("enqueue dead seed: %v", err)
	}
	msg, err := q.Poll(ctx, "dead-seed-worker", time.Second)
	if err != nil {
		t.Fatalf("poll dead seed: %v", err)
	}
	if msg == nil {
		t.Fatal("expected seed queue message")
	}
	if err := q.Nack(ctx, "dead-seed-worker", msg.MessageID, time.Time{}, true); err != nil {
		t.Fatalf("dead-letter seed: %v", err)
	}
	return msg.MessageID
}
