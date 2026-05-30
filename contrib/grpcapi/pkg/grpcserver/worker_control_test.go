package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	execgov1 "github.com/iammm0/execgo/contrib/grpcapi/pkg/pb/proto/execgo/v1"
	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workerControlHarness struct {
	store   *eventsourced.Manager
	sched   *scheduler.Scheduler
	metrics *observability.Metrics
	server  *WorkerControlServer
}

func newWorkerControlHarness(t *testing.T) *workerControlHarness {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new event sourced store: %v", err)
	}
	metrics := observability.NewMetrics()
	sched := scheduler.New(st, metrics, logger, 2)
	sched.Start(context.Background())
	t.Cleanup(sched.Stop)

	return &workerControlHarness{
		store:   st,
		sched:   sched,
		metrics: metrics,
		server:  NewWorkerControlServer(st, sched, logger),
	}
}

func TestWorkerControl_RegisterAndHeartbeat(t *testing.T) {
	h := newWorkerControlHarness(t)
	ctx := context.Background()

	_, err := h.server.RegisterWorker(ctx, &execgov1.RegisterWorkerRequest{
		WorkerId: "worker-a",
		Capabilities: map[string]string{
			"executor": "os,http",
			"sandbox":  "docker",
		},
	})
	if err != nil {
		t.Fatalf("register worker: %v", err)
	}
	_, err = h.server.Heartbeat(ctx, &execgov1.HeartbeatRequest{WorkerId: "worker-a"})
	if err != nil {
		t.Fatalf("heartbeat worker: %v", err)
	}

	workers := h.store.ListWorkers()
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(workers))
	}
	if workers[0].ID != "worker-a" {
		t.Fatalf("unexpected worker id: %s", workers[0].ID)
	}
	if workers[0].Status != "online" {
		t.Fatalf("expected worker online, got %s", workers[0].Status)
	}
	if workers[0].Capabilities["sandbox"] != "docker" {
		t.Fatalf("expected capability sandbox=docker, got %v", workers[0].Capabilities)
	}
}

func TestServer_CancelTaskStatusSemantics(t *testing.T) {
	h := newWorkerControlHarness(t)
	ctx := context.Background()
	server := NewServer(h.store, h.sched, h.metrics, h.server.logger)

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "grpc-cancel-ready", Type: "noop"}}})
	cancelled, err := server.CancelTask(ctx, &execgov1.CancelTaskRequest{
		Id:     "grpc-cancel-ready",
		Reason: "grpc test",
	})
	if err != nil {
		t.Fatalf("cancel ready task: %v", err)
	}
	if !cancelled.GetCancelled() || cancelled.GetStatus() != string(models.StatusCancelled) || cancelled.GetPreviousStatus() != string(models.StatusReady) {
		t.Fatalf("unexpected cancel response: %+v", cancelled)
	}

	again, err := server.CancelTask(ctx, &execgov1.CancelTaskRequest{Id: "grpc-cancel-ready"})
	if err != nil {
		t.Fatalf("cancel already cancelled: %v", err)
	}
	if again.GetCancelled() || again.GetReason() != "already_cancelled" {
		t.Fatalf("unexpected already-cancelled response: %+v", again)
	}

	if _, err := server.CancelTask(ctx, &execgov1.CancelTaskRequest{Id: "grpc-missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing cancel code=%s err=%v want NotFound", status.Code(err), err)
	}

	h.sched.Submit(&models.TaskGraph{Tasks: []*models.Task{{ID: "grpc-cancel-done", Type: "noop"}}})
	h.sched.OnTaskLeased("grpc-cancel-done", "worker-a", time.Now().UTC().Add(time.Second), 1)
	h.sched.OnTaskStarted("grpc-cancel-done", "worker-a", 1)
	h.sched.OnTaskSucceeded("grpc-cancel-done", "worker-a", nil, 1, "")
	if _, err := server.CancelTask(ctx, &execgov1.CancelTaskRequest{Id: "grpc-cancel-done"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("terminal cancel code=%s err=%v want FailedPrecondition", status.Code(err), err)
	}
}

func TestWorkerControl_PollAndAckSuccess(t *testing.T) {
	h := newWorkerControlHarness(t)
	ctx := context.Background()
	h.registerWorker(t, ctx, "worker-a", map[string]string{"executor": "noop", "sandbox": "local"})

	h.sched.Submit(&models.TaskGraph{
		Tasks: []*models.Task{
			{
				ID:       "task-success",
				Type:     "noop",
				Priority: 5,
			},
		},
	})

	poll, err := h.server.PollTask(ctx, &execgov1.PollTaskRequest{
		WorkerId: "worker-a",
		WaitMs:   300,
	})
	if err != nil {
		t.Fatalf("poll task: %v", err)
	}
	if poll == nil || !poll.GetFound() {
		t.Fatalf("expected found polled task, got %+v", poll)
	}
	if poll.GetTask() == nil || poll.GetTask().GetId() != "task-success" {
		t.Fatalf("unexpected polled task: %+v", poll.GetTask())
	}
	if poll.GetQueueMessageId() == "" {
		t.Fatalf("queue_message_id should not be empty")
	}
	if poll.GetLeaseUntilUnixMs() <= time.Now().UnixMilli() {
		t.Fatalf("lease should be in future, got %d", poll.GetLeaseUntilUnixMs())
	}

	_, err = h.server.AckTask(ctx, &execgov1.AckTaskRequest{
		WorkerId:       "worker-a",
		QueueMessageId: poll.GetQueueMessageId(),
		TaskId:         "task-success",
		Status:         "success",
		OutputJson:     `{"ok":true}`,
		Attempt:        poll.GetAttempt(),
	})
	if err != nil {
		t.Fatalf("ack success: %v", err)
	}

	task, ok := h.store.Get("task-success")
	if !ok {
		t.Fatalf("task should exist in store")
	}
	if task.Status != models.StatusSuccess {
		t.Fatalf("expected task status success, got %s", task.Status)
	}
	if string(task.Result) != `{"ok":true}` {
		t.Fatalf("unexpected task result: %s", string(task.Result))
	}
	if got := h.metrics.TasksSucceeded.Load(); got != 1 {
		t.Fatalf("expected TasksSucceeded=1, got %d", got)
	}
	if got := h.metrics.TasksRunning.Load(); got != 0 {
		t.Fatalf("expected TasksRunning=0 after ack, got %d", got)
	}
}

func TestWorkerControl_ReportProgressTransitionsToRunning(t *testing.T) {
	h := newWorkerControlHarness(t)
	ctx := context.Background()
	h.registerWorker(t, ctx, "worker-a", map[string]string{"executor": "noop", "sandbox": "local"})

	h.sched.Submit(&models.TaskGraph{
		Tasks: []*models.Task{
			{
				ID:       "task-progress",
				Type:     "noop",
				Priority: 3,
			},
		},
	})

	poll, err := h.server.PollTask(ctx, &execgov1.PollTaskRequest{
		WorkerId: "worker-a",
		WaitMs:   300,
	})
	if err != nil {
		t.Fatalf("poll task: %v", err)
	}
	if poll == nil || !poll.GetFound() {
		t.Fatalf("expected found polled task, got %+v", poll)
	}

	_, err = h.server.ReportProgress(ctx, &execgov1.ReportProgressRequest{
		WorkerId:     "worker-a",
		TaskId:       "task-progress",
		Attempt:      poll.GetAttempt(),
		ProgressJson: `{"step":"half"}`,
	})
	if err != nil {
		t.Fatalf("report progress: %v", err)
	}

	task, ok := h.store.Get("task-progress")
	if !ok {
		t.Fatalf("task should exist in store")
	}
	if task.Status != models.StatusRunning {
		t.Fatalf("expected running after progress, got %s", task.Status)
	}
	if got := h.metrics.TasksRunning.Load(); got != 1 {
		t.Fatalf("expected TasksRunning=1 after progress, got %d", got)
	}
}

func TestServer_TaskRequiredCapabilitiesProtoMapping(t *testing.T) {
	task := &models.Task{
		ID:                   "cap-task",
		Type:                 "noop",
		RequiredCapabilities: map[string]string{"sandbox": "docker"},
	}
	protoTask := taskToProto(task)
	if protoTask.GetRequiredCapabilities()["sandbox"] != "docker" {
		t.Fatalf("proto required_capabilities=%v want sandbox=docker", protoTask.GetRequiredCapabilities())
	}

	modelTask := taskFromProto(&execgov1.Task{
		Id:                   "cap-task",
		Type:                 "noop",
		RequiredCapabilities: map[string]string{"sandbox": "docker"},
	})
	if modelTask.RequiredCapabilities["sandbox"] != "docker" {
		t.Fatalf("model required_capabilities=%v want sandbox=docker", modelTask.RequiredCapabilities)
	}
}

func (h *workerControlHarness) registerWorker(t *testing.T, ctx context.Context, workerID string, caps map[string]string) {
	t.Helper()
	_, err := h.server.RegisterWorker(ctx, &execgov1.RegisterWorkerRequest{
		WorkerId:     workerID,
		Capabilities: caps,
	})
	if err != nil {
		t.Fatalf("register worker %s: %v", workerID, err)
	}
}
