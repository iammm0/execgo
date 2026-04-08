package eventsourced

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/fsm"
	"github.com/iammm0/execgo/pkg/models"
	"github.com/iammm0/execgo/pkg/store"
)

// Manager is an event-sourced state manager with in-memory read models.
type Manager struct {
	mu sync.RWMutex

	eventStore events.EventStore
	logger     *slog.Logger

	tasks      map[string]*models.Task
	workflows  map[string]*workflowState
	dependents map[string][]string
	workers    map[string]*models.WorkerNode

	idempotency map[string]idempotencyState
}

type workflowState struct {
	ID             string
	TaskIDs        []string
	IdempotencyKey string
	CreatedAt      time.Time
}

type idempotencyState struct {
	WorkflowID string
	ExpiresAt  time.Time
}

const defaultIdempotencyWindow = 24 * time.Hour

var _ store.Store = (*Manager)(nil)
var _ store.EventBackedStore = (*Manager)(nil)

// NewManager creates event-sourced state manager and replays historical events.
func NewManager(eventStore events.EventStore, logger *slog.Logger) (*Manager, error) {
	if eventStore == nil {
		eventStore = events.NewMemoryStore()
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{
		eventStore:  eventStore,
		logger:      logger,
		tasks:       make(map[string]*models.Task),
		workflows:   make(map[string]*workflowState),
		dependents:  make(map[string][]string),
		workers:     make(map[string]*models.WorkerNode),
		idempotency: make(map[string]idempotencyState),
	}
	if err := m.Replay(context.Background()); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) EventStore() events.EventStore { return m.eventStore }

func (m *Manager) Replay(ctx context.Context) error {
	evs, err := m.eventStore.LoadGlobal(ctx, 0, 0)
	if err != nil {
		return fmt.Errorf("load global events: %w", err)
	}

	m.mu.Lock()
	m.tasks = make(map[string]*models.Task)
	m.workflows = make(map[string]*workflowState)
	m.dependents = make(map[string][]string)
	m.workers = make(map[string]*models.WorkerNode)
	m.idempotency = make(map[string]idempotencyState)
	for _, ev := range evs {
		m.applyLocked(ev)
	}
	m.mu.Unlock()

	if len(evs) > 0 {
		m.logger.Info("event replay finished", "events", len(evs), "tasks", len(m.tasks), "workers", len(m.workers))
	}
	return nil
}

func (m *Manager) SubmitGraph(ctx context.Context, graph *models.TaskGraph, options store.SubmitOptions) (*store.SubmitResult, error) {
	if graph == nil {
		return nil, fmt.Errorf("task graph is required")
	}
	if err := graph.Validate(); err != nil {
		return nil, err
	}

	workflowID := strings.TrimSpace(options.WorkflowID)
	if workflowID == "" {
		workflowID = strings.TrimSpace(graph.WorkflowID)
	}
	if workflowID == "" {
		workflowID = fmt.Sprintf("wf-%d", time.Now().UnixNano())
	}

	idempotencyKey := strings.TrimSpace(options.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(graph.IdempotencyKey)
	}

	m.mu.RLock()
	if idempotencyKey != "" {
		if id, ok := m.idempotency[idempotencyKey]; ok && time.Now().Before(id.ExpiresAt) {
			wf := m.workflows[id.WorkflowID]
			taskIDs := make([]string, 0)
			if wf != nil {
				taskIDs = append(taskIDs, wf.TaskIDs...)
			}
			m.mu.RUnlock()
			return &store.SubmitResult{WorkflowID: id.WorkflowID, TaskIDs: taskIDs, IdempotentHit: true}, nil
		}
	}
	m.mu.RUnlock()

	now := time.Now().UTC()
	result := &store.SubmitResult{
		WorkflowID: workflowID,
		TaskIDs:    make([]string, 0, len(graph.Tasks)),
	}

	for _, input := range graph.Tasks {
		if _, ok := m.Get(input.ID); ok {
			return nil, fmt.Errorf("task id already exists: %s", input.ID)
		}
		result.TaskIDs = append(result.TaskIDs, input.ID)

		task := cloneTask(input)
		task.WorkflowID = workflowID
		task.Status = models.StatusPending
		task.CreatedAt = now
		task.UpdatedAt = now
		task.RemainingDeps = len(task.DependsOn)
		if task.Priority < 0 || task.Priority > 9 {
			task.Priority = 5
		}

		payload, _ := json.Marshal(map[string]any{
			"task":            task,
			"workflow_id":     workflowID,
			"idempotency_key": idempotencyKey,
		})
		ev := models.RuntimeEvent{
			AggregateID: input.ID,
			Type:        models.RuntimeEventSubmitted,
			Payload:     payload,
			Metadata:    options.Metadata,
			CreatedAt:   now,
		}
		appended, err := m.appendAndApply(ctx, streamTask(input.ID), -1, ev)
		if err != nil {
			return nil, err
		}
		currentVersion := int64(0)
		if len(appended) > 0 {
			currentVersion = appended[len(appended)-1].AggregateVersion
		}

		if task.RemainingDeps == 0 {
			readyPayload, _ := json.Marshal(map[string]any{"status": models.StatusReady, "reason": "root_task"})
			readyEvent := models.RuntimeEvent{
				AggregateID: input.ID,
				Type:        models.RuntimeEventReady,
				Payload:     readyPayload,
				Metadata:    options.Metadata,
				CreatedAt:   now,
			}
			if _, err := m.appendAndApply(ctx, streamTask(input.ID), currentVersion, readyEvent); err != nil {
				return nil, err
			}
		}
	}

	m.mu.Lock()
	wf := &workflowState{
		ID:             workflowID,
		TaskIDs:        append([]string(nil), result.TaskIDs...),
		IdempotencyKey: idempotencyKey,
		CreatedAt:      now,
	}
	m.workflows[workflowID] = wf
	if idempotencyKey != "" {
		m.idempotency[idempotencyKey] = idempotencyState{
			WorkflowID: workflowID,
			ExpiresAt:  now.Add(defaultIdempotencyWindow),
		}
	}
	m.mu.Unlock()

	return result, nil
}

func (m *Manager) TransitionTask(ctx context.Context, taskID string, to models.TaskStatus, opts store.TransitionOptions) (*models.Task, error) {
	m.mu.RLock()
	cur, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	from := cur.Status
	eventType := opts.EventType
	if eventType == "" {
		eventType = eventTypeFromStatus(to)
	}

	if err := fsm.EnsureTransition(taskID, from, to); err != nil {
		rejectPayload, _ := json.Marshal(map[string]any{
			"from":  from,
			"to":    to,
			"error": err.Error(),
		})
		rejected := models.RuntimeEvent{
			AggregateID: taskID,
			Type:        models.RuntimeEventInvalidTransitionRejected,
			Payload:     rejectPayload,
			Metadata:    opts.Metadata,
			CreatedAt:   time.Now().UTC(),
		}
		_, _ = m.appendAndApply(ctx, streamTask(taskID), cur.Version, rejected)
		return nil, err
	}

	payloadMap := map[string]any{
		"from":        from,
		"to":          to,
		"status":      to,
		"result":      opts.Result,
		"error":       opts.Error,
		"handle_id":   opts.HandleID,
		"progress":    opts.Progress,
		"lease_owner": opts.LeaseOwner,
		"lease_until": opts.LeaseUntil,
		"attempt":     opts.Attempt,
	}
	for k, v := range opts.Payload {
		payloadMap[k] = v
	}
	payload, _ := json.Marshal(payloadMap)
	ev := models.RuntimeEvent{
		AggregateID: taskID,
		Type:        eventType,
		Payload:     payload,
		Metadata:    opts.Metadata,
		CreatedAt:   time.Now().UTC(),
	}

	if _, err := m.appendAndApply(ctx, streamTask(taskID), cur.Version, ev); err != nil {
		return nil, err
	}
	updated, _ := m.Get(taskID)
	return updated, nil
}

func (m *Manager) AppendAudit(ctx context.Context, taskID string, audit any, metadata models.RuntimeEventMetadata) error {
	m.mu.RLock()
	task, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task not found: %s", taskID)
	}
	payload, _ := json.Marshal(map[string]any{
		"task_id": taskID,
		"audit":   audit,
	})
	ev := models.RuntimeEvent{
		AggregateID: taskID,
		Type:        models.RuntimeEventWorkerAudit,
		Payload:     payload,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := m.appendAndApply(ctx, streamTask(taskID), task.Version, ev)
	return err
}

// Put keeps backward compatibility by emitting submission event.
func (m *Manager) Put(task *models.Task) {
	if task == nil {
		return
	}
	graph := &models.TaskGraph{Tasks: []*models.Task{task}}
	_, _ = m.SubmitGraph(context.Background(), graph, store.SubmitOptions{WorkflowID: task.WorkflowID})
}

func (m *Manager) Get(id string) (*models.Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	return cloneTask(t), true
}

func (m *Manager) GetAll() []*models.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*models.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, cloneTask(t))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return false
	}
	delete(m.tasks, id)
	delete(m.dependents, id)
	for parent, children := range m.dependents {
		filtered := children[:0]
		for _, child := range children {
			if child != id {
				filtered = append(filtered, child)
			}
		}
		m.dependents[parent] = filtered
	}
	return true
}

func (m *Manager) UpdateStatus(id string, status models.TaskStatus, result json.RawMessage, errMsg string) bool {
	_, err := m.TransitionTask(context.Background(), id, status, store.TransitionOptions{
		Result: result,
		Error:  errMsg,
	})
	return err == nil
}

func (m *Manager) TaskDependents(taskID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]string(nil), m.dependents[taskID]...)
	sort.Strings(out)
	return out
}

func (m *Manager) WorkflowTasks(workflowID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	wf := m.workflows[workflowID]
	if wf == nil {
		return nil
	}
	out := append([]string(nil), wf.TaskIDs...)
	sort.Strings(out)
	return out
}

func (m *Manager) RegisterWorker(ctx context.Context, workerID string, capabilities map[string]string, metadata models.RuntimeEventMetadata) error {
	payload, _ := json.Marshal(map[string]any{"worker_id": workerID, "capabilities": capabilities, "status": "online"})
	ev := models.RuntimeEvent{
		AggregateID: workerID,
		Type:        models.RuntimeEventWorkerRegistered,
		Payload:     payload,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := m.appendAndApply(ctx, streamWorker(workerID), -1, ev)
	return err
}

func (m *Manager) Heartbeat(ctx context.Context, workerID string, metadata models.RuntimeEventMetadata) error {
	payload, _ := json.Marshal(map[string]any{"worker_id": workerID, "status": "online"})
	m.mu.RLock()
	_, ok := m.workers[workerID]
	m.mu.RUnlock()
	if !ok {
		if err := m.RegisterWorker(ctx, workerID, nil, metadata); err != nil {
			return err
		}
	}
	event := models.RuntimeEvent{
		AggregateID: workerID,
		Type:        models.RuntimeEventWorkerHeartbeat,
		Payload:     payload,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := m.appendAndApply(ctx, streamWorker(workerID), -1, event)
	return err
}

func (m *Manager) MarkWorkerOffline(ctx context.Context, workerID string, metadata models.RuntimeEventMetadata) error {
	payload, _ := json.Marshal(map[string]any{"worker_id": workerID, "status": "offline"})
	event := models.RuntimeEvent{
		AggregateID: workerID,
		Type:        models.RuntimeEventWorkerOffline,
		Payload:     payload,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := m.appendAndApply(ctx, streamWorker(workerID), -1, event)
	return err
}

func (m *Manager) ListWorkers() []*models.WorkerNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*models.WorkerNode, 0, len(m.workers))
	for _, w := range m.workers {
		cp := *w
		if len(w.Capabilities) > 0 {
			cp.Capabilities = make(map[string]string, len(w.Capabilities))
			for k, v := range w.Capabilities {
				cp.Capabilities[k] = v
			}
		}
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) appendAndApply(ctx context.Context, stream string, expectedVersion int64, ev models.RuntimeEvent) ([]models.RuntimeEvent, error) {
	applied, err := m.eventStore.Append(ctx, stream, expectedVersion, ev)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	for _, one := range applied {
		m.applyLocked(one)
	}
	m.mu.Unlock()
	return applied, nil
}

func (m *Manager) applyLocked(ev models.RuntimeEvent) {
	switch ev.Type {
	case models.RuntimeEventSubmitted:
		m.applyTaskSubmitted(ev)
	case models.RuntimeEventAccepted, models.RuntimeEventReady, models.RuntimeEventLeased, models.RuntimeEventStarted,
		models.RuntimeEventProgress, models.RuntimeEventRetried, models.RuntimeEventRetryScheduled,
		models.RuntimeEventSucceeded, models.RuntimeEventFailed, models.RuntimeEventCancelled,
		models.RuntimeEventTimedOut, models.RuntimeEventCompensationTriggered, models.RuntimeEventCompensated:
		m.applyTaskTransition(ev)
	case models.RuntimeEventWorkerRegistered, models.RuntimeEventWorkerHeartbeat, models.RuntimeEventWorkerOffline, models.RuntimeEventWorkerHeartbeatMissed:
		m.applyWorkerEvent(ev)
	case models.RuntimeEventWorkerAudit, models.RuntimeEventInvalidTransitionRejected:
		// Keep read-model version monotonic even when event does not change status.
		if task := m.tasks[ev.AggregateID]; task != nil {
			task.Version = ev.AggregateVersion
			task.UpdatedAt = ev.CreatedAt
		}
	}
}

func (m *Manager) applyTaskSubmitted(ev models.RuntimeEvent) {
	var payload struct {
		Task           *models.Task `json:"task"`
		WorkflowID     string       `json:"workflow_id"`
		IdempotencyKey string       `json:"idempotency_key"`
	}
	if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.Task == nil {
		return
	}
	task := payload.Task
	task.ID = ev.AggregateID
	task.Version = ev.AggregateVersion
	if task.CreatedAt.IsZero() {
		task.CreatedAt = ev.CreatedAt
	}
	task.UpdatedAt = ev.CreatedAt
	if task.Status == "" {
		task.Status = models.StatusPending
	}
	if task.RemainingDeps == 0 && len(task.DependsOn) > 0 {
		task.RemainingDeps = len(task.DependsOn)
	}
	if task.WorkflowID == "" {
		task.WorkflowID = payload.WorkflowID
	}
	m.tasks[task.ID] = cloneTask(task)

	wfID := task.WorkflowID
	if wfID == "" {
		wfID = payload.WorkflowID
	}
	if wfID != "" {
		wf := m.workflows[wfID]
		if wf == nil {
			wf = &workflowState{ID: wfID, CreatedAt: ev.CreatedAt, IdempotencyKey: payload.IdempotencyKey}
			m.workflows[wfID] = wf
		}
		wf.TaskIDs = appendUnique(wf.TaskIDs, task.ID)
		if payload.IdempotencyKey != "" {
			m.idempotency[payload.IdempotencyKey] = idempotencyState{WorkflowID: wfID, ExpiresAt: ev.CreatedAt.Add(defaultIdempotencyWindow)}
		}
	}

	for _, dep := range task.DependsOn {
		m.dependents[dep] = appendUnique(m.dependents[dep], task.ID)
	}
}

func (m *Manager) applyTaskTransition(ev models.RuntimeEvent) {
	task := m.tasks[ev.AggregateID]
	if task == nil {
		return
	}

	payload := make(map[string]json.RawMessage)
	_ = json.Unmarshal(ev.Payload, &payload)

	status := statusFromEvent(ev.Type)
	if raw, ok := payload["status"]; ok {
		_ = json.Unmarshal(raw, &status)
	}
	if status != "" {
		task.Status = status
	}

	if raw, ok := payload["error"]; ok {
		var errMsg string
		_ = json.Unmarshal(raw, &errMsg)
		task.Error = errMsg
	}
	if raw, ok := payload["result"]; ok {
		task.Result = cloneRaw(raw)
	}
	if raw, ok := payload["progress"]; ok {
		task.Progress = cloneRaw(raw)
	}
	if raw, ok := payload["handle_id"]; ok {
		var handle string
		_ = json.Unmarshal(raw, &handle)
		task.HandleID = handle
	}
	if raw, ok := payload["attempt"]; ok {
		var attempt int
		_ = json.Unmarshal(raw, &attempt)
		task.Attempt = attempt
	}
	if raw, ok := payload["lease_owner"]; ok {
		var owner string
		_ = json.Unmarshal(raw, &owner)
		task.LeaseOwner = owner
	}
	if raw, ok := payload["lease_until"]; ok {
		var until time.Time
		_ = json.Unmarshal(raw, &until)
		task.LeaseUntil = until
	}

	task.Version = ev.AggregateVersion
	task.UpdatedAt = ev.CreatedAt
	m.applyRuntimeEnvelope(task, status, ev.CreatedAt)
}

func (m *Manager) applyWorkerEvent(ev models.RuntimeEvent) {
	payload := struct {
		WorkerID     string            `json:"worker_id"`
		Capabilities map[string]string `json:"capabilities"`
		Status       string            `json:"status"`
	}{}
	_ = json.Unmarshal(ev.Payload, &payload)
	if payload.WorkerID == "" {
		payload.WorkerID = ev.AggregateID
	}
	if payload.WorkerID == "" {
		return
	}

	node := m.workers[payload.WorkerID]
	if node == nil {
		node = &models.WorkerNode{ID: payload.WorkerID, CreatedAt: ev.CreatedAt}
		m.workers[payload.WorkerID] = node
	}
	node.LastSeenAt = ev.CreatedAt
	node.UpdatedAt = ev.CreatedAt
	if payload.Status != "" {
		node.Status = payload.Status
	}
	if len(payload.Capabilities) > 0 {
		node.Capabilities = make(map[string]string, len(payload.Capabilities))
		for k, v := range payload.Capabilities {
			node.Capabilities[k] = v
		}
	}
	if node.Status == "" {
		node.Status = "online"
	}
}

func eventTypeFromStatus(status models.TaskStatus) models.RuntimeEventType {
	switch status {
	case models.StatusReady:
		return models.RuntimeEventReady
	case models.StatusLeased:
		return models.RuntimeEventLeased
	case models.StatusRunning:
		return models.RuntimeEventStarted
	case models.StatusRetrying:
		return models.RuntimeEventRetryScheduled
	case models.StatusSuccess:
		return models.RuntimeEventSucceeded
	case models.StatusFailed:
		return models.RuntimeEventFailed
	case models.StatusCancelled:
		return models.RuntimeEventCancelled
	case models.StatusTimedOut:
		return models.RuntimeEventTimedOut
	case models.StatusCompensating:
		return models.RuntimeEventCompensationTriggered
	case models.StatusCompensated:
		return models.RuntimeEventCompensated
	default:
		return models.RuntimeEventProgress
	}
}

func statusFromEvent(eventType models.RuntimeEventType) models.TaskStatus {
	switch eventType {
	case models.RuntimeEventReady:
		return models.StatusReady
	case models.RuntimeEventLeased:
		return models.StatusLeased
	case models.RuntimeEventStarted:
		return models.StatusRunning
	case models.RuntimeEventRetryScheduled, models.RuntimeEventRetried:
		return models.StatusRetrying
	case models.RuntimeEventSucceeded:
		return models.StatusSuccess
	case models.RuntimeEventFailed:
		return models.StatusFailed
	case models.RuntimeEventCancelled:
		return models.StatusCancelled
	case models.RuntimeEventTimedOut:
		return models.StatusTimedOut
	case models.RuntimeEventCompensationTriggered:
		return models.StatusCompensating
	case models.RuntimeEventCompensated:
		return models.StatusCompensated
	default:
		return ""
	}
}

func streamTask(taskID string) string     { return "task:" + taskID }
func streamWorker(workerID string) string { return "worker:" + workerID }

func appendUnique(items []string, value string) []string {
	for _, one := range items {
		if one == value {
			return items
		}
	}
	return append(items, value)
}

func cloneTask(in *models.Task) *models.Task {
	if in == nil {
		return nil
	}
	cp := *in
	cp.DependsOn = append([]string(nil), in.DependsOn...)
	cp.CompensateWith = append([]string(nil), in.CompensateWith...)
	cp.Params = cloneRaw(in.Params)
	cp.Input = cloneRaw(in.Input)
	cp.Progress = cloneRaw(in.Progress)
	cp.Result = cloneRaw(in.Result)
	if in.Runtime != nil {
		r := *in.Runtime
		if in.Runtime.Error != nil {
			e := *in.Runtime.Error
			r.Error = &e
		}
		cp.Runtime = &r
	}
	if in.ScheduledAt != nil {
		t := *in.ScheduledAt
		cp.ScheduledAt = &t
	}
	return &cp
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cp := make(json.RawMessage, len(raw))
	copy(cp, raw)
	return cp
}

func (m *Manager) applyRuntimeEnvelope(task *models.Task, status models.TaskStatus, ts time.Time) {
	if task == nil {
		return
	}
	if status == models.StatusSkipped {
		task.Runtime = nil
		task.RunStatus = ""
		return
	}
	if task.Runtime == nil {
		task.Runtime = &models.RuntimeResult{}
	}
	r := task.Runtime
	if status == models.StatusRunning {
		r.Status = models.RuntimeRunning
		if r.StartedAt == nil {
			started := ts
			r.StartedAt = &started
		}
		if task.Attempt > 0 {
			r.Attempt = task.Attempt
		}
		task.RunStatus = string(models.RuntimeRunning)
		return
	}
	if !status.IsTerminal() && status != models.StatusRetrying {
		return
	}

	switch status {
	case models.StatusSuccess:
		r.Status = models.RuntimeSuccess
	case models.StatusCancelled:
		r.Status = models.RuntimeCancelled
	default:
		r.Status = models.RuntimeFailed
	}
	if r.StartedAt == nil {
		started := task.CreatedAt
		if started.IsZero() {
			started = ts
		}
		r.StartedAt = &started
	}
	finished := ts
	r.FinishedAt = &finished
	r.DurationMS = finished.Sub(*r.StartedAt).Milliseconds()
	r.Attempt = maxInt(r.Attempt, task.Attempt)
	if len(task.Result) > 0 {
		r.Output = cloneRaw(task.Result)
	}
	if task.Error != "" {
		code := models.ErrorExternalFailure
		retryable := status == models.StatusRetrying
		switch {
		case strings.Contains(strings.ToLower(task.Error), "deadline exceeded"):
			code = models.ErrorTimeout
			retryable = true
		case strings.Contains(strings.ToLower(task.Error), "canceled"), strings.Contains(strings.ToLower(task.Error), "cancelled"):
			code = models.ErrorCancelled
			retryable = false
		}
		r.Error = &models.RuntimeError{
			Code:      code,
			Message:   task.Error,
			Retryable: retryable,
			Source:    "worker",
		}
	}
	task.RunStatus = string(r.Status)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
