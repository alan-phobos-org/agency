package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionStoreAddTask(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add first task - should create session
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "do something")

	session, ok := store.Get("session-1")
	require.True(t, ok)
	require.Equal(t, "session-1", session.ID)
	require.Equal(t, "http://agent:9000", session.AgentURL)
	require.Len(t, session.Tasks, 1)
	require.Equal(t, "task-1", session.Tasks[0].TaskID)
	require.Equal(t, "working", session.Tasks[0].State)
	require.Equal(t, "do something", session.Tasks[0].Prompt)

	// Add second task to same session
	store.AddTask("session-1", "http://agent:9000", "task-2", "working", "do more")

	session, _ = store.Get("session-1")
	require.Len(t, session.Tasks, 2)
	require.Equal(t, "task-2", session.Tasks[1].TaskID)
}

func TestSessionStoreGetAll(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Empty store
	sessions := store.GetAll()
	require.Empty(t, sessions)

	// Add sessions
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "prompt 1")
	store.AddTask("session-2", "http://agent:9001", "task-2", "completed", "prompt 2")

	sessions = store.GetAll()
	require.Len(t, sessions, 2)
}

func TestSessionStoreGetAllSortedByUpdatedAt(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add sessions in a specific order
	store.AddTask("session-old", "http://agent:9000", "task-1", "completed", "old prompt")
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	store.AddTask("session-new", "http://agent:9001", "task-2", "working", "new prompt")

	// GetAll should return newest first
	sessions := store.GetAll()
	require.Len(t, sessions, 2)
	require.Equal(t, "session-new", sessions[0].ID, "Newest session should be first")
	require.Equal(t, "session-old", sessions[1].ID, "Older session should be second")

	// Update old session - it should now be first
	time.Sleep(10 * time.Millisecond)
	store.UpdateTaskState("session-old", "task-1", "failed")

	sessions = store.GetAll()
	require.Equal(t, "session-old", sessions[0].ID, "Recently updated session should be first")
	require.Equal(t, "session-new", sessions[1].ID, "Less recently updated should be second")
}

func TestSessionStoreGetAllSortedAfterAddingTasks(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Create two sessions
	store.AddTask("session-A", "http://agent:9000", "task-1", "completed", "prompt A")
	time.Sleep(10 * time.Millisecond)
	store.AddTask("session-B", "http://agent:9001", "task-2", "completed", "prompt B")

	// B is newest
	sessions := store.GetAll()
	require.Equal(t, "session-B", sessions[0].ID)

	// Add task to A - should make it newest
	time.Sleep(10 * time.Millisecond)
	store.AddTask("session-A", "http://agent:9000", "task-3", "working", "prompt A2")

	sessions = store.GetAll()
	require.Equal(t, "session-A", sessions[0].ID, "Session with newly added task should be first")
}

func TestSessionStoreUpdateTaskState(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add a session with tasks
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "prompt")
	store.AddTask("session-1", "http://agent:9000", "task-2", "working", "prompt 2")

	// Update task state
	updated := store.UpdateTaskState("session-1", "task-1", "completed")
	require.True(t, updated)

	session, _ := store.Get("session-1")
	require.Equal(t, "completed", session.Tasks[0].State)
	require.Equal(t, "working", session.Tasks[1].State)

	// Update non-existent task
	updated = store.UpdateTaskState("session-1", "task-999", "failed")
	require.False(t, updated)

	// Update non-existent session
	updated = store.UpdateTaskState("session-999", "task-1", "failed")
	require.False(t, updated)
}

func TestSessionStoreDelete(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "prompt")

	_, ok := store.Get("session-1")
	require.True(t, ok)

	store.Delete("session-1")

	_, ok = store.Get("session-1")
	require.False(t, ok)
}

func TestHandleSessions(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	// Add some sessions
	handlers.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	handlers.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "working", "prompt 2")

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()

	handlers.HandleSessions(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var sessions []*Session
	err = json.Unmarshal(rec.Body.Bytes(), &sessions)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
}

func TestHandleSessionsEmpty(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()

	handlers.HandleSessions(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var sessions []*Session
	err = json.Unmarshal(rec.Body.Bytes(), &sessions)
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func TestHandleAddSessionTask(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	body := `{
		"session_id": "new-session",
		"agent_url": "http://agent:9000",
		"task_id": "task-123",
		"state": "working",
		"prompt": "test prompt"
	}`

	req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleAddSessionTask(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	// Verify session was created
	session, ok := handlers.sessionStore.Get("new-session")
	require.True(t, ok)
	require.Equal(t, "http://agent:9000", session.AgentURL)
	require.Len(t, session.Tasks, 1)
	require.Equal(t, "task-123", session.Tasks[0].TaskID)
}

func TestHandleAddSessionTaskValidation(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	// Missing session_id
	body := `{"task_id": "task-123"}`
	req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleAddSessionTask(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Missing task_id
	body = `{"session_id": "sess-1"}`
	req = httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()

	handlers.HandleAddSessionTask(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// Invalid JSON
	req = httptest.NewRequest("POST", "/api/sessions", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()

	handlers.HandleAddSessionTask(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpdateSessionTask(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	// Create a session with a task
	handlers.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "working", "prompt")

	body := `{"state": "completed"}`
	req := httptest.NewRequest("PUT", "/api/sessions/sess-1/tasks/task-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleUpdateSessionTask(rec, req, "sess-1", "task-1")

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify state was updated
	session, _ := handlers.sessionStore.Get("sess-1")
	require.Equal(t, "completed", session.Tasks[0].State)
}

func TestHandleUpdateSessionTaskNotFound(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, nil, false)
	require.NoError(t, err)

	body := `{"state": "completed"}`
	req := httptest.NewRequest("PUT", "/api/sessions/nonexistent/tasks/task-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleUpdateSessionTask(rec, req, "nonexistent", "task-1")

	require.Equal(t, http.StatusNotFound, rec.Code)
}
