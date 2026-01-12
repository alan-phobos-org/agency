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
	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
	a := New(cfg, "test")

	body := `{"prompt": "test prompt"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), "task_id")
	require.Contains(t, w.Body.String(), "session_id")
	require.Contains(t, w.Body.String(), "working")

	// Wait for background task to complete to avoid TempDir cleanup race
	time.Sleep(100 * time.Millisecond)
}

func TestCreateTaskCreatesSessionDir(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "echo")

	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
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

	cfg := config.Default()
	cfg.SessionDir = t.TempDir()
	a := New(cfg, "test")
	defer a.Shutdown(context.Background())

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
			a := New(cfg, "test")
			args := a.buildClaudeArgs(tt.task)
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

func TestPrepromptFileLoading(t *testing.T) {
	t.Parallel()

	// Create a custom preprompt file
	tmpDir := t.TempDir()
	prepromptPath := filepath.Join(tmpDir, "custom-preprompt.md")
	customContent := "# Custom Instructions\n\nDo custom things."
	err := os.WriteFile(prepromptPath, []byte(customContent), 0644)
	require.NoError(t, err)

	cfg := config.Default()
	cfg.PrepromptFile = prepromptPath

	a := New(cfg, "test")

	// Verify custom preprompt is used
	require.Equal(t, customContent, a.preprompt)

	// Verify it appears in built args
	task := &Task{Model: "sonnet", Prompt: "test prompt"}
	args := a.buildClaudeArgs(task)
	prompt := args[len(args)-1] // Last arg is the prompt
	require.Contains(t, prompt, "# Custom Instructions")
	require.Contains(t, prompt, "test prompt")
}

func TestPrepromptFileFallbackToDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.PrepromptFile = "/nonexistent/path/preprompt.md"

	a := New(cfg, "test")

	// Should fall back to embedded default
	require.Contains(t, a.preprompt, "# Agent Instructions")
}

func TestPrepromptDefaultEmbedded(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	// No PrepromptFile set

	a := New(cfg, "test")

	// Should use embedded default
	require.Contains(t, a.preprompt, "# Agent Instructions")
	require.Contains(t, a.preprompt, "Git Commits")
}

func TestCreateTaskThinkingDefault(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "echo")

	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
	a := New(cfg, "test")

	// Submit task without thinking field - should default to true
	body := `{"prompt": "test prompt"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	// Parse response to get task ID
	var resp struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Wait for task to complete
	time.Sleep(100 * time.Millisecond)

	// Look up task from tasks map (persists after completion)
	a.mu.RLock()
	task, ok := a.tasks[resp.TaskID]
	a.mu.RUnlock()
	require.True(t, ok, "task should exist in tasks map")
	require.True(t, task.Thinking, "thinking should default to true")
}

func TestCreateTaskThinkingExplicit(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	t.Setenv("CLAUDE_BIN", "echo")

	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "thinking explicitly true",
			body:     `{"prompt": "test", "thinking": true}`,
			expected: true,
		},
		{
			name:     "thinking explicitly false",
			body:     `{"prompt": "test", "thinking": false}`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := config.Default()
			cfg.SessionDir = filepath.Join(tmpDir, "sessions")
			cfg.HistoryDir = filepath.Join(tmpDir, "history")
			a := New(cfg, "test")

			req := httptest.NewRequest("POST", "/task", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			a.Router().ServeHTTP(w, req)

			require.Equal(t, http.StatusCreated, w.Code)

			// Parse response to get task ID
			var resp struct {
				TaskID string `json:"task_id"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

			// Wait for task to complete
			time.Sleep(100 * time.Millisecond)

			// Look up task from tasks map (persists after completion)
			a.mu.RLock()
			task, ok := a.tasks[resp.TaskID]
			a.mu.RUnlock()
			require.True(t, ok, "task should exist in tasks map")
			require.Equal(t, tt.expected, task.Thinking)
		})
	}
}

func TestBuildClaudeArgsCustomMaxTurns(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Claude.MaxTurns = 100 // Custom value
	a := New(cfg, "test")

	task := &Task{
		Model:  "sonnet",
		Prompt: "test prompt",
	}

	args := a.buildClaudeArgs(task)
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
	counterFile := filepath.Join(t.TempDir(), "counter")
	t.Setenv("MOCK_MAX_TURNS_COUNTER", counterFile)
	// Fail twice, succeed on 3rd attempt
	t.Setenv("MOCK_MAX_TURNS_FAIL_COUNT", "2")

	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
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
	a.mu.RUnlock()
	require.True(t, ok, "task should exist")
	require.Equal(t, TaskStateCompleted, task.State, "task should complete after auto-resume")
	require.Contains(t, task.Output, "completed after 3 attempts")
}

func TestMaxTurnsExhausted(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv()
	mockPath, err := filepath.Abs("../../testdata/mock-claude-max-turns")
	require.NoError(t, err)
	t.Setenv("CLAUDE_BIN", mockPath)

	// Use temp file for counter
	counterFile := filepath.Join(t.TempDir(), "counter")
	t.Setenv("MOCK_MAX_TURNS_COUNTER", counterFile)
	// Fail 5 times - more than the 2 auto-resumes allowed
	t.Setenv("MOCK_MAX_TURNS_FAIL_COUNT", "5")

	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.SessionDir = filepath.Join(tmpDir, "sessions")
	cfg.HistoryDir = filepath.Join(tmpDir, "history")
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
	a.mu.RUnlock()
	require.True(t, ok, "task should exist")
	require.Equal(t, TaskStateFailed, task.State, "task should fail after exhausting retries")
	require.NotNil(t, task.Error)
	require.Equal(t, "max_turns", task.Error.Type)
	require.Contains(t, task.Error.Message, "maximum turns limit")
}
