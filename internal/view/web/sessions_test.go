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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
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
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	body := `{"state": "completed"}`
	req := httptest.NewRequest("PUT", "/api/sessions/nonexistent/tasks/task-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleUpdateSessionTask(rec, req, "nonexistent", "task-1")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSessionStoreAddTaskWithSource(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add task with source options
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "scheduled task",
		WithSource("scheduler"),
		WithSourceJob("nightly-maintenance"))

	session, ok := store.Get("session-1")
	require.True(t, ok)
	require.Equal(t, "scheduler", session.Source)
	require.Equal(t, "nightly-maintenance", session.SourceJob)
	require.Len(t, session.Tasks, 1)

	// Add task without source (default behavior)
	store.AddTask("session-2", "http://agent:9000", "task-2", "working", "web task")

	session2, ok := store.Get("session-2")
	require.True(t, ok)
	require.Empty(t, session2.Source)
	require.Empty(t, session2.SourceJob)
}

func TestSessionSourceInJSON(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add task with source
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "prompt",
		WithSource("cli"),
		WithSourceJob(""))

	sessions := store.GetAll()
	require.Len(t, sessions, 1)

	// Marshal to JSON and verify source fields
	data, err := json.Marshal(sessions[0])
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	require.Equal(t, "cli", parsed["source"])
	// source_job should be omitted if empty
	_, hasSourceJob := parsed["source_job"]
	require.False(t, hasSourceJob, "source_job should be omitted when empty")
}

func TestSessionStoreArchive(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add a session
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "prompt")

	// Verify session exists and is not archived
	session, ok := store.Get("session-1")
	require.True(t, ok)
	require.False(t, session.Archived)

	// Archive the session
	archived := store.Archive("session-1")
	require.True(t, archived)

	// Verify session is now archived
	session, ok = store.Get("session-1")
	require.True(t, ok)
	require.True(t, session.Archived)

	// Archive non-existent session
	archived = store.Archive("session-999")
	require.False(t, archived)
}

func TestSessionStoreGetAllExcludesArchived(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add multiple sessions
	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	store.AddTask("session-2", "http://agent:9001", "task-2", "working", "prompt 2")
	store.AddTask("session-3", "http://agent:9002", "task-3", "completed", "prompt 3")

	// All sessions should be returned
	sessions := store.GetAll()
	require.Len(t, sessions, 3)

	// Archive one session
	store.Archive("session-2")

	// GetAll should now return only 2 sessions
	sessions = store.GetAll()
	require.Len(t, sessions, 2)

	// Verify archived session is not in the list
	for _, s := range sessions {
		require.NotEqual(t, "session-2", s.ID, "Archived session should not appear in GetAll")
	}

	// But archived session should still be accessible via Get
	session, ok := store.Get("session-2")
	require.True(t, ok)
	require.True(t, session.Archived)
}

func TestHandleArchiveSession(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	// Create a session
	handlers.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt")

	req := httptest.NewRequest("POST", "/api/sessions/sess-1/archive", nil)
	rec := httptest.NewRecorder()

	handlers.HandleArchiveSession(rec, req, "sess-1")

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify session is archived
	session, ok := handlers.sessionStore.Get("sess-1")
	require.True(t, ok)
	require.True(t, session.Archived)

	// Verify session no longer appears in GetAll
	sessions := handlers.sessionStore.GetAll()
	require.Empty(t, sessions)
}

func TestHandleArchiveSessionNotFound(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/sessions/nonexistent/archive", nil)
	rec := httptest.NewRecorder()

	handlers.HandleArchiveSession(rec, req, "nonexistent")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSessionArchivedFieldInJSON(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add and archive a session
	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "prompt")
	store.Archive("session-1")

	session, _ := store.Get("session-1")

	// Marshal to JSON and verify archived field
	data, err := json.Marshal(session)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	require.Equal(t, true, parsed["archived"])

	// Non-archived session should omit the field
	store.AddTask("session-2", "http://agent:9000", "task-2", "working", "prompt 2")
	session2, _ := store.Get("session-2")

	data, err = json.Marshal(session2)
	require.NoError(t, err)

	var parsed2 map[string]interface{}
	err = json.Unmarshal(data, &parsed2)
	require.NoError(t, err)

	_, hasArchived := parsed2["archived"]
	require.False(t, hasArchived, "archived should be omitted when false")
}

// Archive interaction tests

func TestAddTaskToArchivedSession(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Create and archive a session
	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "first task")
	store.Archive("session-1")

	// Add another task to the archived session
	store.AddTask("session-1", "http://agent:9000", "task-2", "working", "second task")

	// Session should still be accessible and have both tasks
	session, ok := store.Get("session-1")
	require.True(t, ok)
	require.Len(t, session.Tasks, 2)
	require.Equal(t, "task-2", session.Tasks[1].TaskID)

	// Session should remain archived
	require.True(t, session.Archived, "Session should remain archived after adding task")

	// Session should still NOT appear in GetAll because it's archived
	sessions := store.GetAll()
	require.Empty(t, sessions, "Archived session should not appear in GetAll even after adding task")
}

func TestUpdateTaskStateOnArchivedSession(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Create session with a working task, then archive
	store.AddTask("session-1", "http://agent:9000", "task-1", "working", "task prompt")
	store.Archive("session-1")

	// Update task state on archived session
	updated := store.UpdateTaskState("session-1", "task-1", "completed")
	require.True(t, updated, "Should be able to update task state on archived session")

	// Verify the update
	session, _ := store.Get("session-1")
	require.Equal(t, "completed", session.Tasks[0].State)
	require.True(t, session.Archived, "Session should remain archived")
}

func TestArchiveUpdatesTimestamp(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "prompt")
	session, _ := store.Get("session-1")
	originalTime := session.UpdatedAt

	time.Sleep(10 * time.Millisecond)
	store.Archive("session-1")

	session, _ = store.Get("session-1")
	require.True(t, session.UpdatedAt.After(originalTime), "Archive should update timestamp")
}

func TestArchiveIdempotent(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "prompt")

	// Archive multiple times
	archived1 := store.Archive("session-1")
	require.True(t, archived1)

	archived2 := store.Archive("session-1")
	require.True(t, archived2, "Archiving already archived session should succeed")

	session, _ := store.Get("session-1")
	require.True(t, session.Archived)
}

func TestHandleSessionsExcludesArchived(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	// Add sessions, archive one
	handlers.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	handlers.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "working", "prompt 2")
	handlers.sessionStore.Archive("sess-1")

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()

	handlers.HandleSessions(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var sessions []*Session
	err = json.Unmarshal(rec.Body.Bytes(), &sessions)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-2", sessions[0].ID)
}

func TestHandleAddSessionTaskToArchivedSession(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	// Create and archive a session
	handlers.sessionStore.AddTask("sess-archived", "http://agent:9000", "task-1", "completed", "first")
	handlers.sessionStore.Archive("sess-archived")

	// Try adding task to archived session via API
	body := `{
		"session_id": "sess-archived",
		"agent_url": "http://agent:9000",
		"task_id": "task-2",
		"state": "working",
		"prompt": "second task"
	}`

	req := httptest.NewRequest("POST", "/api/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleAddSessionTask(rec, req)

	// Should succeed - adding tasks to archived sessions is allowed
	require.Equal(t, http.StatusCreated, rec.Code)

	// Verify task was added
	session, ok := handlers.sessionStore.Get("sess-archived")
	require.True(t, ok)
	require.Len(t, session.Tasks, 2)
	require.True(t, session.Archived, "Session should remain archived")
}

func TestHandleUpdateSessionTaskOnArchivedSession(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	// Create session with working task, then archive
	handlers.sessionStore.AddTask("sess-archived", "http://agent:9000", "task-1", "working", "prompt")
	handlers.sessionStore.Archive("sess-archived")

	// Update task state via API
	body := `{"state": "completed"}`
	req := httptest.NewRequest("PUT", "/api/sessions/sess-archived/tasks/task-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handlers.HandleUpdateSessionTask(rec, req, "sess-archived", "task-1")

	// Should succeed - updating tasks on archived sessions is allowed
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify state was updated
	session, _ := handlers.sessionStore.Get("sess-archived")
	require.Equal(t, "completed", session.Tasks[0].State)
	require.True(t, session.Archived, "Session should remain archived")
}

func TestArchiveDoesNotAffectOtherSessions(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	// Add multiple sessions
	store.AddTask("session-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	store.AddTask("session-2", "http://agent:9001", "task-2", "working", "prompt 2")
	store.AddTask("session-3", "http://agent:9002", "task-3", "completed", "prompt 3")

	// Archive one
	store.Archive("session-2")

	// Verify other sessions are unaffected
	sess1, _ := store.Get("session-1")
	sess3, _ := store.Get("session-3")
	require.False(t, sess1.Archived)
	require.False(t, sess3.Archived)

	// GetAll should return the non-archived ones
	sessions := store.GetAll()
	require.Len(t, sessions, 2)
}

func TestArchiveAlreadyArchivedSession(t *testing.T) {
	t.Parallel()

	discovery := NewDiscovery(DiscoveryConfig{PortStart: 9900, PortEnd: 9900})
	handlers, err := NewHandlers(discovery, "test", nil, nil, false)
	require.NoError(t, err)

	// Create and archive a session
	handlers.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt")
	handlers.sessionStore.Archive("sess-1")

	// Try to archive again via API
	req := httptest.NewRequest("POST", "/api/sessions/sess-1/archive", nil)
	rec := httptest.NewRecorder()

	handlers.HandleArchiveSession(rec, req, "sess-1")

	// Should succeed (idempotent)
	require.Equal(t, http.StatusOK, rec.Code)
}
