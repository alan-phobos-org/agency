package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/agency/internal/api"
	"github.com/anthropics/agency/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

//go:embed claude.md
var agentClaudeMD string

// State represents the agent's current state
type State string

const (
	StateIdle       State = "idle"
	StateWorking    State = "working"
	StateCancelling State = "cancelling"
)

// TaskState represents a task's state
type TaskState string

const (
	TaskStateQueued    TaskState = "queued"
	TaskStateWorking   TaskState = "working"
	TaskStateCompleted TaskState = "completed"
	TaskStateFailed    TaskState = "failed"
	TaskStateCancelled TaskState = "cancelled"
)

// Task represents a task execution
type Task struct {
	ID              string              `json:"task_id"`
	State           TaskState           `json:"state"`
	Prompt          string              `json:"-"`
	Model           string              `json:"-"`
	Timeout         time.Duration       `json:"-"`
	StartedAt       *time.Time          `json:"started_at,omitempty"`
	CompletedAt     *time.Time          `json:"completed_at,omitempty"`
	ExitCode        *int                `json:"exit_code,omitempty"`
	Output          string              `json:"output,omitempty"`
	Error           *TaskError          `json:"error,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	ResumeSession   bool                `json:"-"` // True if continuing an existing session
	WorkDir         string              `json:"-"` // Working directory for task execution
	Project         *api.ProjectContext `json:"-"` // Project context for prompt prepending
	TokenUsage      *TokenUsage         `json:"token_usage,omitempty"`
	DurationSeconds float64             `json:"duration_seconds,omitempty"`

	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// TaskError represents an error during task execution
type TaskError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// TokenUsage represents Claude token usage
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// TaskRequest represents a task submission request
type TaskRequest struct {
	Prompt         string              `json:"prompt"`
	Model          string              `json:"model,omitempty"`
	TimeoutSeconds int                 `json:"timeout_seconds,omitempty"`
	SessionID      string              `json:"session_id,omitempty"`
	Project        *api.ProjectContext `json:"project,omitempty"`
	Env            map[string]string   `json:"env,omitempty"`
}

// StatusResponse represents the /status response
type StatusResponse struct {
	Type          string       `json:"type"`
	Interfaces    []string     `json:"interfaces"`
	Version       string       `json:"version"`
	State         State        `json:"state"`
	UptimeSeconds float64      `json:"uptime_seconds"`
	CurrentTask   *CurrentTask `json:"current_task"`
	Config        StatusConfig `json:"config"`
}

// CurrentTask shows info about the running task
type CurrentTask struct {
	ID            string `json:"id"`
	StartedAt     string `json:"started_at"`
	PromptPreview string `json:"prompt_preview"`
}

// StatusConfig shows agent config in status
type StatusConfig struct {
	Port  int    `json:"port"`
	Model string `json:"model"`
}

// Agent is the main agent server
type Agent struct {
	config    *config.Config
	version   string
	startTime time.Time

	mu          sync.RWMutex
	state       State
	currentTask *Task
	tasks       map[string]*Task

	server   *http.Server
	shutdown chan struct{}
}

// New creates a new Agent
func New(cfg *config.Config, version string) *Agent {
	return &Agent{
		config:    cfg,
		version:   version,
		startTime: time.Now(),
		state:     StateIdle,
		tasks:     make(map[string]*Task),
		shutdown:  make(chan struct{}),
	}
}

// Router returns the HTTP router
func (a *Agent) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	r.Get("/status", a.handleStatus)
	r.Post("/task", a.handleCreateTask)
	r.Get("/task/{id}", a.handleGetTask)
	r.Post("/task/{id}/cancel", a.handleCancelTask)
	r.Post("/shutdown", a.handleShutdown)

	return r
}

// Start starts the agent server
func (a *Agent) Start() error {
	addr := fmt.Sprintf(":%d", a.config.Port)
	a.server = &http.Server{
		Addr:    addr,
		Handler: a.Router(),
	}

	fmt.Fprintf(os.Stderr, "Agent starting on %s\n", addr)
	return a.server.ListenAndServe()
}

// Shutdown gracefully shuts down the agent
func (a *Agent) Shutdown(ctx context.Context) error {
	close(a.shutdown)

	// Cancel any running task
	a.mu.Lock()
	if a.currentTask != nil && a.currentTask.cancel != nil {
		a.currentTask.cancel()
	}
	a.mu.Unlock()

	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}

// handleStatus returns the agent's current state, version, uptime, and config.
// If a task is running, includes a preview of the current task.
func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	resp := StatusResponse{
		Type:          api.TypeAgent,
		Interfaces:    []string{api.InterfaceStatusable, api.InterfaceTaskable},
		Version:       a.version,
		State:         a.state,
		UptimeSeconds: time.Since(a.startTime).Seconds(),
		Config: StatusConfig{
			Port:  a.config.Port,
			Model: a.config.Claude.Model,
		},
	}

	if a.currentTask != nil {
		preview := a.currentTask.Prompt
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		resp.CurrentTask = &CurrentTask{
			ID:            a.currentTask.ID,
			StartedAt:     a.currentTask.StartedAt.Format(time.RFC3339),
			PromptPreview: preview,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleCreateTask validates and queues a new task for execution.
// Returns 201 Created with task_id on success.
// Returns 400 if validation fails, 409 if agent is busy.
func (a *Agent) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "prompt is required")
		return
	}

	a.mu.Lock()
	if a.state != StateIdle {
		currentTaskID := ""
		if a.currentTask != nil {
			currentTaskID = a.currentTask.ID
		}
		a.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":        "agent_busy",
			"message":      fmt.Sprintf("Agent is currently processing %s", currentTaskID),
			"current_task": currentTaskID,
		})
		return
	}

	// Create task with session-based working directory
	// For new sessions, generate a valid UUID session_id upfront
	// For resumed sessions, use the provided session ID
	// WorkDir is derived from session_id for consistent directory mapping
	resumeSession := req.SessionID != ""
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	task := &Task{
		ID:            "task-" + uuid.New().String()[:8],
		State:         TaskStateQueued,
		Prompt:        req.Prompt,
		Model:         req.Model,
		SessionID:     sessionID,
		ResumeSession: resumeSession,
		WorkDir:       sessionID,
		Project:       req.Project,
	}

	if task.Model == "" {
		task.Model = a.config.Claude.Model
	}

	if req.TimeoutSeconds > 0 {
		task.Timeout = time.Duration(req.TimeoutSeconds) * time.Second
	} else {
		task.Timeout = a.config.Claude.Timeout
	}

	a.tasks[task.ID] = task
	a.currentTask = task
	a.state = StateWorking
	a.mu.Unlock()

	// Start task execution in background
	go a.executeTask(task, req.Env)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"task_id":    task.ID,
		"session_id": task.SessionID,
		"status":     "queued",
	})
}

// handleGetTask returns the status and output of a task by ID.
// Returns 404 if task not found.
func (a *Agent) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")

	a.mu.RLock()
	task, ok := a.tasks[taskID]
	a.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Task %s not found", taskID))
		return
	}

	resp := map[string]interface{}{
		"task_id":          task.ID,
		"state":            task.State,
		"exit_code":        task.ExitCode,
		"output":           task.Output,
		"session_id":       task.SessionID,
		"token_usage":      task.TokenUsage,
		"duration_seconds": task.DurationSeconds,
	}

	if task.StartedAt != nil {
		resp["started_at"] = task.StartedAt.Format(time.RFC3339)
	}
	if task.CompletedAt != nil {
		resp["completed_at"] = task.CompletedAt.Format(time.RFC3339)
	}
	if task.Error != nil {
		resp["error"] = task.Error
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleCancelTask cancels a running task by ID.
// Triggers context cancellation which sends SIGTERM to the Claude process.
// Returns 404 if not found, 409 if already completed.
func (a *Agent) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")

	a.mu.Lock()
	task, ok := a.tasks[taskID]
	if !ok {
		a.mu.Unlock()
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Task %s not found", taskID))
		return
	}

	if task.State == TaskStateCompleted || task.State == TaskStateFailed || task.State == TaskStateCancelled {
		a.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":       "already_completed",
			"message":     fmt.Sprintf("Task %s has already completed", taskID),
			"final_state": task.State,
		})
		return
	}

	task.State = TaskStateCancelled
	if task.cancel != nil {
		task.cancel()
	}
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task_id": taskID,
		"state":   TaskStateCancelled,
		"message": "Task cancellation initiated",
	})
}

// handleShutdown initiates graceful agent shutdown.
// If force=false and a task is running, returns 409.
// If force=true, cancels the running task and shuts down.
func (a *Agent) handleShutdown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TimeoutSeconds int  `json:"timeout_seconds"`
		Force          bool `json:"force"`
	}
	req.TimeoutSeconds = 30

	json.NewDecoder(r.Body).Decode(&req)

	a.mu.RLock()
	hasTask := a.currentTask != nil && a.state == StateWorking
	taskID := ""
	if a.currentTask != nil {
		taskID = a.currentTask.ID
	}
	a.mu.RUnlock()

	if hasTask && !req.Force {
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":   "task_in_progress",
			"message": fmt.Sprintf("Task %s is running. Use force=true to terminate.", taskID),
			"task_id": taskID,
		})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"message":       "Shutdown initiated",
		"drain_timeout": req.TimeoutSeconds,
	})

	// Trigger shutdown in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.TimeoutSeconds)*time.Second)
		defer cancel()
		a.Shutdown(ctx)
	}()
}

// executeTask runs the Claude CLI with the given task configuration.
// It handles the full lifecycle: setup, execution, timeout/cancellation, and result parsing.
//
// The function:
//  1. Creates a timeout context based on task.Timeout
//  2. Creates/reuses session directory and executes Claude CLI
//  3. Handles three termination cases: success, timeout, or cancellation
//  4. Parses JSON output from Claude or falls back to raw stdout
//  5. Updates task state and clears agent's current task when done
//
// The env parameter allows passing additional environment variables to Claude.
func (a *Agent) executeTask(task *Task, env map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout)
	task.cancel = cancel
	defer cancel()

	now := time.Now()
	task.StartedAt = &now

	a.mu.Lock()
	task.State = TaskStateWorking
	a.mu.Unlock()

	// Create working directory: <session_dir>/<work_dir>/
	// For new sessions, clean any existing directory first
	workDir := filepath.Join(a.config.SessionDir, task.WorkDir)
	if !task.ResumeSession {
		os.RemoveAll(workDir) // Clean for new sessions
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		a.mu.Lock()
		task.State = TaskStateFailed
		task.Error = &TaskError{
			Type:    "session_error",
			Message: fmt.Sprintf("Failed to create session directory: %v", err),
		}
		a.state = StateIdle
		a.currentTask = nil
		a.mu.Unlock()
		return
	}

	// Resolve Claude binary: CLAUDE_BIN env var or "claude" from PATH
	claudeBin := os.Getenv("CLAUDE_BIN")
	if claudeBin == "" {
		claudeBin = "claude"
	}

	args := buildClaudeArgs(task)

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = workDir

	// Inherit current environment and add task-specific vars
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	task.cmd = cmd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	completedAt := time.Now()
	task.CompletedAt = &completedAt
	task.DurationSeconds = completedAt.Sub(*task.StartedAt).Seconds()

	a.mu.Lock()
	defer a.mu.Unlock()

	// Handle cancellation: context was canceled and task was marked cancelled
	if ctx.Err() == context.Canceled && task.State == TaskStateCancelled {
		a.state = StateIdle
		a.currentTask = nil
		return
	}

	// Handle timeout: context deadline exceeded
	if ctx.Err() == context.DeadlineExceeded {
		task.State = TaskStateFailed
		task.Error = &TaskError{
			Type:    "timeout",
			Message: fmt.Sprintf("Task exceeded timeout of %v", task.Timeout),
		}
		a.state = StateIdle
		a.currentTask = nil
		return
	}

	// Parse Claude's JSON output; fall back to raw stdout if not valid JSON
	var claudeResp struct {
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		ExitCode  int    `json:"exit_code"`
		Usage     struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if jsonErr := json.Unmarshal(stdout.Bytes(), &claudeResp); jsonErr == nil {
		// Only update session_id if Claude returns a non-empty value
		if claudeResp.SessionID != "" {
			task.SessionID = claudeResp.SessionID
		}
		task.Output = claudeResp.Result
		task.TokenUsage = &TokenUsage{
			Input:  claudeResp.Usage.InputTokens,
			Output: claudeResp.Usage.OutputTokens,
		}
		exitCode := claudeResp.ExitCode
		task.ExitCode = &exitCode
	} else {
		// Not valid JSON - use raw output (e.g., from mock Claude in tests)
		task.Output = stdout.String()
	}

	// Determine final state based on command execution result
	if err != nil {
		task.State = TaskStateFailed
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
		task.ExitCode = &exitCode
		task.Error = &TaskError{
			Type:    "claude_error",
			Message: stderr.String(),
		}
	} else {
		task.State = TaskStateCompleted
		exitCode := 0
		task.ExitCode = &exitCode
	}

	a.state = StateIdle
	a.currentTask = nil
}

// buildClaudeArgs constructs the command-line arguments for the Claude CLI.
// It uses "--" to separate options from the prompt, preventing prompts that
// start with dashes from being interpreted as flags.
func buildClaudeArgs(task *Task) []string {
	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--model", task.Model,
		"--output-format", "json",
		"--max-turns", "50",
	}

	// Add session handling for conversation continuity
	// For new sessions: pass --session-id to create session with our UUID
	// For resumed sessions: pass --resume to continue the existing session
	if task.SessionID != "" {
		if task.ResumeSession {
			args = append(args, "--resume", task.SessionID)
		} else {
			args = append(args, "--session-id", task.SessionID)
		}
	}

	// Build prompt with agent instructions and optional project context prepended
	prompt := agentClaudeMD + "\n\n" + task.Prompt
	if task.Project != nil && task.Project.Prompt != "" {
		prompt = agentClaudeMD + "\n\n" + task.Project.Prompt + "\n\n" + task.Prompt
	}

	args = append(args, "-p", "--", prompt)
	return args
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}
