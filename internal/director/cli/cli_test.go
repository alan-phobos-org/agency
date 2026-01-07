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

func TestNew(t *testing.T) {
	t.Parallel()

	d := New("http://localhost:9000")
	require.NotNil(t, d)
	require.Equal(t, "http://localhost:9000", d.agentURL)
	require.NotNil(t, d.client)
}

func TestRunSuccess(t *testing.T) {
	t.Parallel()

	taskID := "task-123"
	sessionID := "session-456"
	exitCode := 0

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			require.Equal(t, "test prompt", req["prompt"])

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": sessionID,
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:    taskID,
				State:     "completed",
				ExitCode:  &exitCode,
				Output:    "task output",
				SessionID: sessionID,
			})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("test prompt", 5*time.Second)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, taskID, result.TaskID)
	require.Equal(t, "completed", result.State)
	require.Equal(t, 0, *result.ExitCode)
	require.Equal(t, "task output", result.Output)
	require.Equal(t, sessionID, result.SessionID)
}

func TestRunPolling(t *testing.T) {
	t.Parallel()

	taskID := "task-poll"
	var pollCount int32
	exitCode := 0

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": "sess",
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			count := atomic.AddInt32(&pollCount, 1)
			if count < 3 {
				// Still working
				json.NewEncoder(w).Encode(TaskResult{
					TaskID: taskID,
					State:  "working",
				})
			} else {
				// Now complete
				json.NewEncoder(w).Encode(TaskResult{
					TaskID:   taskID,
					State:    "completed",
					ExitCode: &exitCode,
					Output:   "done after polling",
				})
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("poll test", 5*time.Second)

	require.NoError(t, err)
	require.Equal(t, "completed", result.State)
	require.GreaterOrEqual(t, atomic.LoadInt32(&pollCount), int32(3))
}

func TestRunTaskFailed(t *testing.T) {
	t.Parallel()

	taskID := "task-fail"
	exitCode := 1

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": "sess",
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:   taskID,
				State:    "failed",
				ExitCode: &exitCode,
				Output:   "error occurred",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("fail test", 5*time.Second)

	require.NoError(t, err) // No error - task completed (with failure)
	require.Equal(t, "failed", result.State)
	require.Equal(t, 1, *result.ExitCode)
}

func TestRunTaskCancelled(t *testing.T) {
	t.Parallel()

	taskID := "task-cancel"

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": "sess",
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			json.NewEncoder(w).Encode(TaskResult{
				TaskID: taskID,
				State:  "cancelled",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("cancel test", 5*time.Second)

	require.NoError(t, err)
	require.Equal(t, "cancelled", result.State)
}

func TestRunSubmitError(t *testing.T) {
	t.Parallel()

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict) // Agent busy
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "agent_busy",
			"message": "Agent is busy",
		})
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("busy test", 5*time.Second)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "unexpected status: 409")
}

func TestRunConnectionError(t *testing.T) {
	t.Parallel()

	d := New("http://localhost:59999") // No server running
	result, err := d.Run("connection test", 1*time.Second)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "submitting task")
}

func TestRunPollTimeout(t *testing.T) {
	t.Parallel()

	taskID := "task-timeout"

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": "sess",
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			// Always return working - never completes
			json.NewEncoder(w).Encode(TaskResult{
				TaskID: taskID,
				State:  "working",
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("timeout test", 300*time.Millisecond)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "did not complete within timeout")
}

func TestRunPollError(t *testing.T) {
	t.Parallel()

	taskID := "task-pollerr"
	var submitted bool

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/task":
			submitted = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskID,
				"session_id": "sess",
			})

		case r.Method == "GET" && r.URL.Path == "/task/"+taskID:
			// Return invalid JSON to cause decode error
			w.Write([]byte("not json"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	d := New(agent.URL)
	result, err := d.Run("poll error test", 5*time.Second)

	require.True(t, submitted)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "decoding status")
}
