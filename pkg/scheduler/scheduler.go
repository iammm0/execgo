// Package scheduler provides DAG orchestration and queue scheduling.
package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/observability"
	"github.com/iammm0/execgo/pkg/store"
	"github.com/iammm0/execgo/pkg/taskqueue"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Scheduler orchestrates DAG dependency resolution and queueing.
type Scheduler struct {
	state   store.Store
	evented store.EventBackedStore
	metrics *observability.Metrics
	logger  *slog.Logger
	queue   taskqueue.Queue

	mu         sync.Mutex
	depCount   map[string]int
	dependents map[string][]string
	started    bool
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates scheduler with memory queue by default.
func New(st store.Store, metrics *observability.Metrics, logger *slog.Logger, maxConcurrency int) *Scheduler {
	_ = maxConcurrency // concurrency is handled by workers in v2.
	return NewWithQueue(st, metrics, logger, taskqueue.NewMemoryQueue())
}

// NewWithQueue creates scheduler with provided queue backend.
func NewWithQueue(st store.Store, metrics *observability.Metrics, logger *slog.Logger, queue taskqueue.Queue) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Scheduler{
		state:      st,
		metrics:    metrics,
		logger:     logger,
		queue:      queue,
		depCount:   make(map[string]int),
		dependents: make(map[string][]string),
	}
	if es, ok := st.(store.EventBackedStore); ok {
		s.evented = es
	}
	return s
}

// Start starts scheduler background services.
func (s *Scheduler) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true
	s.mu.Unlock()

	if s.queue != nil {
		if err := s.queue.Start(s.ctx); err != nil {
			s.logger.Error("queue start failed", "error", err)
		}
	}
	s.restoreGraphState()
	s.logger.Info("scheduler started")
}

// Stop stops scheduler background services.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.started = false
	s.mu.Unlock()
	s.logger.Info("scheduler stopped")
}

// Queue returns the queue backend used by scheduler.
func (s *Scheduler) Queue() taskqueue.Queue {
	return s.queue
}

// Submit validates and submits DAG tasks to scheduler.
func (s *Scheduler) Submit(graph *models.TaskGraph) {
	s.SubmitWithContext(context.Background(), graph)
}

// SubmitWithContext validates and submits DAG tasks using caller context.
func (s *Scheduler) SubmitWithContext(ctx context.Context, graph *models.TaskGraph) {
	if ctx == nil {
		ctx = context.Background()
	}
	if graph == nil || len(graph.Tasks) == 0 {
		return
	}
	ctx, span := observability.StartSpan(ctx, "scheduler.submit",
		trace.WithAttributes(
			attribute.Int("execgo.task_count", len(graph.Tasks)),
		),
	)
	defer span.End()

	now := time.Now().UTC()

	if s.evented != nil {
		res, err := s.evented.SubmitGraph(ctx, graph, store.SubmitOptions{})
		if err != nil {
			s.logger.Error("submit graph failed", "error", err)
			return
		}
		s.logger.Info("task graph submitted", "workflow_id", res.WorkflowID, "tasks", len(res.TaskIDs), "idempotent_hit", res.IdempotentHit)
		if res.IdempotentHit {
			return
		}
	}

	s.mu.Lock()
	for _, task := range graph.Tasks {
		s.depCount[task.ID] = len(task.DependsOn)
		for _, dep := range task.DependsOn {
			s.dependents[dep] = appendUnique(s.dependents[dep], task.ID)
		}
	}
	s.mu.Unlock()

	for _, task := range graph.Tasks {
		if task.Priority < 0 || task.Priority > 9 {
			task.Priority = 5
		}
		if task.ScheduledAt != nil && task.ScheduledAt.After(now) {
			_ = s.queue.EnqueueDelayed(ctx, task.ID, task.Priority, task.Attempt, *task.ScheduledAt)
			continue
		}
		if len(task.DependsOn) == 0 {
			_ = s.queue.Enqueue(ctx, task.ID, task.Priority, task.Attempt)
		}
		s.metrics.TasksTotal.Add(1)
		s.metrics.IncType(task.Type)
	}
}

// OnTaskLeased records lease state.
func (s *Scheduler) OnTaskLeased(taskID, workerID string, until time.Time, attempt int) {
	if s.evented == nil {
		return
	}
	_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusLeased, store.TransitionOptions{
		LeaseOwner: workerID,
		LeaseUntil: until,
		Attempt:    attempt,
		Metadata:   models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
	})
	if err != nil {
		s.logger.Warn("mark task leased failed", "task_id", taskID, "error", err)
	}
}

// OnTaskStarted records running state.
func (s *Scheduler) OnTaskStarted(taskID, workerID string, attempt int) {
	task, ok := s.state.Get(taskID)
	if ok && task.Status == models.StatusRunning {
		return
	}
	if s.evented == nil {
		s.state.UpdateStatus(taskID, models.StatusRunning, nil, "")
		s.metrics.TasksRunning.Add(1)
		return
	}
	_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusRunning, store.TransitionOptions{
		Attempt:  attempt,
		Metadata: models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
	})
	if err != nil {
		s.logger.Warn("mark task started failed", "task_id", taskID, "error", err)
		return
	}
	s.metrics.TasksRunning.Add(1)
}

// OnTaskProgress records progress payload.
func (s *Scheduler) OnTaskProgress(taskID, workerID string, progress json.RawMessage, attempt int) {
	if s.evented == nil {
		return
	}
	_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusRunning, store.TransitionOptions{
		EventType: models.RuntimeEventProgress,
		Progress:  progress,
		Attempt:   attempt,
		Metadata:  models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
	})
	if err != nil {
		s.logger.Warn("record task progress failed", "task_id", taskID, "error", err)
	}
}

// OnTaskSucceeded handles success transition and schedules dependents.
func (s *Scheduler) OnTaskSucceeded(taskID, workerID string, output json.RawMessage, attempt int, handleID string) {
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusSuccess, store.TransitionOptions{
			Result:   output,
			HandleID: handleID,
			Attempt:  attempt,
			Metadata: models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
		})
		if err != nil {
			s.logger.Warn("mark task success failed", "task_id", taskID, "error", err)
		}
	} else {
		s.state.UpdateStatus(taskID, models.StatusSuccess, output, "")
	}
	s.metrics.TasksRunning.Add(-1)
	s.metrics.TasksSucceeded.Add(1)

	s.mu.Lock()
	children := append([]string(nil), s.dependents[taskID]...)
	for _, child := range children {
		s.depCount[child]--
	}
	s.mu.Unlock()

	for _, child := range children {
		s.mu.Lock()
		ready := s.depCount[child] <= 0
		s.mu.Unlock()
		if ready {
			s.enqueueReady(child)
		}
	}
}

// OnTaskRetry schedules retry with delay.
func (s *Scheduler) OnTaskRetry(taskID, workerID string, attempt int, runAt time.Time, errMsg string) {
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusRetrying, store.TransitionOptions{
			Error:    errMsg,
			Attempt:  attempt,
			Metadata: models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
			Payload: map[string]any{
				"retry_at": runAt,
			},
		})
		if err != nil {
			s.logger.Warn("mark task retrying failed", "task_id", taskID, "error", err)
		} else {
			_, readyErr := s.evented.TransitionTask(context.Background(), taskID, models.StatusReady, store.TransitionOptions{
				EventType: models.RuntimeEventReady,
				Attempt:   attempt,
				Metadata:  models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
				Payload: map[string]any{
					"reason": "retry_scheduled",
				},
			})
			if readyErr != nil {
				s.logger.Warn("mark retried task ready failed", "task_id", taskID, "error", readyErr)
			}
		}
	}
	task, ok := s.state.Get(taskID)
	if !ok {
		return
	}
	s.metrics.TasksRunning.Add(-1)
	_ = s.queue.EnqueueDelayed(context.Background(), taskID, task.Priority, attempt, runAt)
}

// OnTaskFailed handles failure transition and cascade skip behavior.
func (s *Scheduler) OnTaskFailed(taskID, workerID string, output json.RawMessage, attempt int, errMsg string, handleID string) {
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusFailed, store.TransitionOptions{
			Result:   output,
			Error:    errMsg,
			HandleID: handleID,
			Attempt:  attempt,
			Metadata: models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
		})
		if err != nil {
			s.logger.Warn("mark task failed failed", "task_id", taskID, "error", err)
		}
	} else {
		s.state.UpdateStatus(taskID, models.StatusFailed, output, errMsg)
	}
	s.metrics.TasksRunning.Add(-1)
	s.metrics.TasksFailed.Add(1)

	s.mu.Lock()
	children := append([]string(nil), s.dependents[taskID]...)
	s.mu.Unlock()
	for _, child := range children {
		s.markSkippedCascade(child, "dependency "+taskID+" failed")
	}

	if task, ok := s.state.Get(taskID); ok {
		for _, compTaskID := range task.CompensateWith {
			s.enqueueReady(compTaskID)
		}
	}
}

func (s *Scheduler) markSkippedCascade(taskID, reason string) {
	if s.evented != nil {
		_, _ = s.evented.TransitionTask(context.Background(), taskID, models.StatusSkipped, store.TransitionOptions{
			EventType: models.RuntimeEventFailed,
			Error:     reason,
		})
	} else {
		s.state.UpdateStatus(taskID, models.StatusSkipped, nil, reason)
	}

	s.mu.Lock()
	children := append([]string(nil), s.dependents[taskID]...)
	s.mu.Unlock()
	for _, child := range children {
		s.markSkippedCascade(child, "dependency "+taskID+" skipped")
	}
}

func (s *Scheduler) enqueueReady(taskID string) {
	task, ok := s.state.Get(taskID)
	if !ok {
		return
	}
	if task.Status.IsTerminal() {
		return
	}

	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusReady, store.TransitionOptions{})
		if err != nil {
			s.logger.Warn("mark task ready failed", "task_id", taskID, "error", err)
			return
		}
	}

	if task.ScheduledAt != nil && task.ScheduledAt.After(time.Now().UTC()) {
		_ = s.queue.EnqueueDelayed(context.Background(), taskID, task.Priority, task.Attempt, *task.ScheduledAt)
		return
	}
	_ = s.queue.Enqueue(context.Background(), taskID, task.Priority, task.Attempt)
}

func (s *Scheduler) restoreGraphState() {
	tasks := s.state.GetAll()
	if len(tasks) == 0 {
		return
	}
	s.mu.Lock()
	s.depCount = make(map[string]int, len(tasks))
	s.dependents = make(map[string][]string)
	for _, task := range tasks {
		if task.Status.IsTerminal() {
			s.depCount[task.ID] = 0
			continue
		}
		pendingDeps := 0
		for _, dep := range task.DependsOn {
			s.dependents[dep] = appendUnique(s.dependents[dep], task.ID)
			depTask, ok := s.state.Get(dep)
			if !ok || depTask.Status != models.StatusSuccess {
				pendingDeps++
			}
		}
		s.depCount[task.ID] = pendingDeps
	}
	s.mu.Unlock()

	ready := make([]*models.Task, 0)
	for _, t := range tasks {
		if t.Status.IsTerminal() {
			continue
		}
		s.mu.Lock()
		deps := s.depCount[t.ID]
		s.mu.Unlock()
		if deps == 0 {
			ready = append(ready, t)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})
	for _, task := range ready {
		s.enqueueReady(task.ID)
	}
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
