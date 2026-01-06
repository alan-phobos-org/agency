package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/agency/internal/config"
	"github.com/stretchr/testify/require"
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
	require.Contains(t, w.Body.String(), `"roles":["agent"]`)
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
			body:       `{"workdir": "/tmp"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "prompt is required",
		},
		{
			name:       "missing workdir",
			body:       `{"prompt": "test"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "workdir is required",
		},
		{
			name:       "invalid json",
			body:       `{invalid`,
			wantStatus: http.StatusBadRequest,
			wantError:  "Invalid JSON",
		},
		{
			name:       "nonexistent workdir",
			body:       `{"prompt": "test", "workdir": "/nonexistent/path/12345"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "workdir does not exist",
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

	cfg := config.Default()
	a := New(cfg, "test")

	// Use temp directory
	workdir := t.TempDir()

	body := `{"prompt": "test prompt", "workdir": "` + workdir + `"}`
	req := httptest.NewRequest("POST", "/task", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	a.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.Contains(t, w.Body.String(), "task_id")
	require.Contains(t, w.Body.String(), "queued")
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
	a := New(cfg, "test")

	workdir := t.TempDir()

	// Submit first task
	body := `{"prompt": "test", "workdir": "` + workdir + `"}`
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
