package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"phobos.org.uk/agency/internal/config"
)

func TestStatusEndpoint(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test-version")

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"state":"idle"`)
	require.Contains(t, w.Body.String(), `"version":"test-version"`)
	require.Contains(t, w.Body.String(), `"type":"agent"`)
	require.Contains(t, w.Body.String(), `"interfaces":["statusable","taskable"]`)
}

func TestCreateTaskValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing prompt",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "prompt is required",
		},
		{
			name:       "invalid json",
			body:       `{invalid`,
			wantStatus: http.StatusBadRequest,
			wantError:  "Invalid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Default()
			a := New(cfg, "test")

			req := httptest.NewRequest("POST", "/task", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.Router().ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code)
			require.Contains(t, w.Body.String(), tt.wantError)
		})
	}
}

func TestCreateTaskSuccess(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "echo")

	tmpDir := t.TempDir()
	// Create agency prompt file
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = "" // Disable history so tasks remain in memory for testing
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")

	body := `{"prompt": "test prompt"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var response struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotEmpty(t, response.TaskID)
	require.NotEmpty(t, response.SessionID)
	require.Equal(t, "working", response.Status)

	// Wait for background task to reach terminal state with polling
	taskID := response.TaskID
	require.Eventually(t, func() bool {
		a.mu.RLock()
		defer a.mu.RUnlock()

		task, exists := a.tasks[taskID]
		if !exists {
			return false
		}

		// Task should reach completed or failed state
		return task.State == TaskStateCompleted || task.State == TaskStateFailed
	}, 2*time.Second, 50*time.Millisecond, "task should complete within 2 seconds")
}

func TestCreateTaskCreatesSessionDir(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "echo")

	tmpDir := t.TempDir()
	// Create agency prompt file
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")

	body := `{"prompt": "test prompt"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	// Wait for task to start executing (creates session directory)
	time.Sleep(100 * time.Millisecond)

	// The session directory should exist under SessionDir
	require.DirExists(t, cfg.SessionDir)
}

func TestGetTaskNotFound(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test")

	req := httptest.NewRequest("GET", "/task/nonexistent", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "not_found")
}

func TestAgentBusy(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "sleep")

	tmpDir := t.TempDir()
	// Create agency prompt file
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")
	defer func() {
		a.Shutdown(context.Background())
		// Allow time for cleanup goroutines to finish
		time.Sleep(100 * time.Millisecond)
	}()

	// Submit first task
	body := `{"prompt": "test"}`
	req1 := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	a.Router().ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	// Try to submit second task
	req2 := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	a.Router().ServeHTTP(w2, req2)

	require.Equal(t, http.StatusConflict, w2.Code)
	require.Contains(t, w2.Body.String(), "agent_busy")
}

func TestShutdownWithoutTask(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test")

	req := httptest.NewRequest("POST", "/shutdown", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Contains(t, w.Body.String(), "Shutdown initiated")
}

func TestBuildClaudeArgs(t *testing.T) {
	t.Parallel()

	// Create a shared temp dir with agency prompt for all subtests
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Agent Instructions\n\nTest instructions here."), 0644))

	tests := []struct {
		name   string
		task   *Task
		verify func(t *testing.T, args []string)
	}{
		{
			name: "normal prompt",
			task: &Task{
				Model:  "sonnet",
				Prompt: "Hello world",
			},
			verify: func(t *testing.T, args []string) {
				require.Contains(t, args, "--")
				dashIdx := indexOf(args, "--")
				require.Greater(t, dashIdx, 0, "-- should be present")
				prompt := args[dashIdx+1]
				// Prompt should contain agent instructions and original prompt
				require.Contains(t, prompt, "# Agent Instructions")
				require.Contains(t, prompt, "Hello world")
			},
		},
		{
			name: "prompt with leading dash",
			task: &Task{
				Model:  "sonnet",
				Prompt: "- clone https://github.com/example/repo",
			},
			verify: func(t *testing.T, args []string) {
				dashIdx := indexOf(args, "--")
				require.Greater(t, dashIdx, 0, "-- should be present")
				prompt := args[dashIdx+1]
				require.Contains(t, prompt, "# Agent Instructions")
				require.Contains(t, prompt, "- clone https://github.com/example/repo")
			},
		},
		{
			name: "prompt with multiple dashes",
			task: &Task{
				Model:  "sonnet",
				Prompt: "- clone repo\n- remove file\n- commit and push",
			},
			verify: func(t *testing.T, args []string) {
				dashIdx := indexOf(args, "--")
				require.Greater(t, dashIdx, 0, "-- should be present")
				prompt := args[dashIdx+1]
				require.Contains(t, prompt, "# Agent Instructions")
				require.Contains(t, prompt, "- clone repo\n- remove file\n- commit and push")
			},
		},
		{
			name: "prompt starting with double dash",
			task: &Task{
				Model:  "sonnet",
				Prompt: "--help me with this",
			},
			verify: func(t *testing.T, args []string) {
				dashIdx := indexOf(args, "--")
				require.Greater(t, dashIdx, 0, "-- should be present")
				prompt := args[dashIdx+1]
				require.Contains(t, prompt, "# Agent Instructions")
				require.Contains(t, prompt, "--help me with this")
			},
		},
		{
			name: "new session with session ID",
			task: &Task{
				Model:         "sonnet",
				Prompt:        "test prompt",
				SessionID:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				ResumeSession: false,
			},
			verify: func(t *testing.T, args []string) {
				// Should use --session-id for new sessions
				require.Contains(t, args, "--session-id")
				idx := indexOf(args, "--session-id")
				require.Equal(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", args[idx+1])
				// Should NOT have --resume
				require.NotContains(t, args, "--resume")
			},
		},
		{
			name: "resumed session with session ID",
			task: &Task{
				Model:         "sonnet",
				Prompt:        "test prompt",
				SessionID:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				ResumeSession: true,
			},
			verify: func(t *testing.T, args []string) {
				// Should use --resume for continued sessions
				require.Contains(t, args, "--resume")
				idx := indexOf(args, "--resume")
				require.Equal(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", args[idx+1])
				// Should NOT have --session-id
				require.NotContains(t, args, "--session-id")
			},
		},
		{
			name: "max-turns from config",
			task: &Task{
				Model:  "sonnet",
				Prompt: "test prompt",
			},
			verify: func(t *testing.T, args []string) {
				require.Contains(t, args, "--max-turns")
				idx := indexOf(args, "--max-turns")
				require.Equal(t, "50", args[idx+1]) // Default value
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.AgencyPromptsDir = promptsDir
			a := New(cfg, "test")
			prompt, err := a.buildPrompt(tt.task)
			require.NoError(t, err)
			cmdSpec := claudeRunner{}.BuildCommand(tt.task, prompt, cfg)
			args := cmdSpec.Args
			tt.verify(t, args)
		})
	}
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func TestAgencyPromptFileLoading(t *testing.T) {
	t.Parallel()

	// Create a custom agency prompt file
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	customContent := "# Custom Instructions\n\nDo custom things."
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte(customContent), 0644))

	cfg := config.Default()
	cfg.AgencyPromptsDir = promptsDir

	a := New(cfg, "test")

	// Verify it appears in built args
	task := &Task{Model: "sonnet", Prompt: "test prompt"}
	prompt, err := a.buildPrompt(task)
	require.NoError(t, err)
	cmdSpec := claudeRunner{}.BuildCommand(task, prompt, cfg)
	args := cmdSpec.Args
	promptArg := args[len(args)-1] // Last arg is the prompt
	require.Contains(t, promptArg, "# Custom Instructions")
	require.Contains(t, promptArg, "test prompt")
}

func TestAgencyPromptExplicitFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "custom-prompt.md")
	customContent := "# Explicit Instructions\n\nDo specific things."
	require.NoError(t, os.WriteFile(promptFile, []byte(customContent), 0644))

	cfg := config.Default()
	cfg.AgencyPromptFile = promptFile

	a := New(cfg, "test")

	// Verify explicit file is used
	task := &Task{Model: "sonnet", Prompt: "test prompt"}
	prompt, err := a.buildPrompt(task)
	require.NoError(t, err)
	require.Contains(t, prompt, "# Explicit Instructions")
	require.Contains(t, prompt, "test prompt")
}

func TestAgencyPromptFileMissing(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AgencyPromptsDir = "/nonexistent/path"

	a := New(cfg, "test")

	// Should return error when prompt file is missing
	task := &Task{Model: "sonnet", Prompt: "test prompt"}
	_, err := a.buildPrompt(task)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agency prompt file not found")
}

func TestBuildClaudeArgsCustomMaxTurns(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.Claude.MaxTurns = 100 // Custom value
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")

	task := &Task{
		Model:  "sonnet",
		Prompt: "test prompt",
	}

	prompt, err := a.buildPrompt(task)
	require.NoError(t, err)
	cmdSpec := claudeRunner{}.BuildCommand(task, prompt, cfg)
	args := cmdSpec.Args
	require.Contains(t, args, "--max-turns")
	idx := indexOf(args, "--max-turns")
	require.Equal(t, "100", args[idx+1])
}

func TestMaxTurnsAutoResume(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	mockPath, err := filepath.Abs("../../testdata/mock-claude-max-turns")
	require.NoError(t, err)
	t.Setenv("CLAUDE_BIN", mockPath)

	// Use temp file for counter to avoid interference between tests
	tmpDir := t.TempDir()
	counterFile := filepath.Join(tmpDir, "counter")
	t.Setenv("MOCK_MAX_TURNS_COUNTER", counterFile)
	// Fail twice, succeed on 3rd attempt
	t.Setenv("MOCK_MAX_TURNS_FAIL_COUNT", "2")

	// Create agency prompt file
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = "" // Disable history so tasks remain in memory for verification
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")

	// Submit task
	body := `{"prompt": "test max turns"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Wait for task to complete (with retries, needs more time)
	time.Sleep(500 * time.Millisecond)

	// Verify task completed successfully after auto-resume
	a.mu.RLock()
	task, ok := a.tasks[resp.TaskID]
	require.True(t, ok, "task should exist")
	taskState := task.State
	taskOutput := task.Output
	a.mu.RUnlock()
	require.Equal(t, TaskStateCompleted, taskState, "task should complete after auto-resume")
	require.Contains(t, taskOutput, "completed after 3 attempts")
}

func TestMaxTurnsExhausted(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	mockPath, err := filepath.Abs("../../testdata/mock-claude-max-turns")
	require.NoError(t, err)
	t.Setenv("CLAUDE_BIN", mockPath)

	// Use temp file for counter
	tmpDir := t.TempDir()
	counterFile := filepath.Join(tmpDir, "counter")
	t.Setenv("MOCK_MAX_TURNS_COUNTER", counterFile)
	// Fail 5 times - more than the 2 auto-resumes allowed
	t.Setenv("MOCK_MAX_TURNS_FAIL_COUNT", "5")

	// Create agency prompt file
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "claude-prod.md"), []byte("# Test Instructions"), 0644))

	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = "" // Disable history so tasks remain in memory for verification
	cfg.AgencyPromptsDir = promptsDir
	a := New(cfg, "test")

	// Submit task
	body := `{"prompt": "test max turns exhausted"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Wait for task to complete
	time.Sleep(500 * time.Millisecond)

	// Verify task failed with max_turns error
	a.mu.RLock()
	task, ok := a.tasks[resp.TaskID]
	require.True(t, ok, "task should exist")
	taskState := task.State
	taskError := task.Error
	a.mu.RUnlock()
	require.Equal(t, TaskStateFailed, taskState, "task should fail after exhausting retries")
	require.NotNil(t, taskError)
	require.Equal(t, "max_turns", taskError.Type)
	require.Contains(t, taskError.Message, "maximum turns limit")
}

func TestLogsStatsEndpoint(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test-version")

	// The logger is initialized on agent creation, so there should be at least the startup log
	req := httptest.NewRequest("GET", "/logs/stats", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var stats struct {
		Debug int64 `json:"debug"`
		Info  int64 `json:"info"`
		Warn  int64 `json:"warn"`
		Error int64 `json:"error"`
		Total int64 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
	require.GreaterOrEqual(t, stats.Total, int64(0))
}

func TestLogsEndpoint(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test-version")

	// Query all logs
	req := httptest.NewRequest("GET", "/logs", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var result struct {
		Entries []struct {
			Timestamp string `json:"timestamp"`
			Level     string `json:"level"`
			Message   string `json:"message"`
			Component string `json:"component"`
		} `json:"entries"`
		Total  int `json:"total"`
		Counts struct {
			Debug int64 `json:"debug"`
			Info  int64 `json:"info"`
			Warn  int64 `json:"warn"`
			Error int64 `json:"error"`
			Total int64 `json:"total"`
		} `json:"counts"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))

	// All entries should have component "agent"
	for _, entry := range result.Entries {
		require.Equal(t, "agent", entry.Component)
	}
}

func TestLogsEndpointWithFilters(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	a := New(cfg, "test-version")

	// Query with level filter
	req := httptest.NewRequest("GET", "/logs?level=error&limit=10", nil)
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var result struct {
		Entries []struct {
			Level string `json:"level"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))

	// All returned entries should be error level
	for _, entry := range result.Entries {
		require.Equal(t, "error", entry.Level)
	}
}
