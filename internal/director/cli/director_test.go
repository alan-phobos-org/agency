package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDirector_Run_Success(t *testing.T) {
	t.Parallel()

	pollCount := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			var req TaskRequest
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, "test prompt", req.Prompt)
			require.Equal(t, "/tmp", req.Workdir)

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(TaskResponse{
				TaskID: "task-123",
				Status: "queued",
			})

		case r.Method == "GET" && r.URL.Path == "/task/task-123":
			count := pollCount.Add(1)
			state := "working"
			if count >= 2 {
				state = "completed"
			}
			json.NewEncoder(w).Encode(TaskStatus{
				TaskID:   "task-123",
				State:    state,
				Output:   "done",
				ExitCode: intPtr(0),
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	d := New(server.URL)
	status, err := d.Run("test prompt", "/tmp", 30*time.Second)

	require.NoError(t, err)
	require.Equal(t, "completed", status.State)
	require.Equal(t, "done", status.Output)
	require.Equal(t, 0, *status.ExitCode)
	require.GreaterOrEqual(t, pollCount.Load(), int32(2))
}

func TestDirector_Run_TaskFailed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(TaskResponse{TaskID: "task-456", Status: "queued"})

		case r.Method == "GET" && r.URL.Path == "/task/task-456":
			json.NewEncoder(w).Encode(TaskStatus{
				TaskID:   "task-456",
				State:    "failed",
				ExitCode: intPtr(1),
				Error:    &Error{Type: "claude_error", Message: "something went wrong"},
			})
		}
	}))
	defer server.Close()

	d := New(server.URL)
	status, err := d.Run("fail prompt", "/tmp", 30*time.Second)

	require.NoError(t, err)
	require.Equal(t, "failed", status.State)
	require.Equal(t, 1, *status.ExitCode)
	require.NotNil(t, status.Error)
	require.Equal(t, "claude_error", status.Error.Type)
}

func TestDirector_Run_AgentBusy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/task" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "agent_busy",
				"message": "Agent is currently processing task-999",
			})
		}
	}))
	defer server.Close()

	d := New(server.URL)
	_, err := d.Run("prompt", "/tmp", 30*time.Second)

	require.Error(t, err)
	require.Contains(t, err.Error(), "409")
}

func TestDirector_Run_PollingTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(TaskResponse{TaskID: "task-slow", Status: "queued"})

		case r.Method == "GET" && r.URL.Path == "/task/task-slow":
			// Always return working - never completes
			json.NewEncoder(w).Encode(TaskStatus{
				TaskID: "task-slow",
				State:  "working",
			})
		}
	}))
	defer server.Close()

	d := New(server.URL)
	// Use a short polling timeout to test timeout behavior
	_, err := d.RunWithTimeout("prompt", "/tmp", 30*time.Second, 500*time.Millisecond)

	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}

func TestDirector_Status(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"roles":   []string{"agent"},
				"version": "v1.0.0",
				"state":   "idle",
			})
		}
	}))
	defer server.Close()

	d := New(server.URL)
	status, err := d.Status()

	require.NoError(t, err)
	require.Equal(t, "idle", status["state"])
	require.Equal(t, "v1.0.0", status["version"])
}

func TestDirector_SubmitTask_ValidationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "validation_error",
			"message": "workdir does not exist",
		})
	}))
	defer server.Close()

	d := New(server.URL)
	_, err := d.Run("prompt", "/nonexistent", 30*time.Second)

	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
}

func TestDirector_ConnectionError(t *testing.T) {
	t.Parallel()

	d := New("http://localhost:1") // Port 1 should be unreachable
	_, err := d.Run("prompt", "/tmp", 30*time.Second)

	require.Error(t, err)
	require.Contains(t, err.Error(), "submitting task")
}

func TestDirector_UnknownState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(TaskResponse{TaskID: "task-weird", Status: "queued"})

		case r.Method == "GET" && r.URL.Path == "/task/task-weird":
			json.NewEncoder(w).Encode(TaskStatus{
				TaskID: "task-weird",
				State:  "unknown_state",
			})
		}
	}))
	defer server.Close()

	d := New(server.URL)
	_, err := d.RunWithTimeout("prompt", "/tmp", 30*time.Second, 2*time.Second)

	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown state")
}

func intPtr(i int) *int {
	return &i
}
