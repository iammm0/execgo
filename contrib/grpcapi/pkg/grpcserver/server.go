package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	execgov1 "github.com/iammm0/execgo/contrib/grpcapi/pkg/pb/proto/execgo/v1"
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"
	execgoversion "github.com/iammm0/execgo/pkg/version"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements ExecGo gRPC methods by mapping them to the existing core HTTP semantics.
type Server struct {
	execgov1.UnimplementedExecGoServer

	state   store.Store
	sched   *scheduler.Scheduler
	metrics *observability.Metrics
	logger  *slog.Logger

	startTime time.Time
}

// NewServer creates a gRPC server implementation.
func NewServer(st store.Store, sched *scheduler.Scheduler, metrics *observability.Metrics, logger *slog.Logger) *Server {
	return &Server{
		state:     st,
		sched:     sched,
		metrics:   metrics,
		logger:    logger,
		startTime: time.Now(),
	}
}

func taskToProto(t *models.Task) *execgov1.Task {
	var paramsJSON string
	if len(t.Params) > 0 {
		paramsJSON = string(t.Params)
	}
	var inputJSON string
	if len(t.Input) > 0 {
		inputJSON = string(t.Input)
	}
	var resultJSON string
	if len(t.Result) > 0 {
		resultJSON = string(t.Result)
	}
	var scheduledAtUnixMS int64
	if t.ScheduledAt != nil {
		scheduledAtUnixMS = t.ScheduledAt.UnixMilli()
	}
	return &execgov1.Task{
		Id:                   t.ID,
		Type:                 t.Type,
		ParamsJson:           paramsJSON,
		DependsOn:            t.DependsOn,
		Retry:                int32(t.Retry),
		TimeoutMs:            t.Timeout,
		Status:               string(t.Status),
		ResultJson:           resultJSON,
		Error:                t.Error,
		CreatedAtUnixMs:      t.CreatedAt.UnixMilli(),
		UpdatedAtUnixMs:      t.UpdatedAt.UnixMilli(),
		ToolName:             t.ToolName,
		InputJson:            inputJSON,
		ExecutionCategory:    t.Category,
		Priority:             int32(t.Priority),
		ScheduledAtUnixMs:    scheduledAtUnixMS,
		Version:              t.Version,
		WorkflowId:           t.WorkflowID,
		Attempt:              int32(t.Attempt),
		HandleId:             t.HandleID,
		RunStatus:            t.RunStatus,
		RequiredCapabilities: copyStringMap(t.RequiredCapabilities),
	}
}

func taskFromProto(t *execgov1.Task) *models.Task {
	var params json.RawMessage
	if t.GetParamsJson() != "" {
		params = json.RawMessage(t.ParamsJson)
	}
	var input json.RawMessage
	if t.GetInputJson() != "" {
		input = json.RawMessage(t.InputJson)
	}
	var scheduledAt *time.Time
	if t.GetScheduledAtUnixMs() > 0 {
		ts := time.UnixMilli(t.GetScheduledAtUnixMs()).UTC()
		scheduledAt = &ts
	}

	return &models.Task{
		ID:                   t.GetId(),
		WorkflowID:           t.GetWorkflowId(),
		Type:                 t.GetType(),
		Params:               params,
		ToolName:             t.GetToolName(),
		Input:                input,
		Category:             t.GetExecutionCategory(),
		DependsOn:            t.GetDependsOn(),
		Retry:                int(t.GetRetry()),
		Priority:             int(t.GetPriority()),
		ScheduledAt:          scheduledAt,
		Timeout:              t.GetTimeoutMs(),
		Version:              t.GetVersion(),
		Attempt:              int(t.GetAttempt()),
		HandleID:             t.GetHandleId(),
		RunStatus:            t.GetRunStatus(),
		RequiredCapabilities: copyStringMap(t.GetRequiredCapabilities()),
		// Status/CreatedAt/UpdatedAt are assigned by scheduler.Submit and persistence layer.
		Status: models.StatusPending,
	}
}

func (s *Server) SubmitTasks(ctx context.Context, req *execgov1.TaskGraph) (*execgov1.SubmitTasksResponse, error) {
	graph := &models.TaskGraph{}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "task graph is required")
	}

	graph.Tasks = make([]*models.Task, 0, len(req.GetTasks()))
	for _, pt := range req.GetTasks() {
		graph.Tasks = append(graph.Tasks, taskFromProto(pt))
	}

	if err := graph.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Validate executor types early (same behavior as HTTP layer).
	for _, task := range graph.Tasks {
		executor.NormalizeTask(task)
		if _, err := executor.Get(task.Type); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "unknown task type %q (available: %v)", task.Type, executor.RegisteredTypes())
		}
	}

	s.sched.SubmitWithContext(ctx, graph)

	ids := make([]string, 0, len(graph.Tasks))
	for _, t := range graph.Tasks {
		ids = append(ids, t.ID)
	}

	return &execgov1.SubmitTasksResponse{
		Accepted: int32(len(graph.Tasks)),
		TaskIds:  ids,
	}, nil
}

func (s *Server) GetTask(ctx context.Context, req *execgov1.GetTaskRequest) (*execgov1.GetTaskResponse, error) {
	if req == nil || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	task, ok := s.state.Get(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "task not found: %s", req.GetId())
	}

	return &execgov1.GetTaskResponse{Task: taskToProto(task)}, nil
}

func (s *Server) ListTasks(ctx context.Context, req *execgov1.ListTasksRequest) (*execgov1.ListTasksResponse, error) {
	_ = req // no filters for now
	tasks := s.state.GetAll()
	out := &execgov1.ListTasksResponse{Tasks: make([]*execgov1.Task, 0, len(tasks))}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, taskToProto(t))
	}
	return out, nil
}

func (s *Server) DeleteTask(ctx context.Context, req *execgov1.DeleteTaskRequest) (*execgov1.DeleteTaskResponse, error) {
	if req == nil || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	if !s.state.Delete(req.GetId()) {
		return nil, status.Errorf(codes.NotFound, "task not found: %s", req.GetId())
	}

	return &execgov1.DeleteTaskResponse{Deleted: true}, nil
}

func (s *Server) CancelTask(ctx context.Context, req *execgov1.CancelTaskRequest) (*execgov1.CancelTaskResponse, error) {
	if req == nil || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	result, err := s.sched.CancelTask(ctx, req.GetId(), req.GetReason(), "grpc")
	if err != nil {
		switch {
		case errors.Is(err, scheduler.ErrTaskNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, scheduler.ErrTaskTerminal):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return &execgov1.CancelTaskResponse{
		Cancelled:      result.Cancelled,
		TaskId:         result.TaskID,
		Status:         string(result.Status),
		PreviousStatus: string(result.PreviousStatus),
		Reason:         result.Reason,
	}, nil
}

func (s *Server) Health(ctx context.Context, req *execgov1.HealthRequest) (*execgov1.HealthResponse, error) {
	_ = req
	return &execgov1.HealthResponse{
		Status:  "ok",
		Version: execgoversion.Current,
		Uptime:  time.Since(s.startTime).Round(time.Second).String(),
	}, nil
}

func (s *Server) Metrics(ctx context.Context, req *execgov1.MetricsRequest) (*execgov1.MetricsResponse, error) {
	_ = req
	return &execgov1.MetricsResponse{
		TasksTotal:                   s.metrics.TasksTotal.Load(),
		TasksRunning:                 s.metrics.TasksRunning.Load(),
		TasksSucceeded:               s.metrics.TasksSucceeded.Load(),
		TasksFailed:                  s.metrics.TasksFailed.Load(),
		TasksCancelled:               s.metrics.TasksCancelled.Load(),
		DispatchCapabilityMismatches: s.metrics.DispatchCapabilityMismatches.Load(),
		ByType:                       s.metrics.Snapshot(),
	}, nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
