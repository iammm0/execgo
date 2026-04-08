package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	execgov1 "github.com/iammm0/execgo/contrib/grpcapi/pkg/pb/proto/execgo/v1"
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/sandbox"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// RemoteGRPCConfig configures a gRPC-based remote worker client.
type RemoteGRPCConfig struct {
	Endpoint          string
	WorkerID          string
	Capabilities      map[string]string
	Concurrency       int
	PollWait          time.Duration
	HeartbeatInterval time.Duration
	RetryBaseBackoff  time.Duration
	RetryMaxBackoff   time.Duration
	Runner            sandbox.Runner
	DialOptions       []grpc.DialOption
}

// RemoteGRPCWorker executes tasks fetched from WorkerControl gRPC service.
type RemoteGRPCWorker struct {
	cfg RemoteGRPCConfig

	logger *slog.Logger
	conn   *grpc.ClientConn
	client execgov1.WorkerControlClient

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRemoteGRPCWorker creates a remote worker client.
func NewRemoteGRPCWorker(cfg RemoteGRPCConfig, logger *slog.Logger) *RemoteGRPCWorker {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = "worker-remote"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = 2 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.RetryBaseBackoff <= 0 {
		cfg.RetryBaseBackoff = 100 * time.Millisecond
	}
	if cfg.RetryMaxBackoff <= 0 {
		cfg.RetryMaxBackoff = 30 * time.Second
	}
	if cfg.Runner == nil {
		cfg.Runner = sandbox.LocalRunner{}
	}
	return &RemoteGRPCWorker{cfg: cfg, logger: logger}
}

// Start connects to control plane and starts worker loops.
func (w *RemoteGRPCWorker) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(w.cfg.Endpoint) == "" {
		return errors.New("remote worker endpoint is required")
	}
	if w.conn != nil {
		return nil
	}

	dialOpts := w.cfg.DialOptions
	if len(dialOpts) == 0 {
		dialOpts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		}
	}
	conn, err := grpc.DialContext(ctx, w.cfg.Endpoint, dialOpts...)
	if err != nil {
		return err
	}
	w.conn = conn
	w.client = execgov1.NewWorkerControlClient(conn)

	if _, err := w.client.RegisterWorker(ctx, &execgov1.RegisterWorkerRequest{WorkerId: w.cfg.WorkerID, Capabilities: w.cfg.Capabilities}); err != nil {
		_ = conn.Close()
		w.conn = nil
		w.client = nil
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.heartbeatLoop(runCtx)
	}()

	for i := 0; i < w.cfg.Concurrency; i++ {
		w.wg.Add(1)
		go func(slot int) {
			defer w.wg.Done()
			w.runLoop(runCtx, slot)
		}(i)
	}

	w.logger.Info("remote grpc worker started", "worker_id", w.cfg.WorkerID, "endpoint", w.cfg.Endpoint, "concurrency", w.cfg.Concurrency)
	return nil
}

// Stop stops loops and closes grpc connection.
func (w *RemoteGRPCWorker) Stop() error {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if w.conn != nil {
		if err := w.conn.Close(); err != nil {
			return err
		}
		w.conn = nil
		w.client = nil
	}
	w.logger.Info("remote grpc worker stopped", "worker_id", w.cfg.WorkerID)
	return nil
}

func (w *RemoteGRPCWorker) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := w.client.Heartbeat(ctx, &execgov1.HeartbeatRequest{WorkerId: w.cfg.WorkerID})
			if err != nil && !errors.Is(err, context.Canceled) {
				w.logger.Warn("remote heartbeat failed", "error", err)
			}
		}
	}
}

func (w *RemoteGRPCWorker) runLoop(ctx context.Context, slot int) {
	logger := w.logger.With("worker_id", w.cfg.WorkerID, "slot", slot)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		resp, err := w.client.PollTask(ctx, &execgov1.PollTaskRequest{
			WorkerId: w.cfg.WorkerID,
			WaitMs:   w.cfg.PollWait.Milliseconds(),
		})
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("poll task failed", "error", err)
			}
			continue
		}
		if resp == nil || !resp.GetFound() || resp.GetTask() == nil {
			continue
		}

		if err := w.handlePolledTask(ctx, resp); err != nil {
			logger.Warn("handle remote task failed", "task_id", resp.GetTask().GetId(), "error", err)
		}
	}
}

func (w *RemoteGRPCWorker) handlePolledTask(ctx context.Context, polled *execgov1.PollTaskResponse) error {
	task := taskFromProto(polled.GetTask())
	attempt := int(polled.GetAttempt())
	if attempt <= 0 {
		attempt = 1
	}

	executor.NormalizeTask(task)
	execImpl, err := executor.Get(task.Type)
	if err != nil {
		_, ackErr := w.client.AckTask(ctx, &execgov1.AckTaskRequest{
			WorkerId:       w.cfg.WorkerID,
			QueueMessageId: polled.GetQueueMessageId(),
			TaskId:         task.ID,
			Status:         "failed",
			Error:          err.Error(),
			Attempt:        int32(attempt),
		})
		if ackErr != nil {
			return ackErr
		}
		return nil
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if task.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(task.Timeout)*time.Millisecond)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	res, runErr, audit := w.cfg.Runner.Run(runCtx, execImpl, task)
	if res != nil && len(res.Progress) > 0 {
		progressJSON, _ := json.Marshal(res.Progress)
		_, _ = w.client.ReportProgress(ctx, &execgov1.ReportProgressRequest{
			WorkerId:     w.cfg.WorkerID,
			TaskId:       task.ID,
			ProgressJson: string(progressJSON),
			Attempt:      int32(attempt),
		})
	}
	if runErr == nil && res != nil && !res.Status.IsTerminal() {
		res, runErr = awaitRuntimeResult(runCtx, execImpl, res)
	}

	if audit != nil {
		audit.WorkerID = w.cfg.WorkerID
		if b, err := json.Marshal(audit); err == nil {
			_, _ = w.client.ReportAudit(ctx, &execgov1.ReportAuditRequest{
				WorkerId:  w.cfg.WorkerID,
				TaskId:    task.ID,
				AuditJson: string(b),
				Attempt:   int32(attempt),
			})
		}
	}

	output := resultOutput(res)
	outputJSON := ""
	if len(output) > 0 {
		outputJSON = string(output)
	}
	handleID := ""
	if res != nil {
		handleID = res.HandleID
	}

	if runErr == nil {
		_, err = w.client.AckTask(ctx, &execgov1.AckTaskRequest{
			WorkerId:       w.cfg.WorkerID,
			QueueMessageId: polled.GetQueueMessageId(),
			TaskId:         task.ID,
			Status:         "success",
			OutputJson:     outputJSON,
			HandleId:       handleID,
			Attempt:        int32(attempt),
		})
		return err
	}

	maxAttempts := task.Retry + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if attempt < maxAttempts && shouldRetry(runErr, res) {
		nextAttempt := attempt + 1
		retryAfter := computeBackoff(w.cfg.RetryBaseBackoff, w.cfg.RetryMaxBackoff, nextAttempt)
		_, err = w.client.AckTask(ctx, &execgov1.AckTaskRequest{
			WorkerId:       w.cfg.WorkerID,
			QueueMessageId: polled.GetQueueMessageId(),
			TaskId:         task.ID,
			Status:         "retry",
			Error:          runErr.Error(),
			RetryDelayMs:   retryAfter.Milliseconds(),
			Attempt:        int32(attempt),
		})
		return err
	}

	_, err = w.client.AckTask(ctx, &execgov1.AckTaskRequest{
		WorkerId:       w.cfg.WorkerID,
		QueueMessageId: polled.GetQueueMessageId(),
		TaskId:         task.ID,
		Status:         "failed",
		OutputJson:     outputJSON,
		Error:          runErr.Error(),
		HandleId:       handleID,
		Attempt:        int32(attempt),
	})
	return err
}

func awaitRuntimeResult(ctx context.Context, execImpl executor.Executor, initial *executor.Result) (*executor.Result, error) {
	if initial == nil || initial.HandleID == "" {
		return initial, nil
	}
	reader, ok := execImpl.(executor.HandleReader)
	if !ok {
		return initial, nil
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	current := initial
	for {
		if current != nil && current.Status.IsTerminal() {
			return current, runtimeResultError(current)
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-ticker.C:
			next, found := reader.GetHandle(initial.HandleID)
			if !found {
				return current, errors.New("handle not found")
			}
			current = next
		}
	}
}

func taskFromProto(t *execgov1.Task) *models.Task {
	if t == nil {
		return &models.Task{}
	}
	var params json.RawMessage
	if strings.TrimSpace(t.GetParamsJson()) != "" {
		params = json.RawMessage(t.GetParamsJson())
	}
	var input json.RawMessage
	if strings.TrimSpace(t.GetInputJson()) != "" {
		input = json.RawMessage(t.GetInputJson())
	}
	var scheduledAt *time.Time
	if t.GetScheduledAtUnixMs() > 0 {
		ts := time.UnixMilli(t.GetScheduledAtUnixMs()).UTC()
		scheduledAt = &ts
	}
	return &models.Task{
		ID:          t.GetId(),
		WorkflowID:  t.GetWorkflowId(),
		Type:        t.GetType(),
		Params:      params,
		ToolName:    t.GetToolName(),
		Input:       input,
		Category:    t.GetExecutionCategory(),
		DependsOn:   t.GetDependsOn(),
		Retry:       int(t.GetRetry()),
		Priority:    int(t.GetPriority()),
		ScheduledAt: scheduledAt,
		Timeout:     t.GetTimeoutMs(),
		Status:      models.TaskStatus(t.GetStatus()),
		Error:       t.GetError(),
		Attempt:     int(t.GetAttempt()),
		Version:     t.GetVersion(),
		HandleID:    t.GetHandleId(),
		RunStatus:   t.GetRunStatus(),
	}
}
