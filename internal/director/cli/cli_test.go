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

// --- Director Routing Tests ---

func TestDirectorRouting(t *testing.T) {
	t.Parallel()

	var directorCalled atomic.Bool
	var receivedRequest map[string]interface{}

	// Mock director server
	directorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/task" && r.Method == "POST" {
			directorCalled.Store(true)
			json.NewDecoder(r.Body).Decode(&receivedRequest)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-123",
				"session_id": "sess-abc",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer directorServer.Close()

	// Mock agent server for polling
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task/task-123" && r.Method == "GET" {
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:    "task-123",
				State:     "completed",
				Output:    "done",
				SessionID: "sess-abc",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL(directorServer.URL))

	result, err := d.Run("test prompt", 5*time.Second)

	require.NoError(t, err)
	require.True(t, directorCalled.Load(), "Director should be called first")
	require.Equal(t, "task-123", result.TaskID)
	require.Equal(t, "completed", result.State)
	require.Equal(t, "sess-abc", result.SessionID)

	// Verify request format to director
	require.Equal(t, agentServer.URL, receivedRequest["agent_url"])
	require.Equal(t, "test prompt", receivedRequest["prompt"])
	require.Equal(t, "cli", receivedRequest["source"])
}

func TestDirectorFallbackToAgent(t *testing.T) {
	t.Parallel()

	var agentSubmitCalled atomic.Bool

	// Mock director server that fails
	directorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer directorServer.Close()

	// Mock agent server
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentSubmitCalled.Store(true)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-456",
				"session_id": "sess-def",
			})
			return
		}
		if r.URL.Path == "/task/task-456" && r.Method == "GET" {
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:    "task-456",
				State:     "completed",
				Output:    "fallback done",
				SessionID: "sess-def",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL(directorServer.URL))

	result, err := d.Run("test prompt", 5*time.Second)

	require.NoError(t, err)
	require.True(t, agentSubmitCalled.Load(), "Agent should be called as fallback")
	require.Equal(t, "task-456", result.TaskID)
	require.Equal(t, "completed", result.State)
}

func TestDirectorUnavailableFallbackFails(t *testing.T) {
	t.Parallel()

	// Both director and agent fail
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL("http://localhost:1"))

	_, err := d.Run("test prompt", 2*time.Second)

	require.Error(t, err)
}

func TestWithDirectorURLOption(t *testing.T) {
	t.Parallel()

	d := New("http://agent:9000", WithDirectorURL("http://director:8080"))

	require.Equal(t, "http://agent:9000", d.agentURL)
	require.Equal(t, "http://director:8080", d.directorURL)
}

func TestDirectorTimeoutSeconds(t *testing.T) {
	t.Parallel()

	var receivedTimeout float64

	directorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/task" && r.Method == "POST" {
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			receivedTimeout = req["timeout_seconds"].(float64)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-timeout",
				"session_id": "sess-timeout",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer directorServer.Close()

	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task/task-timeout" && r.Method == "GET" {
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:    "task-timeout",
				State:     "completed",
				SessionID: "sess-timeout",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL(directorServer.URL))

	_, err := d.Run("test", 30*time.Second)

	require.NoError(t, err)
	require.Equal(t, float64(30), receivedTimeout, "Timeout should be passed to director")
}

func TestSessionIDPreservedFromDirector(t *testing.T) {
	t.Parallel()

	// Mock director server
	directorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/task" && r.Method == "POST" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-sess",
				"session_id": "original-session",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer directorServer.Close()

	// Mock agent server that doesn't return session_id in poll
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task/task-sess" && r.Method == "GET" {
			// Omit session_id from poll response
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": "task-sess",
				"state":   "completed",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL(directorServer.URL))

	result, err := d.Run("test", 5*time.Second)

	require.NoError(t, err)
	require.Equal(t, "original-session", result.SessionID, "Session ID should be preserved from director response")
}

func TestDirectorNon201Status(t *testing.T) {
	t.Parallel()

	var agentCalled atomic.Bool

	// Mock director that returns 400
	directorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer directorServer.Close()

	// Mock agent as fallback
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentCalled.Store(true)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-fallback",
				"session_id": "sess-fallback",
			})
			return
		}
		if r.URL.Path == "/task/task-fallback" && r.Method == "GET" {
			json.NewEncoder(w).Encode(TaskResult{
				TaskID:    "task-fallback",
				State:     "completed",
				SessionID: "sess-fallback",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agentServer.Close()

	d := New(agentServer.URL, WithDirectorURL(directorServer.URL))

	result, err := d.Run("test", 5*time.Second)

	require.NoError(t, err)
	require.True(t, agentCalled.Load(), "Should fallback to agent on director non-201")
	require.Equal(t, "task-fallback", result.TaskID)
}
