package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"phobos.org.uk/agency/internal/api"
)

// Queue-specific task states
const (
	TaskStatePending     = "pending"     // In queue, waiting for agent
	TaskStateDispatching = "dispatching" // Being sent to agent
	TaskStateWorking     = "working"     // Running on agent
	TaskStateCompleted   = "completed"   // Finished successfully
	TaskStateFailed      = "failed"      // Failed
	TaskStateCancelled   = "cancelled"   // Cancelled
)

// Persistence directory names
const (
	dirPending    = "pending"
	dirDispatched = "dispatched"
)

// ErrQueueFull is returned when the queue is at capacity
var ErrQueueFull = errors.New("queue is at capacity")

// QueuedTask represents a task waiting in the queue
type QueuedTask struct {
	QueueID   string    `json:"queue_id"`   // Unique queue entry ID
	State     string    `json:"state"`      // pending, dispatching, working, etc.
	CreatedAt time.Time `json:"created_at"` // Queue entry time

	// Original request
	Prompt         string            `json:"prompt"`
	Tier           string            `json:"tier,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	AgentKind      string            `json:"agent_kind,omitempty"`

	// Dispatch tracking
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"` // When sent to agent
	TaskID       string     `json:"task_id,omitempty"`       // Agent's task ID (once dispatched)
	AgentURL     string     `json:"agent_url,omitempty"`     // Target agent (once dispatched)
	Attempts     int        `json:"attempts"`                // Dispatch attempt count
	LastError    string     `json:"last_error,omitempty"`    // Most recent error

	// Source tracking
	Source    string `json:"source"`               // "web", "scheduler", "cli"
	SourceJob string `json:"source_job,omitempty"` // Job name (if scheduler)
}

// QueueConfig defines queue behavior
type QueueConfig struct {
	Dir             string        // Persistence directory
	MaxSize         int           // Maximum queue depth (default: 50)
	MaxAttempts     int           // Retry limit per task (default: 3)
	DispatchTimeout time.Duration // Time to wait for agent response (default: 30s)
}

const (
	DefaultMaxSize         = 50
	DefaultMaxAttempts     = 3
	DefaultDispatchTimeout = 30 * time.Second
)

// WorkQueue manages pending tasks with file-based persistence
type WorkQueue struct {
	mu     sync.RWMutex
	tasks  []*QueuedTask          // FIFO order
	byID   map[string]*QueuedTask // Quick lookup by queue_id
	dir    string                 // Persistence directory
	config QueueConfig
}

// NewWorkQueue creates a new work queue with persistence
func NewWorkQueue(cfg QueueConfig) (*WorkQueue, error) {
	if cfg.MaxSize == 0 {
		cfg.MaxSize = DefaultMaxSize
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	if cfg.DispatchTimeout == 0 {
		cfg.DispatchTimeout = DefaultDispatchTimeout
	}

	q := &WorkQueue{
		tasks:  make([]*QueuedTask, 0),
		byID:   make(map[string]*QueuedTask),
		dir:    cfg.Dir,
		config: cfg,
	}

	// Create directories
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "pending"), 0700); err != nil {
		return nil, fmt.Errorf("creating pending directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "dispatched"), 0700); err != nil {
		return nil, fmt.Errorf("creating dispatched directory: %w", err)
	}

	// Load existing tasks from disk
	if err := q.loadFromDisk(); err != nil {
		return nil, fmt.Errorf("loading queue from disk: %w", err)
	}

	return q, nil
}

// QueueSubmitRequest represents a request to add a task to the queue
type QueueSubmitRequest struct {
	Prompt         string            `json:"prompt"`
	Tier           string            `json:"tier,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Source         string            `json:"source,omitempty"`     // "web", "scheduler", "cli"
	SourceJob      string            `json:"source_job,omitempty"` // Job name (if scheduler)
	AgentKind      string            `json:"agent_kind,omitempty"`
}

// Add adds a task to the queue. Returns the task, position, and error.
func (q *WorkQueue) Add(req QueueSubmitRequest) (*QueuedTask, int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Check capacity
	pendingCount := 0
	for _, t := range q.tasks {
		if t.State == TaskStatePending {
			pendingCount++
		}
	}
	if pendingCount >= q.config.MaxSize {
		return nil, 0, ErrQueueFull
	}

	// Generate queue ID
	queueID := fmt.Sprintf("queue-%d", time.Now().UnixNano())

	agentKind := req.AgentKind
	if agentKind == "" {
		agentKind = api.AgentKindClaude
	}

	task := &QueuedTask{
		QueueID:        queueID,
		State:          TaskStatePending,
		CreatedAt:      time.Now(),
		Prompt:         req.Prompt,
		Tier:           req.Tier,
		TimeoutSeconds: req.TimeoutSeconds,
		SessionID:      req.SessionID,
		Env:            req.Env,
		AgentKind:      agentKind,
		Source:         req.Source,
		SourceJob:      req.SourceJob,
		Attempts:       0,
	}

	q.tasks = append(q.tasks, task)
	q.byID[task.QueueID] = task

	// Persist to disk
	if err := q.save(task); err != nil {
		// Log but don't fail - task is in memory
		fmt.Fprintf(os.Stderr, "queue: failed to persist task %s: %v\n", task.QueueID, err)
	}

	// Calculate position (1-indexed)
	position := 0
	for i, t := range q.tasks {
		if t.State == TaskStatePending {
			position++
			if t.QueueID == task.QueueID {
				return task, position, nil
			}
		}
		if t.QueueID == task.QueueID {
			return task, i + 1, nil
		}
	}

	return task, len(q.tasks), nil
}

// NextPending returns the next pending task without removing it
func (q *WorkQueue) NextPending() *QueuedTask {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, task := range q.tasks {
		if task.State == TaskStatePending {
			return task
		}
	}
	return nil
}

// Get returns a task by queue ID
func (q *WorkQueue) Get(queueID string) *QueuedTask {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.byID[queueID]
}

// GetAll returns all tasks in the queue
func (q *WorkQueue) GetAll() []*QueuedTask {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result := make([]*QueuedTask, len(q.tasks))
	copy(result, q.tasks)
	return result
}

// Depth returns the current queue depth (pending tasks only)
func (q *WorkQueue) Depth() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	count := 0
	for _, t := range q.tasks {
		if t.State == TaskStatePending {
			count++
		}
	}
	return count
}

// TotalCount returns total tasks (pending + dispatched)
func (q *WorkQueue) TotalCount() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.tasks)
}

// SetState updates a task's state
func (q *WorkQueue) SetState(task *QueuedTask, state string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	task.State = state
	if err := q.save(task); err != nil {
		fmt.Fprintf(os.Stderr, "queue: failed to save task %s: %v\n", task.QueueID, err)
	}
}

// SetDispatched marks a task as dispatched with agent info
func (q *WorkQueue) SetDispatched(task *QueuedTask, agentURL, taskID, sessionID string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	task.State = TaskStateWorking
	task.DispatchedAt = &now
	task.AgentURL = agentURL
	task.TaskID = taskID
	if sessionID != "" {
		task.SessionID = sessionID
	}

	// Move file from pending to dispatched
	q.moveToDir(task, "dispatched")
}

// RequeueAtBack moves a task to the back of the queue
func (q *WorkQueue) RequeueAtBack(task *QueuedTask) {
	q.mu.Lock()
	defer q.mu.Unlock()

	task.State = TaskStatePending
	task.DispatchedAt = nil
	task.TaskID = ""
	task.AgentURL = ""

	// Remove from current position
	for i, t := range q.tasks {
		if t.QueueID == task.QueueID {
			q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
			break
		}
	}

	// Add to back
	q.tasks = append(q.tasks, task)

	// Move file back to pending
	q.moveToDir(task, "pending")
}

// Remove removes a task from the queue
func (q *WorkQueue) Remove(task *QueuedTask) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.byID, task.QueueID)
	for i, t := range q.tasks {
		if t.QueueID == task.QueueID {
			q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
			break
		}
	}

	// Remove from disk
	q.removeFile(task)
}

// Cancel cancels a queued task. Returns true if found and cancelled.
func (q *WorkQueue) Cancel(queueID string) (*QueuedTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	task, ok := q.byID[queueID]
	if !ok {
		return nil, false
	}

	task.State = TaskStateCancelled
	delete(q.byID, task.QueueID)
	for i, t := range q.tasks {
		if t.QueueID == queueID {
			q.tasks = append(q.tasks[:i], q.tasks[i+1:]...)
			break
		}
	}

	q.removeFile(task)
	return task, true
}

// Position returns the position of a task in the pending queue (1-indexed)
func (q *WorkQueue) Position(queueID string) int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	pos := 0
	for _, t := range q.tasks {
		if t.State == TaskStatePending {
			pos++
			if t.QueueID == queueID {
				return pos
			}
		}
	}
	return 0 // Not found or not pending
}

// OldestAge returns the age of the oldest pending task in seconds
func (q *WorkQueue) OldestAge() float64 {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, t := range q.tasks {
		if t.State == TaskStatePending {
			return time.Since(t.CreatedAt).Seconds()
		}
	}
	return 0
}

// DispatchedCount returns the number of dispatched (working) tasks
func (q *WorkQueue) DispatchedCount() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	count := 0
	for _, t := range q.tasks {
		if t.State == TaskStateWorking || t.State == TaskStateDispatching {
			count++
		}
	}
	return count
}

// Config returns the queue configuration
func (q *WorkQueue) Config() QueueConfig {
	return q.config
}

// Persistence methods

func (q *WorkQueue) save(task *QueuedTask) error {
	dir := dirPending
	if task.State == TaskStateDispatching || task.State == TaskStateWorking {
		dir = dirDispatched
	}
	path := filepath.Join(q.dir, dir, task.QueueID+".json")
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (q *WorkQueue) moveToDir(task *QueuedTask, targetDir string) {
	// Remove from both directories (one will fail, that's ok)
	for _, dir := range []string{dirPending, dirDispatched} {
		path := filepath.Join(q.dir, dir, task.QueueID+".json")
		os.Remove(path)
	}

	// Save to target directory
	path := filepath.Join(q.dir, targetDir, task.QueueID+".json")
	data, _ := json.MarshalIndent(task, "", "  ")
	os.WriteFile(path, data, 0600)
}

func (q *WorkQueue) removeFile(task *QueuedTask) {
	for _, dir := range []string{dirPending, dirDispatched} {
		path := filepath.Join(q.dir, dir, task.QueueID+".json")
		os.Remove(path)
	}
}

func (q *WorkQueue) loadFromDisk() error {
	// Load dispatched tasks first
	dispatchedDir := filepath.Join(q.dir, dirDispatched)
	entries, _ := os.ReadDir(dispatchedDir)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		task, err := q.loadTask(filepath.Join(dispatchedDir, entry.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "queue: failed to load %s: %v\n", entry.Name(), err)
			continue
		}
		// Dispatched tasks that were in-flight during restart go back to pending
		// (We can't verify with agent since we don't have discovery yet)
		task.State = TaskStatePending
		task.TaskID = ""
		task.AgentURL = ""
		task.DispatchedAt = nil
		q.tasks = append(q.tasks, task)
		q.byID[task.QueueID] = task
		// Move file to pending
		q.moveToDir(task, dirPending)
	}

	// Load pending tasks
	pendingDir := filepath.Join(q.dir, dirPending)
	entries, _ = os.ReadDir(pendingDir)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// Skip if already loaded from dispatched (moved)
		queueID := entry.Name()[:len(entry.Name())-5] // Remove .json
		if _, exists := q.byID[queueID]; exists {
			continue
		}
		task, err := q.loadTask(filepath.Join(pendingDir, entry.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "queue: failed to load %s: %v\n", entry.Name(), err)
			continue
		}
		q.tasks = append(q.tasks, task)
		q.byID[task.QueueID] = task
	}

	// Sort by created_at for FIFO
	sort.Slice(q.tasks, func(i, j int) bool {
		return q.tasks[i].CreatedAt.Before(q.tasks[j].CreatedAt)
	})

	if len(q.tasks) > 0 {
		fmt.Fprintf(os.Stderr, "queue: loaded %d tasks from disk\n", len(q.tasks))
	}

	return nil
}

func (q *WorkQueue) loadTask(path string) (*QueuedTask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var task QueuedTask
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	return &task, nil
}
