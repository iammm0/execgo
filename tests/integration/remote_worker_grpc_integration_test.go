package integration_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/iammm0/execgo/contrib/grpcapi/pkg/grpcserver"
	execgov1 "github.com/iammm0/execgo/contrib/grpcapi/pkg/pb/proto/execgo/v1"
	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/executor"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/scheduler"
	"github.com/iammm0/execgo/pkg/store/eventsourced"
	"github.com/iammm0/execgo/pkg/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestDistributedRuntime_RemoteWorkerEndToEndGRPC(t *testing.T) {
	executor.RegisterBuiltins()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new event sourced manager: %v", err)
	}
	metrics := observability.NewMetrics()

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	defer runtimeCancel()

	sched := scheduler.New(st, metrics, logger, 4)
	sched.Start(runtimeCtx)
	defer sched.Stop()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	defer lis.Close()

	grpcSrv := grpc.NewServer()
	execgov1.RegisterExecGoServer(grpcSrv, grpcserver.NewServer(st, sched, metrics, logger))
	execgov1.RegisterWorkerControlServer(grpcSrv, grpcserver.NewWorkerControlServer(st, sched, logger))
	defer grpcSrv.GracefulStop()

	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, err := grpc.DialContext(dialCtx, lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	defer conn.Close()

	execClient := execgov1.NewExecGoClient(conn)

	remote := worker.NewRemoteGRPCWorker(worker.RemoteGRPCConfig{
		Endpoint:          lis.Addr().String(),
		WorkerID:          "remote-worker-it",
		Capabilities:      map[string]string{"executor": "os,noop", "sandbox": "local"},
		Concurrency:       2,
		PollWait:          80 * time.Millisecond,
		HeartbeatInterval: 120 * time.Millisecond,
	}, logger)
	if err := remote.Start(runtimeCtx); err != nil {
		t.Fatalf("start remote grpc worker: %v", err)
	}
	defer func() {
		if stopErr := remote.Stop(); stopErr != nil {
			t.Fatalf("stop remote grpc worker: %v", stopErr)
		}
	}()

	_, err = execClient.SubmitTasks(context.Background(), &execgov1.TaskGraph{
		Tasks: []*execgov1.Task{
			{
				Id:         "dist-remote-first",
				Type:       "noop",
				ParamsJson: `{"message":"hello remote worker"}`,
				Priority:   6,
			},
			{
				Id:        "dist-remote-second",
				Type:      "noop",
				DependsOn: []string{"dist-remote-first"},
				Priority:  6,
				RequiredCapabilities: map[string]string{
					"sandbox": "local",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("submit tasks via grpc: %v", err)
	}

	first := waitTaskByGRPC(t, execClient, "dist-remote-first", 8*time.Second)
	second := waitTaskByGRPC(t, execClient, "dist-remote-second", 8*time.Second)

	if first.GetStatus() != string(models.StatusSuccess) {
		t.Fatalf("first task status=%s want=%s error=%s", first.GetStatus(), models.StatusSuccess, first.GetError())
	}
	if second.GetStatus() != string(models.StatusSuccess) {
		t.Fatalf("second task status=%s want=%s error=%s", second.GetStatus(), models.StatusSuccess, second.GetError())
	}
	if second.GetRunStatus() != string(models.RuntimeSuccess) {
		t.Fatalf("second task run_status=%s want=%s", second.GetRunStatus(), models.RuntimeSuccess)
	}
	if second.GetResultJson() == "" {
		t.Fatalf("second task should keep legacy result_json for compatibility")
	}

	waitWorkerOnline(t, st, "remote-worker-it", 5*time.Second)
	if got := metrics.TasksSucceeded.Load(); got < 2 {
		t.Fatalf("expected tasks_succeeded >= 2, got %d", got)
	}

	ready, delayed, dead, depthErr := sched.Queue().Depth(context.Background())
	if depthErr != nil {
		t.Fatalf("queue depth: %v", depthErr)
	}
	if ready != 0 || delayed != 0 || dead != 0 {
		t.Fatalf("queue should be drained, ready=%d delayed=%d dead=%d", ready, delayed, dead)
	}
}

func TestDistributedRuntime_RemoteWorkerCancelGRPC(t *testing.T) {
	executor.RegisterBuiltins()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new event sourced manager: %v", err)
	}
	metrics := observability.NewMetrics()

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	defer runtimeCancel()

	sched := scheduler.New(st, metrics, logger, 2)
	sched.Start(runtimeCtx)
	defer sched.Stop()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	defer lis.Close()

	grpcSrv := grpc.NewServer()
	execgov1.RegisterExecGoServer(grpcSrv, grpcserver.NewServer(st, sched, metrics, logger))
	execgov1.RegisterWorkerControlServer(grpcSrv, grpcserver.NewWorkerControlServer(st, sched, logger))
	defer grpcSrv.GracefulStop()
	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	conn, err := grpc.DialContext(context.Background(), lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	defer conn.Close()

	execClient := execgov1.NewExecGoClient(conn)
	remote := worker.NewRemoteGRPCWorker(worker.RemoteGRPCConfig{
		Endpoint:            lis.Addr().String(),
		WorkerID:            "remote-worker-cancel-it",
		Capabilities:        map[string]string{"executor": "os", "sandbox": "local"},
		Concurrency:         1,
		PollWait:            50 * time.Millisecond,
		HeartbeatInterval:   100 * time.Millisecond,
		CancelCheckInterval: 40 * time.Millisecond,
	}, logger)
	if err := remote.Start(runtimeCtx); err != nil {
		t.Fatalf("start remote grpc worker: %v", err)
	}
	defer func() {
		if stopErr := remote.Stop(); stopErr != nil {
			t.Fatalf("stop remote grpc worker: %v", stopErr)
		}
	}()

	_, err = execClient.SubmitTasks(context.Background(), &execgov1.TaskGraph{
		Tasks: []*execgov1.Task{{
			Id:         "remote-cancel-sleep",
			Type:       "os",
			ToolName:   "sleep",
			ParamsJson: `{"duration_ms":5000}`,
			Priority:   6,
		}},
	})
	if err != nil {
		t.Fatalf("submit cancel task: %v", err)
	}
	waitTaskStatusByGRPC(t, execClient, "remote-cancel-sleep", models.StatusRunning, 5*time.Second)

	cancelResp, err := execClient.CancelTask(context.Background(), &execgov1.CancelTaskRequest{
		Id:     "remote-cancel-sleep",
		Reason: "grpc integration cancel",
	})
	if err != nil {
		t.Fatalf("cancel task via grpc: %v", err)
	}
	if !cancelResp.GetCancelled() || cancelResp.GetStatus() != string(models.StatusCancelled) {
		t.Fatalf("unexpected cancel response: %+v", cancelResp)
	}

	task := waitTaskByGRPC(t, execClient, "remote-cancel-sleep", 5*time.Second)
	if task.GetStatus() != string(models.StatusCancelled) {
		t.Fatalf("task status=%s want cancelled", task.GetStatus())
	}
	if task.GetRunStatus() != string(models.RuntimeCancelled) {
		t.Fatalf("run_status=%s want cancelled", task.GetRunStatus())
	}
	if got := metrics.TasksCancelled.Load(); got != 1 {
		t.Fatalf("TasksCancelled=%d want 1", got)
	}
}

func TestDistributedRuntime_RemoteWorkerCapabilityDispatchGRPC(t *testing.T) {
	executor.RegisterBuiltins()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := eventsourced.NewManager(events.NewMemoryStore(), logger)
	if err != nil {
		t.Fatalf("new event sourced manager: %v", err)
	}
	metrics := observability.NewMetrics()

	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	defer runtimeCancel()

	sched := scheduler.New(st, metrics, logger, 2)
	sched.Start(runtimeCtx)
	defer sched.Stop()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc: %v", err)
	}
	defer lis.Close()

	grpcSrv := grpc.NewServer()
	execgov1.RegisterExecGoServer(grpcSrv, grpcserver.NewServer(st, sched, metrics, logger))
	execgov1.RegisterWorkerControlServer(grpcSrv, grpcserver.NewWorkerControlServer(st, sched, logger))
	defer grpcSrv.GracefulStop()
	go func() {
		_ = grpcSrv.Serve(lis)
	}()

	conn, err := grpc.DialContext(context.Background(), lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	defer conn.Close()
	execClient := execgov1.NewExecGoClient(conn)

	wrong := worker.NewRemoteGRPCWorker(worker.RemoteGRPCConfig{
		Endpoint:          lis.Addr().String(),
		WorkerID:          "remote-worker-mcp-only",
		Capabilities:      map[string]string{"executor": "mcp", "sandbox": "local"},
		Concurrency:       1,
		PollWait:          40 * time.Millisecond,
		HeartbeatInterval: 100 * time.Millisecond,
	}, logger)
	if err := wrong.Start(runtimeCtx); err != nil {
		t.Fatalf("start wrong remote worker: %v", err)
	}
	defer func() {
		if stopErr := wrong.Stop(); stopErr != nil {
			t.Fatalf("stop wrong worker: %v", stopErr)
		}
	}()

	matching := worker.NewRemoteGRPCWorker(worker.RemoteGRPCConfig{
		Endpoint:          lis.Addr().String(),
		WorkerID:          "remote-worker-os",
		Capabilities:      map[string]string{"executor": "os", "sandbox": "local"},
		Concurrency:       1,
		PollWait:          40 * time.Millisecond,
		HeartbeatInterval: 100 * time.Millisecond,
	}, logger)
	if err := matching.Start(runtimeCtx); err != nil {
		t.Fatalf("start matching remote worker: %v", err)
	}
	defer func() {
		if stopErr := matching.Stop(); stopErr != nil {
			t.Fatalf("stop matching worker: %v", stopErr)
		}
	}()

	_, err = execClient.SubmitTasks(context.Background(), &execgov1.TaskGraph{
		Tasks: []*execgov1.Task{{
			Id:                   "remote-cap-os",
			Type:                 "os",
			ToolName:             "noop",
			RequiredCapabilities: map[string]string{"sandbox": "local"},
			Priority:             6,
		}},
	})
	if err != nil {
		t.Fatalf("submit capability task: %v", err)
	}

	task := waitTaskByGRPC(t, execClient, "remote-cap-os", 6*time.Second)
	if task.GetStatus() != string(models.StatusSuccess) {
		t.Fatalf("status=%s want success", task.GetStatus())
	}
	if task.GetRequiredCapabilities()["sandbox"] != "local" {
		t.Fatalf("required_capabilities=%v want sandbox=local", task.GetRequiredCapabilities())
	}
	if workerID := firstRemoteTaskStartedWorker(t, st, "remote-cap-os"); workerID != "remote-worker-os" {
		t.Fatalf("task_started worker=%q want remote-worker-os", workerID)
	}
}

func waitTaskByGRPC(t *testing.T, client execgov1.ExecGoClient, taskID string, timeout time.Duration) *execgov1.Task {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.GetTask(context.Background(), &execgov1.GetTaskRequest{Id: taskID})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				time.Sleep(30 * time.Millisecond)
				continue
			}
			t.Fatalf("grpc get task %s: %v", taskID, err)
		}
		task := resp.GetTask()
		if task == nil {
			time.Sleep(30 * time.Millisecond)
			continue
		}
		ts := models.TaskStatus(task.GetStatus())
		if ts.IsTerminal() {
			return task
		}
		time.Sleep(30 * time.Millisecond)
	}

	t.Fatalf("task %s did not become terminal within %v", taskID, timeout)
	return nil
}

func firstRemoteTaskStartedWorker(t *testing.T, st *eventsourced.Manager, taskID string) string {
	t.Helper()
	evs, err := st.EventStore().LoadGlobal(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	for _, ev := range evs {
		if ev.AggregateID == taskID && ev.Type == models.RuntimeEventStarted {
			return ev.Metadata.WorkerID
		}
	}
	t.Fatalf("task_started event not found for %s", taskID)
	return ""
}

func waitTaskStatusByGRPC(t *testing.T, client execgov1.ExecGoClient, taskID string, want models.TaskStatus, timeout time.Duration) *execgov1.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.GetTask(context.Background(), &execgov1.GetTaskRequest{Id: taskID})
		if err != nil {
			if status.Code(err) == codes.NotFound {
				time.Sleep(30 * time.Millisecond)
				continue
			}
			t.Fatalf("grpc get task %s: %v", taskID, err)
		}
		task := resp.GetTask()
		if task != nil && models.TaskStatus(task.GetStatus()) == want {
			return task
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach status %s within %v", taskID, want, timeout)
	return nil
}

func waitWorkerOnline(t *testing.T, st *eventsourced.Manager, workerID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, w := range st.ListWorkers() {
			if w.ID == workerID && w.Status == "online" {
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("worker %s did not become online within %v", workerID, timeout)
}
