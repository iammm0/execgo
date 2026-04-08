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
