package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"phobos.org.uk/agency/internal/api"
	"phobos.org.uk/agency/internal/config"
	"phobos.org.uk/agency/internal/history"
	"phobos.org.uk/agency/internal/logging"
	"phobos.org.uk/agency/internal/stream"
)

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
	ID              string        `json:"task_id"`
	State           TaskState     `json:"state"`
	Prompt          string        `json:"-"`
	Model           string        `json:"-"`
	Timeout         time.Duration `json:"-"`
	StartedAt       *time.Time    `json:"started_at,omitempty"`
	CompletedAt     *time.Time    `json:"completed_at,omitempty"`
	ExitCode        *int          `json:"exit_code,omitempty"`
	Output          string        `json:"output,omitempty"`
	Error           *TaskError    `json:"error,omitempty"`
	SessionID       string        `json:"session_id,omitempty"`
	ResumeSession   bool          `json:"-"` // True if continuing an existing session
	WorkDir         string        `json:"-"` // Working directory for task execution
	TokenUsage      *TokenUsage   `json:"token_usage,omitempty"`
	DurationSeconds float64       `json:"duration_seconds,omitempty"`

	maxTurnsResumes int // Number of auto-resumes due to max_turns limit
	cmd             *exec.Cmd
	cancel          context.CancelFunc
}

// TaskError represents an error during task execution
type TaskError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// TokenUsage represents token usage.
type TokenUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// TaskRequest represents a task submission request
type TaskRequest struct {
	Prompt         string            `json:"prompt"`
	Tier           string            `json:"tier,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

const maxSessionIDLen = 128

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// StatusResponse represents the /status response
type StatusResponse struct {
	Type          string           `json:"type"`
	Interfaces    []string         `json:"interfaces"`
	Version       string           `json:"version"`
	AgentKind     string           `json:"agent_kind"`
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
	history   *history.Store
	log       *logging.Logger
	runner    Runner
	agentKind string

	mu          sync.RWMutex
	state       State
	currentTask *Task
	tasks       map[string]*Task

	server   *http.Server
	shutdown chan struct{}
}

// New creates a new Agent
func New(cfg *config.Config, version string) *Agent {
	return NewWithRunner(cfg, version, NewClaudeRunner())
}

// NewWithRunner creates a new Agent with a specific CLI runner.
func NewWithRunner(cfg *config.Config, version string, runner Runner) *Agent {
	cfg.AgentKind = runner.Kind()

	// Initialize structured logger
	logLevel := logging.LevelInfo
	if lvl := os.Getenv("AGENCY_LOG_LEVEL"); lvl != "" {
		switch strings.ToLower(lvl) {
		case "debug":
			logLevel = logging.LevelDebug
		case "warn":
			logLevel = logging.LevelWarn
		case "error":
			logLevel = logging.LevelError
		}
	}
	log := logging.New(logging.Config{
		Output:     os.Stderr,
		Level:      logLevel,
		Component:  "agent",
		MaxEntries: 1000,
	})

	// Initialize history store
	var historyStore *history.Store
	if cfg.HistoryDir != "" {
		var err error
		historyStore, err = history.NewStore(cfg.HistoryDir)
		if err != nil {
			log.Warn("failed to initialize history store", map[string]any{"error": err.Error()})
		}
	}

	return &Agent{
		config:    cfg,
		version:   version,
		startTime: time.Now(),
		history:   historyStore,
		log:       log,
		runner:    runner,
		agentKind: runner.Kind(),
		state:     StateIdle,
		tasks:     make(map[string]*Task),
		shutdown:  make(chan struct{}),
	}
}

// corsMiddleware adds CORS headers for cross-origin requests from the web view
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow requests from any origin (local development)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Router returns the HTTP router
func (a *Agent) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)

	r.Get("/status", a.handleStatus)
	r.Post("/task", a.handleCreateTask)
	r.Get("/task/{id}", a.handleGetTask)
	r.Post("/task/{id}/cancel", a.handleCancelTask)
	r.Post("/shutdown", a.handleShutdown)

	// History endpoints
	r.Get("/history", a.handleListHistory)
	r.Get("/history/{id}", a.handleGetHistory)
	r.Get("/history/{id}/debug", a.handleGetHistoryDebug)

	// Logging endpoints
	r.Get("/logs", a.handleLogs)
	r.Get("/logs/stats", a.handleLogStats)

	return r
}

// Start starts the agent server
func (a *Agent) Start() error {
	addr := fmt.Sprintf(":%d", a.config.Port)

	// Setup TLS certificates
	certDir := filepath.Join(a.config.SessionDir, ".certs")
	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")

	if err := ensureTLSCert(certPath, keyPath); err != nil {
		return fmt.Errorf("ensuring TLS cert: %w", err)
	}

	a.server = &http.Server{
		Addr:      addr,
		Handler:   a.Router(),
		TLSConfig: getTLSConfig(),
	}

	a.log.Info("agent starting", map[string]any{
		"addr":    addr,
		"version": a.version,
		"model":   a.config.Claude.Model,
		"tls":     "enabled",
	})
	return a.server.ListenAndServeTLS(certPath, keyPath)
}

// Shutdown gracefully shuts down the agent
func (a *Agent) Shutdown(ctx context.Context) error {
	close(a.shutdown)

	// Cancel any running task
	a.mu.Lock()
	if a.currentTask != nil {
		if a.currentTask.cancel != nil {
			a.currentTask.cancel()
		}
		// Kill the process group to ensure clean shutdown of CLI subprocess
		if a.currentTask.cmd != nil {
			killProcessGroup(a.currentTask.cmd)
		}
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
		AgentKind:     a.agentKind,
		State:         a.state,
		UptimeSeconds: time.Since(a.startTime).Seconds(),
		Config: StatusConfig{
			Port:  a.config.Port,
			Model: a.defaultModel(),
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

func isSafeSessionID(sessionID string) bool {
	if sessionID == "" || len(sessionID) > maxSessionIDLen {
		return false
	}
	if strings.Contains(sessionID, "..") {
		return false
	}
	if strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") {
		return false
	}
	if filepath.IsAbs(sessionID) {
		return false
	}
	return sessionIDPattern.MatchString(sessionID)
}

func (a *Agent) defaultModel() string {
	if model := a.modelForTier(api.TierStandard); model != "" {
		return model
	}
	switch a.runner.Kind() {
	case api.AgentKindCodex:
		return a.config.Codex.Model
	default:
		return a.config.Claude.Model
	}
}

func (a *Agent) defaultTimeout() time.Duration {
	switch a.runner.Kind() {
	case api.AgentKindCodex:
		return a.config.Codex.Timeout
	default:
		return a.config.Claude.Timeout
	}
}

func (a *Agent) modelForTier(tier string) string {
	if model := a.config.Tiers.Value(tier); model != "" {
		return model
	}
	switch a.runner.Kind() {
	case api.AgentKindClaude:
		return config.DefaultClaudeTiers().Value(tier)
	case api.AgentKindCodex:
		return config.DefaultCodexTiers().Value(tier)
	}
	return ""
}

func (a *Agent) resolveModel(tier string) (string, error) {
	if tier == "" {
		tier = api.TierStandard
	}
	model := a.modelForTier(tier)
	if model == "" {
		model = a.defaultModel()
	}
	if model == "" {
		if a.runner.Kind() == api.AgentKindCodex {
			return "", fmt.Errorf("no model configured for tier %q (set codex.model or tiers.%s)", tier, tier)
		}
		return "", fmt.Errorf("no model configured for tier %q", tier)
	}
	return model, nil
}

// loadAgencyPrompt loads the agency prompt file for this agent.
// It looks for the prompt file in this order:
// 1. Explicit AgencyPromptFile from config
// 2. <AgencyPromptsDir>/<agent_kind>-<mode>.md (e.g., claude-prod.md)
// 3. <AgencyPromptsDir>/<agent_kind>-prod.md (fallback if dev variant missing)
// Returns error if no prompt file is found (forces proper installation).
func (a *Agent) loadAgencyPrompt() (string, error) {
	// 1. Try explicit file path from config
	if a.config.AgencyPromptFile != "" {
		data, err := os.ReadFile(a.config.AgencyPromptFile)
		if err != nil {
			return "", fmt.Errorf("reading agency prompt file %s: %w", a.config.AgencyPromptFile, err)
		}
		return string(data), nil
	}

	// 2. Determine prompts directory
	promptsDir := a.config.AgencyPromptsDir
	if promptsDir == "" {
		promptsDir = config.DefaultPromptsPath()
	}

	// 3. Try mode-specific file (e.g., claude-dev.md)
	mode := config.AgencyMode()
	promptFile := filepath.Join(promptsDir, fmt.Sprintf("%s-%s.md", a.agentKind, mode))
	if data, err := os.ReadFile(promptFile); err == nil {
		return string(data), nil
	}

	// 4. Fallback to prod variant if dev variant missing
	if mode != "prod" {
		prodFile := filepath.Join(promptsDir, fmt.Sprintf("%s-prod.md", a.agentKind))
		if data, err := os.ReadFile(prodFile); err == nil {
			a.log.Info("using prod agency prompt (dev variant not found)", map[string]any{
				"prod_file": prodFile,
				"dev_file":  promptFile,
			})
			return string(data), nil
		}
	}

	return "", fmt.Errorf("agency prompt file not found: tried %s (install agency prompts to %s)", promptFile, promptsDir)
}

func (a *Agent) buildPrompt(task *Task) (string, error) {
	// Load agency prompt fresh each task (allows hot-reload)
	agencyPrompt, err := a.loadAgencyPrompt()
	if err != nil {
		return "", err
	}
	return agencyPrompt + "\n\n" + task.Prompt, nil
}

func setTaskCompletion(task *Task, completedAt time.Time) {
	task.CompletedAt = &completedAt
	if task.StartedAt != nil {
		task.DurationSeconds = completedAt.Sub(*task.StartedAt).Seconds()
	}
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

	if req.Tier != "" && !api.IsValidTier(req.Tier) {
		api.WriteError(w, http.StatusBadRequest, "validation_error", "tier must be fast, standard, or heavy")
		return
	}

	if req.SessionID != "" && !isSafeSessionID(req.SessionID) {
		api.WriteError(w, http.StatusBadRequest, "validation_error", "session_id contains invalid characters")
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

	model, err := a.resolveModel(req.Tier)
	if err != nil {
		a.mu.Unlock()
		api.WriteError(w, http.StatusInternalServerError, "configuration_error", err.Error())
		return
	}

	task := &Task{
		ID:            "task-" + uuid.New().String()[:8],
		State:         TaskStateQueued,
		Prompt:        req.Prompt,
		Model:         model,
		SessionID:     sessionID,
		ResumeSession: resumeSession,
		WorkDir:       sessionID,
	}

	if req.TimeoutSeconds > 0 {
		task.Timeout = time.Duration(req.TimeoutSeconds) * time.Second
	} else {
		task.Timeout = a.defaultTimeout()
	}

	a.tasks[task.ID] = task
	a.currentTask = task
	a.state = StateWorking

	// Log task creation with task-scoped logger
	a.log.WithTask(task.ID).Info("task created", map[string]any{
		"session_id": task.SessionID,
		"model":      task.Model,
		"resume":     task.ResumeSession,
	})

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
	var resp map[string]interface{}
	if ok {
		var exitCode *int
		if task.ExitCode != nil {
			code := *task.ExitCode
			exitCode = &code
		}
		var tokenUsage *TokenUsage
		if task.TokenUsage != nil {
			usage := *task.TokenUsage
			tokenUsage = &usage
		}
		var taskError *TaskError
		if task.Error != nil {
			errCopy := *task.Error
			taskError = &errCopy
		}

		resp = map[string]interface{}{
			"task_id":          task.ID,
			"state":            task.State,
			"exit_code":        exitCode,
			"output":           task.Output,
			"session_id":       task.SessionID,
			"token_usage":      tokenUsage,
			"duration_seconds": task.DurationSeconds,
		}

		if task.StartedAt != nil {
			resp["started_at"] = task.StartedAt.Format(time.RFC3339)
		}
		if task.CompletedAt != nil {
			resp["completed_at"] = task.CompletedAt.Format(time.RFC3339)
		}
		if taskError != nil {
			resp["error"] = taskError
		}
	}
	a.mu.RUnlock()

	if ok {
		api.WriteJSON(w, http.StatusOK, resp)
		return
	}

	if a.history != nil {
		if entry, err := a.history.Get(taskID); err == nil {
			api.WriteJSON(w, http.StatusOK, entry)
			return
		}
	}

	api.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Task %s not found", taskID))
}

// handleCancelTask cancels a running task by ID.
// Triggers context cancellation which sends SIGTERM to the CLI process.
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
	// Kill the process group to ensure clean shutdown of CLI subprocess
	if task.cmd != nil {
		killProcessGroup(task.cmd)
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

// executeTask runs the CLI runner with the given task configuration.
// It handles the full lifecycle: setup, execution, timeout/cancellation, and result parsing.
//
// The function:
//  1. Creates a timeout context based on task.Timeout
//  2. Creates/reuses session directory and executes the CLI runner
//  3. Handles three termination cases: success, timeout, or cancellation
//  4. Parses JSON output from the runner or falls back to raw stdout
//  5. Updates task state and clears agent's current task when done
//
// The env parameter allows passing additional environment variables to the runner.
// Auto-resumes up to 2 times if the runner reports a max_turns limit.
func (a *Agent) executeTask(task *Task, env map[string]string) {
	taskLog := a.log.WithTask(task.ID)
	taskLog.Info("task started", map[string]any{
		"timeout_seconds": task.Timeout.Seconds(),
	})

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
	if err := os.MkdirAll(workDir, 0700); err != nil {
		completedAt := time.Now()
		a.mu.Lock()
		setTaskCompletion(task, completedAt)
		task.State = TaskStateFailed
		exitCode := 1
		task.ExitCode = &exitCode
		task.Error = &TaskError{
			Type:    "session_error",
			Message: fmt.Sprintf("Failed to create session directory: %v", err),
		}
		a.mu.Unlock()
		a.saveTaskHistory(task, nil)
		a.cleanupTask(task)
		return
	}

	runnerBin := a.runner.ResolveBin()

	const maxAutoResumes = 2
	var lastOutput []byte

	// Execution loop: runs once normally, up to 2 more times for max_turns auto-resume
	for {
		prompt, promptErr := a.buildPrompt(task)
		if promptErr != nil {
			completedAt := time.Now()
			a.mu.Lock()
			setTaskCompletion(task, completedAt)
			task.State = TaskStateFailed
			exitCode := 1
			task.ExitCode = &exitCode
			task.Error = &TaskError{
				Type:    "prompt_error",
				Message: promptErr.Error(),
			}
			a.mu.Unlock()
			a.saveTaskHistory(task, nil)
			a.cleanupTask(task)
			return
		}
		cmdSpec := a.runner.BuildCommand(task, prompt, a.config)

		cmd := exec.CommandContext(ctx, runnerBin, cmdSpec.Args...)
		cmd.Dir = workDir
		if cmdSpec.PromptInStdin {
			cmd.Stdin = strings.NewReader(prompt)
		}

		// Inherit current environment and add task-specific vars
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}

		// Set up process group for proper signal propagation
		setupProcessGroup(cmd)

		// Set up streaming output capture
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			completedAt := time.Now()
			a.mu.Lock()
			setTaskCompletion(task, completedAt)
			task.State = TaskStateFailed
			exitCode := 1
			task.ExitCode = &exitCode
			task.Error = &TaskError{
				Type:    "pipe_error",
				Message: fmt.Sprintf("Failed to create stdout pipe: %v", err),
			}
			a.mu.Unlock()
			a.saveTaskHistory(task, nil)
			a.cleanupTask(task)
			return
		}

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		// Start the process first, then set task.cmd to avoid race
		// where handleCancelTask reads cmd.Process while it's being initialized
		cmdErr := cmd.Start()
		if cmdErr != nil {
			completedAt := time.Now()
			a.mu.Lock()
			setTaskCompletion(task, completedAt)
			task.State = TaskStateFailed
			exitCode := 1
			task.ExitCode = &exitCode
			task.Error = &TaskError{
				Type:    "start_error",
				Message: fmt.Sprintf("Failed to start CLI: %v", cmdErr),
			}
			a.mu.Unlock()
			a.saveTaskHistory(task, nil)
			a.cleanupTask(task)
			return
		}

		// Process started successfully, now it's safe to set task.cmd
		a.mu.Lock()
		task.cmd = cmd
		a.mu.Unlock()

		// Stream and parse output line by line
		parser := stream.NewClaudeStreamParser()
		eventLogger := stream.NewToolEventLogger(taskLog)

		var outputBuf bytes.Buffer
		var lastResult *stream.ClaudeStreamEvent

		scanner := bufio.NewScanner(stdout)
		// Increase buffer size for potentially large JSON lines
		const maxScannerBuffer = 10 * 1024 * 1024 // 10MB
		scanner.Buffer(make([]byte, 64*1024), maxScannerBuffer)

		for scanner.Scan() {
			line := scanner.Bytes()
			outputBuf.Write(line)
			outputBuf.WriteByte('\n')

			// Parse stream events and log them
			events, parseErr := parser.ParseLine(line)
			if parseErr != nil {
				taskLog.Debug("stream parse error", map[string]any{
					"error": parseErr.Error(),
				})
				continue
			}

			for _, event := range events {
				eventLogger.Log(event)
			}

			// Track the last result event for final metrics
			var rawEvent stream.ClaudeStreamEvent
			if json.Unmarshal(line, &rawEvent) == nil && rawEvent.Type == "result" {
				lastResult = &rawEvent
			}
		}

		// Check for scanner errors (e.g., line too long, I/O error)
		if err := scanner.Err(); err != nil {
			taskLog.Warn("stream scanner error", map[string]any{
				"error": err.Error(),
			})
		}

		lastOutput = outputBuf.Bytes()

		// Wait for command to complete
		cmdErr = cmd.Wait()
		completedAt := time.Now()

		a.mu.Lock()
		setTaskCompletion(task, completedAt)

		// Handle cancellation: context was canceled and task was marked cancelled
		if ctx.Err() == context.Canceled && task.State == TaskStateCancelled {
			if task.Error == nil {
				task.Error = &TaskError{
					Type:    "cancelled",
					Message: "Task cancelled",
				}
			}
			a.mu.Unlock()
			a.saveTaskHistory(task, lastOutput)
			a.cleanupTask(task)
			return
		}

		// Handle timeout: context deadline exceeded
		if ctx.Err() == context.DeadlineExceeded {
			task.State = TaskStateFailed
			exitCode := 1
			task.ExitCode = &exitCode
			task.Error = &TaskError{
				Type:    "timeout",
				Message: fmt.Sprintf("Task exceeded timeout of %v", task.Timeout),
			}
			a.mu.Unlock()
			a.saveTaskHistory(task, lastOutput)
			a.cleanupTask(task)
			return
		}

		// Process the final result from stream
		if lastResult != nil {
			// Extract session_id from the result
			if lastResult.SessionID != "" {
				if isSafeSessionID(lastResult.SessionID) {
					task.SessionID = lastResult.SessionID
				} else {
					taskLog.Warn("ignoring unsafe session_id from CLI", map[string]any{
						"session_id": lastResult.SessionID,
					})
				}
			}

			// Extract result text - look for it in the stream output
			task.Output = extractResultFromStream(lastOutput)

			// Check for max_turns limit and auto-resume if possible
			if lastResult.Subtype == "error_max_turns" && task.maxTurnsResumes < maxAutoResumes {
				task.maxTurnsResumes++
				task.ResumeSession = true
				taskLog.Info("hit max_turns limit, auto-resuming", map[string]any{
					"attempt":     task.maxTurnsResumes + 1,
					"max_retries": maxAutoResumes + 1,
				})
				a.mu.Unlock()
				continue // Retry with resume
			}

			// If max_turns exhausted after all retries, fail with clear error
			if lastResult.Subtype == "error_max_turns" {
				task.State = TaskStateFailed
				task.Error = &TaskError{
					Type: "max_turns",
					Message: fmt.Sprintf("Task exceeded maximum turns limit (%d turns x %d attempts). Consider breaking the task into smaller steps.",
						a.runner.MaxTurnsLimit(a.config), maxAutoResumes+1),
				}
				a.mu.Unlock()
				a.saveTaskHistory(task, lastOutput)
				a.cleanupTask(task)
				return
			}
		}

		// Try runner-specific parsing for metadata (session_id, tokens, etc)
		parsedOutput, parsed := a.runner.ParseOutput(lastOutput)
		if parsed {
			// Only update session_id if runner returns a safe, non-empty value and we didn't already get one
			if parsedOutput.SessionID != "" && task.SessionID == "" {
				if isSafeSessionID(parsedOutput.SessionID) {
					task.SessionID = parsedOutput.SessionID
				} else {
					taskLog.Warn("ignoring unsafe session_id from runner", map[string]any{
						"session_id": parsedOutput.SessionID,
					})
				}
			}
			// Extract token usage if not already set
			if task.TokenUsage == nil && parsedOutput.TokenUsage != nil {
				usage := *parsedOutput.TokenUsage
				task.TokenUsage = &usage
			}
			// Extract output from runner if available (overrides extractResultFromStream for non-Claude runners)
			if parsedOutput.HasOutput {
				task.Output = parsedOutput.Output
			}
			// For Codex, handle session directory renaming
			if a.runner.Kind() == api.AgentKindCodex && !task.ResumeSession && task.SessionID != "" {
				oldPath := workDir
				newPath := filepath.Join(a.config.SessionDir, task.SessionID)
				task.WorkDir = task.SessionID
				if oldPath != newPath {
					if err := os.Rename(oldPath, newPath); err != nil {
						taskLog.Warn("failed to rename session directory", map[string]any{
							"error": err.Error(),
						})
					}
				}
			}
		}

		// Determine final state based on command execution result
		if cmdErr != nil {
			task.State = TaskStateFailed
			exitCode := 1
			if exitErr, ok := cmdErr.(*exec.ExitError); ok {
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					exitCode = status.ExitStatus()
				}
			}
			task.ExitCode = &exitCode
			task.Error = &TaskError{
				Type:    a.runner.ErrorType(),
				Message: stderr.String(),
			}
			taskLog.Error("task failed", map[string]any{
				"error_type":       a.runner.ErrorType(),
				"exit_code":        exitCode,
				"duration_seconds": task.DurationSeconds,
			})
		} else {
			task.State = TaskStateCompleted
			exitCode := 0
			task.ExitCode = &exitCode
			logFields := map[string]any{
				"duration_seconds": task.DurationSeconds,
			}
			if task.TokenUsage != nil {
				logFields["input_tokens"] = task.TokenUsage.Input
				logFields["output_tokens"] = task.TokenUsage.Output
			}
			taskLog.Info("task completed", logFields)
		}

		// Save to history and complete
		a.mu.Unlock()
		a.saveTaskHistory(task, lastOutput)
		a.cleanupTask(task)
		return
	}
}

// extractResultFromStream extracts the result text from Claude stream-json output.
// It looks for the last assistant message with text content.
func extractResultFromStream(output []byte) string {
	var lastText string

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var event struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
			Result string `json:"result"` // For backwards compatibility with non-stream format
		}
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			// Check for result field (backwards compatibility)
			if event.Result != "" {
				return event.Result
			}
			// Extract text from assistant messages
			if event.Type == "assistant" {
				for _, block := range event.Message.Content {
					if block.Type == "text" && block.Text != "" {
						lastText = block.Text
					}
				}
			}
		}
	}
	// Ignore scanner.Err() - we've extracted what we could from the available output

	return lastText
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
		a.log.WithTask(task.ID).Warn("failed to save task history", map[string]any{
			"error": err.Error(),
		})
	}

	// Save debug log (raw CLI output)
	if len(rawOutput) > 0 {
		if err := a.history.SaveDebugLog(task.ID, rawOutput); err != nil {
			a.log.WithTask(task.ID).Warn("failed to save debug log", map[string]any{
				"error": err.Error(),
			})
		}
	}
}

func (a *Agent) cleanupTask(task *Task) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Keep completed tasks only when history storage is disabled.
	if a.currentTask != nil && a.currentTask.ID == task.ID {
		a.currentTask = nil
	}
	a.state = StateIdle
	if a.history != nil {
		delete(a.tasks, task.ID)
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

// handleLogs returns log entries with optional filtering.
// Query params:
//   - level: minimum log level (debug, info, warn, error)
//   - task_id: filter by task ID
//   - since: RFC3339 timestamp to filter entries after
//   - until: RFC3339 timestamp to filter entries before
//   - limit: max entries to return (default 100)
func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := logging.Query{
		Limit: 100, // Default limit
	}

	if level := r.URL.Query().Get("level"); level != "" {
		q.Level = logging.Level(level)
	}
	if taskID := r.URL.Query().Get("task_id"); taskID != "" {
		q.TaskID = taskID
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.Since = t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			q.Until = t
		}
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if v, err := strconv.Atoi(limit); err == nil && v > 0 {
			q.Limit = v
		}
	}

	result := a.log.Query(q)
	api.WriteJSON(w, http.StatusOK, result)
}

// handleLogStats returns log statistics without entries.
func (a *Agent) handleLogStats(w http.ResponseWriter, r *http.Request) {
	stats := a.log.Stats()
	api.WriteJSON(w, http.StatusOK, stats)
}
