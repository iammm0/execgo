// Package scheduler provides DAG orchestration and queue scheduling.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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
	state    store.Store
	evented  store.EventBackedStore
	metrics  *observability.Metrics
	logger   *slog.Logger
	queue    taskqueue.Queue
	recovery RecoveryConfig

	mu         sync.Mutex
	depCount   map[string]int
	dependents map[string][]string
	inFlight   map[string]context.CancelFunc
	started    bool
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

var (
	// ErrTaskNotFound is returned when cancellation targets an unknown task.
	ErrTaskNotFound = errors.New("task not found")
	// ErrTaskTerminal is returned when cancellation targets a completed task.
	ErrTaskTerminal = errors.New("task already terminal")
)

const defaultCancelReason = "user_requested"

// RecoveryConfig controls lease recovery and worker stale detection.
type RecoveryConfig struct {
	Enabled            bool
	LeaseSweepInterval time.Duration
	WorkerStaleAfter   time.Duration
	MaxRecoverBatch    int
}

// RecoveryReport summarizes one recovery sweep.
type RecoveryReport struct {
	Requeued     int
	TimedOut     int
	WorkersStale int
}

// CancelResult summarizes a task cancellation request.
type CancelResult struct {
	Cancelled      bool              `json:"cancelled"`
	TaskID         string            `json:"task_id"`
	Status         models.TaskStatus `json:"status"`
	PreviousStatus models.TaskStatus `json:"previous_status,omitempty"`
	Reason         string            `json:"reason,omitempty"`
}

// DefaultRecoveryConfig returns production-safe recovery defaults.
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		Enabled:            true,
		LeaseSweepInterval: 5 * time.Second,
		WorkerStaleAfter:   15 * time.Second,
		MaxRecoverBatch:    100,
	}
}

// New creates scheduler with memory queue by default.
func New(st store.Store, metrics *observability.Metrics, logger *slog.Logger, maxConcurrency int) *Scheduler {
	_ = maxConcurrency // concurrency is handled by workers in v2.
	return NewWithQueue(st, metrics, logger, taskqueue.NewMemoryQueue())
}

// NewWithQueue creates scheduler with provided queue backend.
func NewWithQueue(st store.Store, metrics *observability.Metrics, logger *slog.Logger, queue taskqueue.Queue) *Scheduler {
	return NewWithQueueAndRecovery(st, metrics, logger, queue, DefaultRecoveryConfig())
}

// NewWithQueueAndRecovery creates scheduler with provided queue and recovery behavior.
func NewWithQueueAndRecovery(st store.Store, metrics *observability.Metrics, logger *slog.Logger, queue taskqueue.Queue, recovery RecoveryConfig) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Scheduler{
		state:      st,
		metrics:    metrics,
		logger:     logger,
		queue:      queue,
		recovery:   normalizeRecoveryConfig(recovery),
		depCount:   make(map[string]int),
		dependents: make(map[string][]string),
		inFlight:   make(map[string]context.CancelFunc),
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
	if s.recovery.Enabled && s.evented != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.recoveryLoop(s.ctx)
		}()
	}
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
	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// Queue returns the queue backend used by scheduler.
func (s *Scheduler) Queue() taskqueue.Queue {
	return s.queue
}

// RegisterInFlight records a cancellable running task context.
func (s *Scheduler) RegisterInFlight(taskID string, cancel context.CancelFunc) func() {
	if taskID == "" || cancel == nil {
		return func() {}
	}
	s.mu.Lock()
	s.inFlight[taskID] = cancel
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.inFlight, taskID)
		s.mu.Unlock()
	}
}

// CancelTask marks a non-terminal task cancelled and signals any local runner.
func (s *Scheduler) CancelTask(ctx context.Context, taskID, reason, source string) (CancelResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = defaultCancelReason
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "scheduler"
	}

	task, ok := s.state.Get(taskID)
	if !ok {
		return CancelResult{TaskID: taskID, Reason: reason}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	result := CancelResult{
		TaskID:         task.ID,
		Status:         task.Status,
		PreviousStatus: task.Status,
		Reason:         reason,
	}
	if task.Status == models.StatusCancelled {
		result.Reason = "already_cancelled"
		return result, nil
	}
	if task.Status.IsTerminal() {
		return result, fmt.Errorf("%w: %s", ErrTaskTerminal, task.Status)
	}

	if s.evented != nil {
		if _, err := s.evented.TransitionTask(ctx, task.ID, models.StatusCancelled, store.TransitionOptions{
			Error:      reason,
			ClearLease: true,
			Attempt:    task.Attempt,
			Metadata: models.RuntimeEventMetadata{
				TaskID:   task.ID,
				WorkerID: task.LeaseOwner,
				Attempt:  task.Attempt,
				Producer: source,
			},
			Payload: map[string]any{
				"reason":          reason,
				"previous_status": task.Status,
				"source":          source,
			},
		}); err != nil {
			return result, err
		}
	} else if !s.state.UpdateStatus(task.ID, models.StatusCancelled, nil, reason) {
		return result, fmt.Errorf("%w: %s", ErrTaskNotFound, task.ID)
	}

	if task.Status == models.StatusRunning {
		s.metrics.TasksRunning.Add(-1)
	}
	s.metrics.TasksCancelled.Add(1)
	s.cancelInFlight(task.ID)
	s.skipDependentsForCancel(ctx, task.ID)

	result.Cancelled = true
	result.Status = models.StatusCancelled
	return result, nil
}

func (s *Scheduler) cancelInFlight(taskID string) {
	s.mu.Lock()
	cancel := s.inFlight[taskID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RecoverExpiredLeases requeues expired leases and marks stale workers.
func (s *Scheduler) RecoverExpiredLeases(ctx context.Context, now time.Time) (RecoveryReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report := RecoveryReport{}
	if s.evented == nil || s.queue == nil {
		return report, nil
	}

	limit := s.recovery.MaxRecoverBatch
	if limit <= 0 {
		limit = DefaultRecoveryConfig().MaxRecoverBatch
	}

	var joined error
	recovered := 0
	for _, task := range s.state.GetAll() {
		if recovered >= limit {
			break
		}
		if task == nil || task.Status.IsTerminal() {
			continue
		}
		if task.Status != models.StatusLeased && task.Status != models.StatusRunning {
			continue
		}
		if task.LeaseUntil.IsZero() || task.LeaseUntil.After(now) {
			continue
		}

		if err := s.recoverExpiredTask(ctx, task, now, &report); err != nil {
			joined = errors.Join(joined, err)
		}
		recovered++
	}

	if err := s.markStaleWorkers(ctx, now, &report); err != nil {
		joined = errors.Join(joined, err)
	}

	return report, joined
}

func normalizeRecoveryConfig(cfg RecoveryConfig) RecoveryConfig {
	def := DefaultRecoveryConfig()
	if !cfg.Enabled && cfg.LeaseSweepInterval == 0 && cfg.WorkerStaleAfter == 0 && cfg.MaxRecoverBatch == 0 {
		cfg.Enabled = def.Enabled
	}
	if cfg.LeaseSweepInterval <= 0 {
		cfg.LeaseSweepInterval = def.LeaseSweepInterval
	}
	if cfg.WorkerStaleAfter <= 0 {
		cfg.WorkerStaleAfter = def.WorkerStaleAfter
	}
	if cfg.MaxRecoverBatch <= 0 {
		cfg.MaxRecoverBatch = def.MaxRecoverBatch
	}
	return cfg
}

func (s *Scheduler) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(s.recovery.LeaseSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			report, err := s.RecoverExpiredLeases(ctx, now.UTC())
			if err != nil {
				s.logger.Warn("lease recovery sweep failed", "error", err)
			}
			if report.Requeued > 0 || report.TimedOut > 0 || report.WorkersStale > 0 {
				s.logger.Info("lease recovery sweep completed", "requeued", report.Requeued, "timed_out", report.TimedOut, "workers_stale", report.WorkersStale)
			}
		}
	}
}

func (s *Scheduler) recoverExpiredTask(ctx context.Context, task *models.Task, now time.Time, report *RecoveryReport) error {
	attempt := task.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	nextAttempt := attempt + 1
	maxAttempts := task.Retry + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	payload := map[string]any{
		"reason":               "lease_expired",
		"expired_at":           now,
		"previous_lease_owner": task.LeaseOwner,
		"previous_lease_until": task.LeaseUntil,
	}

	if attempt >= maxAttempts {
		_, err := s.evented.TransitionTask(ctx, task.ID, models.StatusTimedOut, store.TransitionOptions{
			Error:      "lease expired before worker ack",
			Attempt:    attempt,
			ClearLease: true,
			Metadata: models.RuntimeEventMetadata{
				TaskID:   task.ID,
				WorkerID: task.LeaseOwner,
				Attempt:  attempt,
				Producer: "scheduler-recovery",
			},
			Payload: payload,
		})
		if err != nil {
			return err
		}
		if task.Status == models.StatusRunning {
			s.metrics.TasksRunning.Add(-1)
		}
		s.metrics.TasksFailed.Add(1)
		report.TimedOut++
		return nil
	}

	switch task.Status {
	case models.StatusRunning:
		if _, err := s.evented.TransitionTask(ctx, task.ID, models.StatusRetrying, store.TransitionOptions{
			Error:      "lease expired before worker ack",
			Attempt:    nextAttempt,
			ClearLease: true,
			Metadata: models.RuntimeEventMetadata{
				TaskID:   task.ID,
				WorkerID: task.LeaseOwner,
				Attempt:  nextAttempt,
				Producer: "scheduler-recovery",
			},
			Payload: payload,
		}); err != nil {
			return err
		}
		s.metrics.TasksRunning.Add(-1)
	}

	if _, err := s.evented.TransitionTask(ctx, task.ID, models.StatusReady, store.TransitionOptions{
		EventType:  models.RuntimeEventReady,
		Attempt:    nextAttempt,
		ClearLease: true,
		Metadata: models.RuntimeEventMetadata{
			TaskID:   task.ID,
			WorkerID: task.LeaseOwner,
			Attempt:  nextAttempt,
			Producer: "scheduler-recovery",
		},
		Payload: payload,
	}); err != nil {
		return err
	}

	if err := s.queue.Enqueue(ctx, task.ID, task.Priority, nextAttempt); err != nil {
		return err
	}
	report.Requeued++
	return nil
}

func (s *Scheduler) markStaleWorkers(ctx context.Context, now time.Time, report *RecoveryReport) error {
	if s.evented == nil {
		return nil
	}
	var joined error
	for _, worker := range s.evented.ListWorkers() {
		if worker == nil || worker.Status != "online" {
			continue
		}
		if worker.LastSeenAt.IsZero() || now.Sub(worker.LastSeenAt) <= s.recovery.WorkerStaleAfter {
			continue
		}
		err := s.evented.MarkWorkerHeartbeatMissed(ctx, worker.ID, models.RuntimeEventMetadata{
			WorkerID: worker.ID,
			Producer: "scheduler-recovery",
		})
		if err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		report.WorkersStale++
	}
	return joined
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
	if s.taskTerminalOrMissing(taskID) {
		return
	}
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
	if !ok || task.Status.IsTerminal() || task.Status == models.StatusRunning {
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
	if s.taskTerminalOrMissing(taskID) {
		return
	}
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
	if s.taskTerminalOrMissing(taskID) {
		return
	}
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusSuccess, store.TransitionOptions{
			Result:     output,
			HandleID:   handleID,
			ClearLease: true,
			Attempt:    attempt,
			Metadata:   models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
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
	if s.taskTerminalOrMissing(taskID) {
		return
	}
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusRetrying, store.TransitionOptions{
			Error:      errMsg,
			ClearLease: true,
			Attempt:    attempt,
			Metadata:   models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
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
	if s.taskTerminalOrMissing(taskID) {
		return
	}
	if s.evented != nil {
		_, err := s.evented.TransitionTask(context.Background(), taskID, models.StatusFailed, store.TransitionOptions{
			Result:     output,
			Error:      errMsg,
			HandleID:   handleID,
			ClearLease: true,
			Attempt:    attempt,
			Metadata:   models.RuntimeEventMetadata{WorkerID: workerID, Attempt: attempt, TaskID: taskID},
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
	task, ok := s.state.Get(taskID)
	if !ok || task.Status.IsTerminal() {
		return
	}
	if s.evented != nil {
		_, _ = s.evented.TransitionTask(context.Background(), taskID, models.StatusSkipped, store.TransitionOptions{
			EventType:  models.RuntimeEventFailed,
			Error:      reason,
			ClearLease: true,
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

func (s *Scheduler) skipDependentsForCancel(ctx context.Context, taskID string) {
	_ = ctx
	s.mu.Lock()
	children := append([]string(nil), s.dependents[taskID]...)
	s.mu.Unlock()
	for _, child := range children {
		s.markSkippedCascade(child, "upstream task cancelled: "+taskID)
	}
}

func (s *Scheduler) taskTerminalOrMissing(taskID string) bool {
	task, ok := s.state.Get(taskID)
	return !ok || task.Status.IsTerminal()
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
