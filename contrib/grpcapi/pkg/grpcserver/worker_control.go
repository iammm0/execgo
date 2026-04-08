package grpcserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	execgov1 "github.com/iammm0/execgo/contrib/grpcapi/pkg/pb/proto/execgo/v1"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// WorkerControlServer exposes queue-backed worker control plane RPCs.
type WorkerControlServer struct {
	execgov1.UnimplementedWorkerControlServer

	state         store.Store
	evented       store.EventBackedStore
	sched         *scheduler.Scheduler
	logger        *slog.Logger
	leaseDuration time.Duration
}

// NewWorkerControlServer creates a WorkerControl gRPC server.
func NewWorkerControlServer(st store.Store, sched *scheduler.Scheduler, logger *slog.Logger) *WorkerControlServer {
	w := &WorkerControlServer{
		state:         st,
		sched:         sched,
		logger:        logger,
		leaseDuration: 30 * time.Second,
	}
	if es, ok := st.(store.EventBackedStore); ok {
		w.evented = es
	}
	if w.logger == nil {
		w.logger = slog.Default()
	}
	return w
}

func (w *WorkerControlServer) RegisterWorker(ctx context.Context, req *execgov1.RegisterWorkerRequest) (*execgov1.RegisterWorkerResponse, error) {
	if req == nil || strings.TrimSpace(req.GetWorkerId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if w.evented == nil {
		return nil, status.Error(codes.FailedPrecondition, "worker registry unavailable on current store backend")
	}
	if err := w.evented.RegisterWorker(ctx, req.GetWorkerId(), req.GetCapabilities(), models.RuntimeEventMetadata{WorkerID: req.GetWorkerId(), Producer: "grpc-worker-control"}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &execgov1.RegisterWorkerResponse{Ok: true, ServerTimeUnixMs: time.Now().UnixMilli()}, nil
}

func (w *WorkerControlServer) Heartbeat(ctx context.Context, req *execgov1.HeartbeatRequest) (*execgov1.HeartbeatResponse, error) {
	if req == nil || strings.TrimSpace(req.GetWorkerId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if w.evented != nil {
		if err := w.evented.Heartbeat(ctx, req.GetWorkerId(), models.RuntimeEventMetadata{WorkerID: req.GetWorkerId(), Producer: "grpc-worker-control"}); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return &execgov1.HeartbeatResponse{Ok: true, ServerTimeUnixMs: time.Now().UnixMilli()}, nil
}

func (w *WorkerControlServer) PollTask(ctx context.Context, req *execgov1.PollTaskRequest) (*execgov1.PollTaskResponse, error) {
	if req == nil || strings.TrimSpace(req.GetWorkerId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if w.sched == nil || w.sched.Queue() == nil {
		return nil, status.Error(codes.FailedPrecondition, "scheduler queue is unavailable")
	}

	wait := 2 * time.Second
	if req.GetWaitMs() > 0 {
		wait = time.Duration(req.GetWaitMs()) * time.Millisecond
	}
	msg, err := w.sched.Queue().Poll(ctx, req.GetWorkerId(), wait)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if msg == nil {
		return &execgov1.PollTaskResponse{Found: false}, nil
	}

	task, ok := w.state.Get(msg.TaskID)
	if !ok {
		_ = w.sched.Queue().Ack(ctx, req.GetWorkerId(), msg.MessageID)
		return &execgov1.PollTaskResponse{Found: false}, nil
	}

	attempt := msg.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	leaseUntil := time.Now().UTC().Add(w.leaseDuration)
	w.sched.OnTaskLeased(task.ID, req.GetWorkerId(), leaseUntil, attempt)

	return &execgov1.PollTaskResponse{
		Found:            true,
		QueueMessageId:   msg.MessageID,
		Task:             taskToProto(task),
		Attempt:          int32(attempt),
		LeaseUntilUnixMs: leaseUntil.UnixMilli(),
	}, nil
}

func (w *WorkerControlServer) AckTask(ctx context.Context, req *execgov1.AckTaskRequest) (*execgov1.AckTaskResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	workerID := strings.TrimSpace(req.GetWorkerId())
	if workerID == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if strings.TrimSpace(req.GetQueueMessageId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_message_id is required")
	}
	if strings.TrimSpace(req.GetTaskId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}

	attempt := int(req.GetAttempt())
	if attempt <= 0 {
		attempt = 1
	}
	statusStr := strings.ToLower(strings.TrimSpace(req.GetStatus()))
	var output json.RawMessage
	if strings.TrimSpace(req.GetOutputJson()) != "" {
		output = json.RawMessage(req.GetOutputJson())
	}

	switch statusStr {
	case "success":
		w.sched.OnTaskStarted(req.GetTaskId(), workerID, attempt)
		w.sched.OnTaskSucceeded(req.GetTaskId(), workerID, output, attempt, req.GetHandleId())
	case "retry":
		w.sched.OnTaskStarted(req.GetTaskId(), workerID, attempt)
		delay := req.GetRetryDelayMs()
		runAt := time.Now().UTC()
		if delay > 0 {
			runAt = runAt.Add(time.Duration(delay) * time.Millisecond)
		}
		w.sched.OnTaskRetry(req.GetTaskId(), workerID, attempt+1, runAt, req.GetError())
	case "failed":
		w.sched.OnTaskStarted(req.GetTaskId(), workerID, attempt)
		w.sched.OnTaskFailed(req.GetTaskId(), workerID, output, attempt, req.GetError(), req.GetHandleId())
	default:
		return nil, status.Error(codes.InvalidArgument, "status must be success|failed|retry")
	}

	if err := w.sched.Queue().Ack(ctx, workerID, req.GetQueueMessageId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &execgov1.AckTaskResponse{Ok: true}, nil
}

func (w *WorkerControlServer) NackTask(ctx context.Context, req *execgov1.NackTaskRequest) (*execgov1.NackTaskResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	workerID := strings.TrimSpace(req.GetWorkerId())
	if workerID == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	msgID := strings.TrimSpace(req.GetQueueMessageId())
	if msgID == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_message_id is required")
	}

	requeueAt := time.Time{}
	if req.GetRequeueDelayMs() > 0 {
		requeueAt = time.Now().UTC().Add(time.Duration(req.GetRequeueDelayMs()) * time.Millisecond)
	}
	if err := w.sched.Queue().Nack(ctx, workerID, msgID, requeueAt, req.GetDeadLetter()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &execgov1.NackTaskResponse{Ok: true}, nil
}

func (w *WorkerControlServer) ReportProgress(ctx context.Context, req *execgov1.ReportProgressRequest) (*execgov1.ReportProgressResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	workerID := strings.TrimSpace(req.GetWorkerId())
	taskID := strings.TrimSpace(req.GetTaskId())
	if workerID == "" || taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id and task_id are required")
	}
	attempt := int(req.GetAttempt())
	if attempt <= 0 {
		attempt = 1
	}
	w.sched.OnTaskStarted(taskID, workerID, attempt)
	progress := json.RawMessage(req.GetProgressJson())
	if len(progress) == 0 {
		progress = json.RawMessage(`{"status":"running"}`)
	}
	w.sched.OnTaskProgress(taskID, workerID, progress, attempt)
	return &execgov1.ReportProgressResponse{Ok: true}, nil
}

func (w *WorkerControlServer) ReportAudit(ctx context.Context, req *execgov1.ReportAuditRequest) (*execgov1.ReportAuditResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if w.evented == nil {
		return nil, status.Error(codes.FailedPrecondition, "audit store unavailable on current backend")
	}
	workerID := strings.TrimSpace(req.GetWorkerId())
	taskID := strings.TrimSpace(req.GetTaskId())
	if workerID == "" || taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id and task_id are required")
	}
	var auditPayload any
	if strings.TrimSpace(req.GetAuditJson()) == "" {
		auditPayload = map[string]any{"note": "empty audit"}
	} else {
		if err := json.Unmarshal([]byte(req.GetAuditJson()), &auditPayload); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid audit_json: "+err.Error())
		}
	}
	if err := w.evented.AppendAudit(ctx, taskID, auditPayload, models.RuntimeEventMetadata{TaskID: taskID, WorkerID: workerID, Attempt: int(req.GetAttempt()), Producer: "grpc-worker-control"}); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &execgov1.ReportAuditResponse{Ok: true}, nil
}
