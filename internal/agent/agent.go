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
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"phobos.org.uk/agency/internal/api"
	"phobos.org.uk/agency/internal/config"
	"phobos.org.uk/agency/internal/history"
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
	Thinking        bool                `json:"-"` // Enable extended thinking mode
	TokenUsage      *TokenUsage         `json:"token_usage,omitempty"`
	DurationSeconds float64             `json:"duration_seconds,omitempty"`

	maxTurnsResumes int // Number of auto-resumes due to max_turns limit
	cmd             *exec.Cmd
	cancel          context.CancelFunc
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
	Thinking       *bool               `json:"thinking,omitempty"` // Enable extended thinking (default: true)
}

// StatusResponse represents the /status response
type StatusResponse struct {
	Type          string           `json:"type"`
	Interfaces    []string         `json:"interfaces"`
	Version       string           `json:"version"`
	State         State            `json:"state"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	CurrentTask   *api.CurrentTask `json:"current_task"`
	Config        StatusConfig     `json:"config"`
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
	preprompt string // Preprompt instructions loaded at startup
	history   *history.Store

	mu          sync.RWMutex
	state       State
	currentTask *Task
	tasks       map[string]*Task

	server   *http.Server
	shutdown chan struct{}
}

// New creates a new Agent
func New(cfg *config.Config, version string) *Agent {
	// Load preprompt: try custom file first, fallback to embedded default
	preprompt := agentClaudeMD
	if cfg.PrepromptFile != "" {
		if data, err := os.ReadFile(cfg.PrepromptFile); err == nil {
			preprompt = string(data)
			fmt.Fprintf(os.Stderr, "Loaded preprompt from %s\n", cfg.PrepromptFile)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: failed to load preprompt file %s: %v (using default)\n", cfg.PrepromptFile, err)
		}
	}

	// Initialize history store
	var historyStore *history.Store
	if cfg.HistoryDir != "" {
		var err error
		historyStore, err = history.NewStore(cfg.HistoryDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to initialize history store: %v\n", err)
		}
	}

	return &Agent{
		config:    cfg,
		version:   version,
		startTime: time.Now(),
		preprompt: preprompt,
		history:   historyStore,
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

	// History endpoints
	r.Get("/history", a.handleListHistory)
	r.Get("/history/{id}", a.handleGetHistory)
	r.Get("/history/{id}/debug", a.handleGetHistoryDebug)

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

	if a.currentTask != nil && a.currentTask.StartedAt != nil {
		preview := a.currentTask.Prompt
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		resp.CurrentTask = &api.CurrentTask{
			ID:            a.currentTask.ID,
			StartedAt:     a.currentTask.StartedAt.Format(time.RFC3339),
			PromptPreview: preview,
		}
	}

	api.WriteJSON(w, http.StatusOK, resp)
}

// handleCreateTask validates and queues a new task for execution.
// Returns 201 Created with task_id on success.
// Returns 400 if validation fails, 409 if agent is busy.
func (a *Agent) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.Prompt == "" {
		api.WriteError(w, http.StatusBadRequest, "validation_error", "prompt is required")
		return
	}

	a.mu.Lock()
	if a.state != StateIdle {
		currentTaskID := ""
		if a.currentTask != nil {
			currentTaskID = a.currentTask.ID
		}
		a.mu.Unlock()
		api.WriteJSON(w, http.StatusConflict, map[string]interface{}{
			"error":        api.ErrorAgentBusy,
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

	// Default thinking to true if not specified
	thinking := true
	if req.Thinking != nil {
		thinking = *req.Thinking
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
		Thinking:      thinking,
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

	// Copy fields needed for response before releasing lock
	taskID := task.ID
	respSessionID := task.SessionID
	a.mu.Unlock()

	// Start task execution in background
	go a.executeTask(task, req.Env)

	api.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"task_id":    taskID,
		"session_id": respSessionID,
		"status":     "working",
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
		api.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Task %s not found", taskID))
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

	api.WriteJSON(w, http.StatusOK, resp)
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
		api.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Task %s not found", taskID))
		return
	}

	if task.State == TaskStateCompleted || task.State == TaskStateFailed || task.State == TaskStateCancelled {
		a.mu.Unlock()
		api.WriteJSON(w, http.StatusConflict, map[string]interface{}{
			"error":       api.ErrorAlreadyCompleted,
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

	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
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

	// Ignore decode errors - defaults (TimeoutSeconds=30, Force=false) are safe
	_ = json.NewDecoder(r.Body).Decode(&req)

	a.mu.RLock()
	hasTask := a.currentTask != nil && a.state == StateWorking
	taskID := ""
	if a.currentTask != nil {
		taskID = a.currentTask.ID
	}
	a.mu.RUnlock()

	if hasTask && !req.Force {
		api.WriteJSON(w, http.StatusConflict, map[string]interface{}{
			"error":   api.ErrorTaskInProgress,
			"message": fmt.Sprintf("Task %s is running. Use force=true to terminate.", taskID),
			"task_id": taskID,
		})
		return
	}

	api.WriteJSON(w, http.StatusAccepted, map[string]interface{}{
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
// Auto-resumes up to 2 times if Claude hits the max_turns limit.
func (a *Agent) executeTask(task *Task, env map[string]string) {
	// All task field access must happen under the lock to avoid races with Shutdown()
	a.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout)
	task.cancel = cancel
	now := time.Now()
	task.StartedAt = &now
	task.State = TaskStateWorking
	a.mu.Unlock()

	defer cancel()

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

	const maxAutoResumes = 2
	var lastOutput []byte

	// Execution loop: runs once normally, up to 2 more times for max_turns auto-resume
	for {
		args := a.buildClaudeArgs(task)

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
		lastOutput = stdout.Bytes()
		completedAt := time.Now()
		task.CompletedAt = &completedAt
		task.DurationSeconds = completedAt.Sub(*task.StartedAt).Seconds()

		a.mu.Lock()

		// Handle cancellation: context was canceled and task was marked cancelled
		if ctx.Err() == context.Canceled && task.State == TaskStateCancelled {
			a.state = StateIdle
			a.currentTask = nil
			a.mu.Unlock()
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
			a.mu.Unlock()
			return
		}

		// Parse Claude's JSON output; fall back to raw stdout if not valid JSON
		var claudeResp struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
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

			// Check for max_turns limit and auto-resume if possible
			if claudeResp.Subtype == "error_max_turns" && task.maxTurnsResumes < maxAutoResumes {
				task.maxTurnsResumes++
				task.ResumeSession = true
				fmt.Fprintf(os.Stderr, "[agent] Task %s hit max_turns limit, auto-resuming (attempt %d/%d)\n",
					task.ID, task.maxTurnsResumes+1, maxAutoResumes+1)
				a.mu.Unlock()
				continue // Retry with resume
			}

			// If max_turns exhausted after all retries, fail with clear error
			if claudeResp.Subtype == "error_max_turns" {
				task.State = TaskStateFailed
				task.Error = &TaskError{
					Type: "max_turns",
					Message: fmt.Sprintf("Task exceeded maximum turns limit (%d turns x %d attempts). Consider breaking the task into smaller steps.",
						a.config.Claude.MaxTurns, maxAutoResumes+1),
				}
				a.saveTaskHistory(task, lastOutput)
				a.state = StateIdle
				a.currentTask = nil
				a.mu.Unlock()
				return
			}
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

		// Save to history and complete
		a.saveTaskHistory(task, lastOutput)
		a.state = StateIdle
		a.currentTask = nil
		a.mu.Unlock()
		return
	}
}

// buildClaudeArgs constructs the command-line arguments for the Claude CLI.
// It uses "--" to separate options from the prompt, preventing prompts that
// start with dashes from being interpreted as flags.
func (a *Agent) buildClaudeArgs(task *Task) []string {
	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--model", task.Model,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(a.config.Claude.MaxTurns),
	}

	// Note: Extended thinking is enabled by default in Claude CLI for compatible models.
	// There is no CLI flag to control it; it's determined by the model's capabilities.

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
	prompt := a.preprompt + "\n\n" + task.Prompt
	if task.Project != nil && task.Project.Prompt != "" {
		prompt = a.preprompt + "\n\n" + task.Project.Prompt + "\n\n" + task.Prompt
	}

	args = append(args, "-p", "--", prompt)
	return args
}

// saveTaskHistory saves a completed task to the history store.
func (a *Agent) saveTaskHistory(task *Task, rawOutput []byte) {
	if a.history == nil {
		return
	}

	entry := &history.Entry{
		TaskID:          task.ID,
		SessionID:       task.SessionID,
		State:           string(task.State),
		Prompt:          task.Prompt,
		Model:           task.Model,
		Output:          task.Output,
		DurationSeconds: task.DurationSeconds,
		ExitCode:        task.ExitCode,
		Steps:           history.ExtractSteps(rawOutput),
	}

	if task.StartedAt != nil {
		entry.StartedAt = *task.StartedAt
	}
	if task.CompletedAt != nil {
		entry.CompletedAt = *task.CompletedAt
	}
	if task.Error != nil {
		entry.Error = &history.EntryError{
			Type:    task.Error.Type,
			Message: task.Error.Message,
		}
	}
	if task.TokenUsage != nil {
		entry.TokenUsage = &history.TokenUsage{
			Input:  task.TokenUsage.Input,
			Output: task.TokenUsage.Output,
		}
	}

	if err := a.history.Save(entry); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save task history: %v\n", err)
	}

	// Save debug log (raw Claude output)
	if len(rawOutput) > 0 {
		if err := a.history.SaveDebugLog(task.ID, rawOutput); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save debug log: %v\n", err)
		}
	}
}

// handleListHistory returns paginated task history.
func (a *Agent) handleListHistory(w http.ResponseWriter, r *http.Request) {
	if a.history == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "history_unavailable", "History storage not configured")
		return
	}

	// Parse pagination params
	page := 1
	limit := 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	result := a.history.List(history.ListOptions{
		Page:  page,
		Limit: limit,
	})

	api.WriteJSON(w, http.StatusOK, result)
}

// handleGetHistory returns a single history entry with outline.
func (a *Agent) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	if a.history == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "history_unavailable", "History storage not configured")
		return
	}

	taskID := chi.URLParam(r, "id")
	entry, err := a.history.Get(taskID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	api.WriteJSON(w, http.StatusOK, entry)
}

// handleGetHistoryDebug returns the full debug log for a task.
func (a *Agent) handleGetHistoryDebug(w http.ResponseWriter, r *http.Request) {
	if a.history == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "history_unavailable", "History storage not configured")
		return
	}

	taskID := chi.URLParam(r, "id")
	debugLog, err := a.history.GetDebugLog(taskID)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(debugLog)
}
