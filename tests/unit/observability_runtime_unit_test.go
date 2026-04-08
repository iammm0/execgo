package unit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iammm0/execgo/pkg/httpserver"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/tests/testutil"
)

func TestObservabilityMiddlewareInjectsTraceID(t *testing.T) {
	metrics := observability.NewMetrics()
	rt, err := observability.InitRuntime(context.Background(), observability.RuntimeConfig{
		ServiceName: "execgo-test",
	}, metrics, nil)
	if err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	defer func() { _ = rt.Shutdown(context.Background()) }()

	handler := rt.HTTPMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if traceID := observability.TraceIDFromContext(r.Context()); traceID == "" {
			t.Fatalf("trace id not found in request context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", resp.Code)
	}
	if got := resp.Header().Get("X-Trace-ID"); got == "" {
		t.Fatalf("expected X-Trace-ID header")
	}
}

func TestPrometheusEndpointCompatibility(t *testing.T) {
	base := testutil.NewRuntime(t, 1)
	obs, err := observability.InitRuntime(context.Background(), observability.RuntimeConfig{
		ServiceName: "execgo-test",
	}, base.Metrics, base.Logger)
	if err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	defer func() { _ = obs.Shutdown(context.Background()) }()

	engine := httpserver.NewEngine(base.Store, base.Scheduler, base.Metrics, base.Logger).
		DisableTrace().
		Use(obs.HTTPMiddleware()).
		WithPrometheusHandler(obs.PrometheusHandler())
	server := httptest.NewServer(engine.Handler())
	defer server.Close()

	base.Metrics.TasksTotal.Add(2)
	base.Metrics.TasksSucceeded.Add(1)
	base.Metrics.IncType("noop")

	res, err := http.Get(server.URL + "/metrics/prometheus")
	if err != nil {
		t.Fatalf("get prometheus endpoint: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d, body=%s", res.StatusCode, string(body))
	}
	content := string(body)
	if !strings.Contains(content, "execgo_tasks_total") {
		t.Fatalf("expected execgo_tasks_total metric, got body=%s", content)
	}
	if !strings.Contains(content, "task_type=\"noop\"") {
		t.Fatalf("expected task_type label for noop metric, got body=%s", content)
	}
}

func TestPrometheusEndpointNotConfigured(t *testing.T) {
	base := testutil.NewRuntime(t, 1)
	engine := httpserver.NewEngine(base.Store, base.Scheduler, base.Metrics, base.Logger)
	server := httptest.NewServer(engine.Handler())
	defer server.Close()

	res, err := http.Get(server.URL + "/metrics/prometheus")
	if err != nil {
		t.Fatalf("get prometheus endpoint: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501 when prometheus handler is absent, got %d", res.StatusCode)
	}
}
