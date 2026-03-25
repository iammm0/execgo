// Package api 提供 ExecGo 的 HTTP API 层
// provides the HTTP API layer for ExecGo.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/iammm0/execgo/internal/executor"
	"github.com/iammm0/execgo/internal/models"
	"github.com/iammm0/execgo/internal/observability"
	"github.com/iammm0/execgo/internal/scheduler"
	"github.com/iammm0/execgo/internal/state"
)

// Server HTTP API 服务器 / HTTP API server.
type Server struct {
	state     *state.Manager
	scheduler *scheduler.Scheduler
	metrics   *observability.Metrics
	logger    *slog.Logger
	startTime time.Time
}

// NewServer 创建 API 服务器 / creates an API server.
func NewServer(sm *state.Manager, sched *scheduler.Scheduler, metrics *observability.Metrics, logger *slog.Logger) *Server {
	return &Server{
		state:     sm,
		scheduler: sched,
		metrics:   metrics,
		logger:    logger,
		startTime: time.Now(),
	}
}

// Handler 返回配置好的 HTTP 路由 / returns the configured HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /tasks", s.handleSubmitTasks)
	mux.HandleFunc("GET /tasks/{id}", s.handleGetTask)
	mux.HandleFunc("GET /tasks", s.handleListTasks)
	mux.HandleFunc("DELETE /tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// 用追踪中间件包装 / wrap with trace middleware
	return observability.TraceMiddleware(mux)
}

// ----------------------------------------------------------------
// 路由处理器 / Route Handlers
// ----------------------------------------------------------------

// handleSubmitTasks 提交任务图 / submits a task graph.
func (s *Server) handleSubmitTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := observability.L(ctx, s.logger)

	var graph models.TaskGraph
	if err := json.NewDecoder(r.Body).Decode(&graph); err != nil {
		logger.Warn("invalid request body", "error", err)
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	if err := graph.Validate(); err != nil {
		logger.Warn("task graph validation failed", "error", err)
		writeJSON(w, http.StatusBadRequest, models.ErrorResponse{Error: err.Error()})
		return
	}

	// 检查所有任务类型是否有对应执行器 / verify executors exist for all task types
	for _, task := range graph.Tasks {
		if _, err := executor.Get(task.Type); err != nil {
			logger.Warn("unknown task type", "type", task.Type)
			writeJSON(w, http.StatusBadRequest, models.ErrorResponse{
				Error: "unknown task type: " + task.Type + " (available: " + strings.Join(executor.RegisteredTypes(), ", ") + ")",
			})
			return
		}
	}

	s.scheduler.Submit(&graph)
	logger.Info("task graph submitted", "task_count", len(graph.Tasks))

	ids := make([]string, len(graph.Tasks))
	for i, t := range graph.Tasks {
		ids[i] = t.ID
	}

	writeJSON(w, http.StatusAccepted, models.SubmitResponse{
		Accepted: len(graph.Tasks),
		TaskIDs:  ids,
	})
}

// handleGetTask 获取单个任务 / retrieves a single task.
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task, ok := s.state.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "task not found: " + id})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleListTasks 列出所有任务 / lists all tasks.
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.state.GetAll()
	writeJSON(w, http.StatusOK, tasks)
}

// handleDeleteTask 删除任务 / deletes a task.
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.state.Delete(id) {
		writeJSON(w, http.StatusNotFound, models.ErrorResponse{Error: "task not found: " + id})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHealth 健康检查 / health check.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, models.HealthResponse{
		Status:  "ok",
		Version: "v0.1.0",
		Uptime:  time.Since(s.startTime).Round(time.Second).String(),
	})
}

// handleMetrics 指标端点 / metrics endpoint.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, models.MetricsResponse{
		TasksTotal:     s.metrics.TasksTotal.Load(),
		TasksRunning:   s.metrics.TasksRunning.Load(),
		TasksSucceeded: s.metrics.TasksSucceeded.Load(),
		TasksFailed:    s.metrics.TasksFailed.Load(),
		ByType:         s.metrics.Snapshot(),
	})
}

// ----------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
