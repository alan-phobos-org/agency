package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueueHandlerSubmit(t *testing.T) {
	t.Parallel()

	// Create queue
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Submit task
	body := `{"prompt": "Test task", "source": "cli"}`
	req := httptest.NewRequest("POST", "/api/queue/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleQueueSubmit(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	var resp QueueSubmitResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.QueueID)
	require.Equal(t, 1, resp.Position)
	require.Equal(t, "pending", resp.State)
}

func TestQueueHandlerSubmitValidation(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Missing prompt
	body := `{"source": "cli"}`
	req := httptest.NewRequest("POST", "/api/queue/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleQueueSubmit(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestQueueHandlerSubmitQueueFull(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 1, // Very small queue
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Fill the queue
	body := `{"prompt": "First task"}`
	req := httptest.NewRequest("POST", "/api/queue/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleQueueSubmit(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Try to add another
	req = httptest.NewRequest("POST", "/api/queue/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.HandleQueueSubmit(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestQueueHandlerStatus(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Add some tasks
	q.Add(QueueSubmitRequest{Prompt: "Task 1", Source: "web"})
	q.Add(QueueSubmitRequest{Prompt: "Task 2", Source: "scheduler"})

	req := httptest.NewRequest("GET", "/api/queue", nil)
	rec := httptest.NewRecorder()

	h.HandleQueueStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp QueueStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, 2, resp.Depth)
	require.Equal(t, 50, resp.MaxSize)
	require.Len(t, resp.Tasks, 2)
}

func TestQueueHandlerTaskStatus(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Add a task
	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "Test task"})

	req := httptest.NewRequest("GET", "/api/queue/"+task.QueueID, nil)
	rec := httptest.NewRecorder()

	h.HandleQueueTaskStatus(rec, req, task.QueueID)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp QueuedTaskDetail
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, task.QueueID, resp.QueueID)
	require.Equal(t, "pending", resp.State)
	require.Equal(t, 1, resp.Position)
}

func TestQueueHandlerTaskStatusNotFound(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	req := httptest.NewRequest("GET", "/api/queue/nonexistent", nil)
	rec := httptest.NewRecorder()

	h.HandleQueueTaskStatus(rec, req, "nonexistent")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestQueueHandlerCancel(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	// Add a task
	task, _, _ := q.Add(QueueSubmitRequest{Prompt: "Test task"})

	req := httptest.NewRequest("POST", "/api/queue/"+task.QueueID+"/cancel", nil)
	rec := httptest.NewRecorder()

	h.HandleQueueCancel(rec, req, task.QueueID)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp QueueCancelResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, task.QueueID, resp.QueueID)
	require.Equal(t, "cancelled", resp.State)
	require.False(t, resp.WasDispatched)

	// Verify task was removed
	require.Nil(t, q.Get(task.QueueID))
}

func TestQueueHandlerCancelNotFound(t *testing.T) {
	t.Parallel()

	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := NewQueueHandlers(q, d, NewSessionStore())

	req := httptest.NewRequest("POST", "/api/queue/nonexistent/cancel", nil)
	rec := httptest.NewRecorder()

	h.HandleQueueCancel(rec, req, "nonexistent")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestQueueHandlerTaskSubmitViaQueueDirect(t *testing.T) {
	t.Parallel()

	// Create mock agent
	agentCalled := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":  "agent",
				"state": "idle",
			})
			return
		}
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    "task-123",
				"session_id": "session-456",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	// Setup
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	// Manually add agent to discovery
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()

	ss := NewSessionStore()
	h := NewQueueHandlers(q, d, ss)

	// Submit with agent_url - should go directly to idle agent
	body := `{"agent_url": "` + agent.URL + `", "prompt": "Test task"}`
	req := httptest.NewRequest("POST", "/api/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleTaskSubmitViaQueue(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, agentCalled, "Agent should have been called directly")

	var resp TaskSubmitResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "task-123", resp.TaskID)
}

func TestQueueHandlerTaskSubmitViaQueueQueued(t *testing.T) {
	t.Parallel()

	// Setup
	q, err := NewWorkQueue(QueueConfig{
		Dir:     t.TempDir(),
		MaxSize: 50,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	ss := NewSessionStore()
	h := NewQueueHandlers(q, d, ss)

	// Submit without agent_url - should be queued
	body := `{"prompt": "Test task"}`
	req := httptest.NewRequest("POST", "/api/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleTaskSubmitViaQueue(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp["queue_id"])
	require.Equal(t, "pending", resp["state"])
}

func TestQueueTaskBWhileTaskARunning(t *testing.T) {
	t.Parallel()

	// Track agent state and task submissions
	var mu sync.Mutex
	agentState := "working" // Agent starts busy with task A
	taskAID := "task-A"
	taskBID := "task-B"
	taskSubmissions := []string{}

	// Create mock agent that tracks state transitions
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		currentState := agentState
		mu.Unlock()

		if r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":  "agent",
				"state": currentState,
			})
			return
		}
		if r.URL.Path == "/task" && r.Method == "POST" {
			mu.Lock()
			if currentState == "working" {
				mu.Unlock()
				// Agent busy - reject with 409
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": "agent busy"})
				return
			}
			taskSubmissions = append(taskSubmissions, taskBID)
			agentState = "working" // Now working on task B
			mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id":    taskBID,
				"session_id": "session-B",
			})
			return
		}
		if r.URL.Path == "/task/"+taskAID {
			// Task A status - return working or completed
			mu.Lock()
			state := "completed" // Task A done
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": taskAID,
				"state":   state,
			})
			return
		}
		if r.URL.Path == "/task/"+taskBID {
			// Task B status
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": taskBID,
				"state":   "working",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	// Setup queue and discovery
	q, err := NewWorkQueue(QueueConfig{
		Dir:             t.TempDir(),
		MaxSize:         50,
		DispatchTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	// Add agent to discovery as "working"
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "working",
	}
	d.mu.Unlock()

	ss := NewSessionStore()
	h := NewQueueHandlers(q, d, ss)

	// Step 1: Submit task B while agent is busy with task A
	body := `{"prompt": "Task B prompt"}`
	req := httptest.NewRequest("POST", "/api/queue/task", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleQueueSubmit(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	var submitResp QueueSubmitResponse
	err = json.Unmarshal(rec.Body.Bytes(), &submitResp)
	require.NoError(t, err)
	require.NotEmpty(t, submitResp.QueueID)
	require.Equal(t, "pending", submitResp.State)
	require.Equal(t, 1, submitResp.Position)

	queueID := submitResp.QueueID

	// Verify task B is pending in queue
	task := q.Get(queueID)
	require.NotNil(t, task)
	require.True(t, task.State.IsPending(), "expected pending state, got %s", task.State)

	// Step 2: Simulate task A completing - agent becomes idle
	mu.Lock()
	agentState = "idle"
	mu.Unlock()

	// Update discovery to reflect agent is now idle
	d.mu.Lock()
	d.components[agent.URL].State = "idle"
	d.mu.Unlock()

	// Step 3: Create dispatcher and dispatch one task
	dispatcher := NewDispatcher(q, d, ss)

	// Trigger dispatcher manually (simulating one tick)
	dispatcher.dispatchNext()

	// Step 4: Verify task B was dispatched
	task = q.Get(queueID)
	require.NotNil(t, task)
	// Should be either dispatching or working
	require.True(t, task.State.IsDispatched(),
		"Task should move to dispatching or working state, got %s", task.State)

	// Verify agent received the task
	mu.Lock()
	submissions := taskSubmissions
	mu.Unlock()
	require.Len(t, submissions, 1, "Agent should have received exactly one task submission")
	require.Equal(t, taskBID, submissions[0])

	// Verify task has agent info
	require.Equal(t, agent.URL, task.AgentURL)
	require.Equal(t, taskBID, task.TaskID)
}
