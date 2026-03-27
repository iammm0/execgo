package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
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
			{"id": "first", "type": "noop", "params": map[string]any{"message": "hi"}},
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
	if result["status"] != "success" && result["status"] != "running" {
		t.Fatalf("unexpected mcp task status: %v", result["status"])
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
		if task.Status == models.StatusSuccess || task.Status == models.StatusFailed || task.Status == models.StatusSkipped {
			return &task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s not terminal within %v", taskID, timeout)
	return nil
}

