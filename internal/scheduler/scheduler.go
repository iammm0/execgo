// Package scheduler 实现基于 DAG 的任务调度器
// implements a DAG-based task scheduler with concurrency control.
package scheduler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/iammm0/execgo/internal/executor"
	"github.com/iammm0/execgo/internal/models"
	"github.com/iammm0/execgo/internal/observability"
	"github.com/iammm0/execgo/internal/state"
)

// Scheduler DAG 任务调度器 / DAG task scheduler.
type Scheduler struct {
	state   *state.Manager
	metrics *observability.Metrics
	logger  *slog.Logger

	readyQueue chan *models.Task          // 就绪队列 / ready queue
	semaphore  chan struct{}              // 并发信号量 / concurrency semaphore
	depCount   map[string]int            // 剩余依赖计数 / remaining dependency count
	dependents map[string][]string       // 反向依赖图: 谁依赖完成后应通知谁 / reverse dependency graph
	mu         sync.Mutex                // 保护 depCount 和 dependents / protects depCount & dependents

	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// New 创建调度器 / creates a new scheduler.
func New(sm *state.Manager, metrics *observability.Metrics, logger *slog.Logger, maxConcurrency int) *Scheduler {
	return &Scheduler{
		state:      sm,
		metrics:    metrics,
		logger:     logger,
		readyQueue: make(chan *models.Task, 1024),
		semaphore:  make(chan struct{}, maxConcurrency),
		depCount:   make(map[string]int),
		dependents: make(map[string][]string),
	}
}

// Start 启动调度器工作循环 / starts the scheduler work loop.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop(ctx)
	}()

	s.logger.Info("scheduler started")
}

// Stop 优雅关闭调度器 / gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

// Submit 提交任务图到调度器 / submits a task graph to the scheduler.
func (s *Scheduler) Submit(graph *models.TaskGraph) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, task := range graph.Tasks {
		task.Status = models.StatusPending
		task.CreatedAt = now
		task.UpdatedAt = now
		s.state.Put(task)
		s.metrics.TasksTotal.Add(1)
		s.metrics.IncType(task.Type)

		// 构建依赖计数与反向依赖图 / build dependency count and reverse graph
		s.depCount[task.ID] = len(task.DependsOn)
		for _, dep := range task.DependsOn {
			s.dependents[dep] = append(s.dependents[dep], task.ID)
		}
	}

	// 将无依赖的任务加入就绪队列 / enqueue tasks with no dependencies
	for _, task := range graph.Tasks {
		if s.depCount[task.ID] == 0 {
			s.enqueue(task)
		}
	}
}

// enqueue 将任务放入就绪队列 / puts a task into the ready queue.
func (s *Scheduler) enqueue(task *models.Task) {
	select {
	case s.readyQueue <- task:
	default:
		s.logger.Warn("ready queue full, task will wait", "task_id", task.ID)
		go func() { s.readyQueue <- task }()
	}
}

// loop 调度主循环 / main scheduling loop.
func (s *Scheduler) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-s.readyQueue:
			s.semaphore <- struct{}{} // 获取并发槽 / acquire concurrency slot
			s.wg.Add(1)
			go func(t *models.Task) {
				defer s.wg.Done()
				defer func() { <-s.semaphore }() // 释放并发槽 / release concurrency slot
				s.executeTask(ctx, t)
			}(task)
		}
	}
}

// executeTask 执行单个任务，含超时和重试 / executes a single task with timeout and retry.
func (s *Scheduler) executeTask(ctx context.Context, task *models.Task) {
	logger := s.logger.With("task_id", task.ID, "task_type", task.Type)

	// 获取执行器 / get executor
	exec, err := executor.Get(task.Type)
	if err != nil {
		logger.Error("executor not found", "error", err)
		s.completeTask(task, models.StatusFailed, nil, err.Error())
		return
	}

	// 更新状态为 running / mark as running
	s.state.UpdateStatus(task.ID, models.StatusRunning, nil, "")
	s.metrics.TasksRunning.Add(1)
	logger.Info("task started")

	maxAttempts := task.Retry + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	var result json.RawMessage

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// 指数退避: 100ms * 2^(attempt-2) / exponential backoff
			backoff := time.Duration(100*(1<<(attempt-2))) * time.Millisecond
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			logger.Info("retrying task", "attempt", attempt, "backoff", backoff)
			time.Sleep(backoff)
		}

		// 构建带超时的 context / build context with timeout
		execCtx := ctx
		var cancelFn context.CancelFunc
		if task.Timeout > 0 {
			execCtx, cancelFn = context.WithTimeout(ctx, time.Duration(task.Timeout)*time.Millisecond)
		} else {
			execCtx, cancelFn = context.WithCancel(ctx)
		}

		result, lastErr = exec.Execute(execCtx, task)
		cancelFn()

		if lastErr == nil {
			break
		}
		logger.Warn("task attempt failed", "attempt", attempt, "error", lastErr)
	}

	s.metrics.TasksRunning.Add(-1)

	if lastErr != nil {
		logger.Error("task failed after all retries", "error", lastErr)
		s.completeTask(task, models.StatusFailed, result, lastErr.Error())
	} else {
		logger.Info("task completed successfully")
		s.completeTask(task, models.StatusSuccess, result, "")
	}
}

// completeTask 完成任务并级联触发下游依赖 / completes a task and cascades to downstream dependents.
func (s *Scheduler) completeTask(task *models.Task, status models.TaskStatus, result json.RawMessage, errMsg string) {
	s.state.UpdateStatus(task.ID, status, result, errMsg)

	if status == models.StatusSuccess {
		s.metrics.TasksSucceeded.Add(1)
	} else {
		s.metrics.TasksFailed.Add(1)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	children := s.dependents[task.ID]
	for _, childID := range children {
		if status == models.StatusFailed {
			// 依赖失败 → 跳过下游 / dependency failed → skip downstream
			s.state.UpdateStatus(childID, models.StatusSkipped, nil, "dependency "+task.ID+" failed")
			s.metrics.TasksFailed.Add(1)
			s.cascadeSkip(childID)
			continue
		}

		s.depCount[childID]--
		if s.depCount[childID] <= 0 {
			if child, ok := s.state.Get(childID); ok {
				s.enqueue(child)
			}
		}
	}
}

// cascadeSkip 级联跳过所有下游依赖 / cascades skip to all downstream dependents.
func (s *Scheduler) cascadeSkip(taskID string) {
	for _, childID := range s.dependents[taskID] {
		s.state.UpdateStatus(childID, models.StatusSkipped, nil, "dependency "+taskID+" skipped")
		s.cascadeSkip(childID)
	}
}
