package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestHandlers creates a Handlers instance for testing with a temporary auth store
func newTestHandlers(t *testing.T, d *Discovery, version string, contexts *ContextsConfig) *Handlers {
	t.Helper()
	dir := t.TempDir()
	authStore, err := NewAuthStore(filepath.Join(dir, "auth.json"), "")
	require.NoError(t, err)

	h, err := NewHandlers(d, version, contexts, authStore, false)
	require.NoError(t, err)
	return h
}

func TestHandleStatus(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test-version", nil)

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()

	h.HandleStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Equal(t, "view", resp["type"])
	require.Equal(t, []interface{}{"statusable", "observable", "taskable"}, resp["interfaces"])
	require.Equal(t, "test-version", resp["version"])
	require.Equal(t, "running", resp["state"])
	require.NotNil(t, resp["uptime_seconds"])
}

func TestHandleAgents(t *testing.T) {
	t.Parallel()

	// Setup mock agent
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "agent",
			"interfaces": []string{"statusable", "taskable"},
			"version":    "agent-v1",
			"state":      "idle",
		})
	}))
	defer agent.Close()

	port := extractPort(t, agent.URL)
	d := NewDiscovery(DiscoveryConfig{PortStart: port, PortEnd: port})
	d.scan()

	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	rec := httptest.NewRecorder()

	h.HandleAgents(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var agents []*ComponentStatus
	err := json.Unmarshal(rec.Body.Bytes(), &agents)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	require.Equal(t, "idle", agents[0].State)
}

func TestHandleAgentsEmpty(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	rec := httptest.NewRecorder()

	h.HandleAgents(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var agents []interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &agents)
	require.NoError(t, err)
	require.Len(t, agents, 0)
}

func TestHandleDirectors(t *testing.T) {
	t.Parallel()

	// Setup mock director
	director := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "director",
			"interfaces": []string{"statusable", "observable", "taskable"},
			"version":    "dir-v1",
			"state":      "running",
		})
	}))
	defer director.Close()

	port := extractPort(t, director.URL)
	d := NewDiscovery(DiscoveryConfig{PortStart: port, PortEnd: port})
	d.scan()

	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/directors", nil)
	rec := httptest.NewRecorder()

	h.HandleDirectors(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var directors []*ComponentStatus
	err := json.Unmarshal(rec.Body.Bytes(), &directors)
	require.NoError(t, err)
	require.Len(t, directors, 1)
	require.Equal(t, "running", directors[0].State)
}

func TestHandleTaskSubmitValidation(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing agent_url",
			body:    `{"prompt": "test"}`,
			wantErr: "agent_url is required",
		},
		{
			name:    "missing prompt",
			body:    `{"agent_url": "http://localhost:9000"}`,
			wantErr: "prompt is required",
		},
		{
			name:    "invalid json",
			body:    `{invalid}`,
			wantErr: "Invalid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/task", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			h.HandleTaskSubmit(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), tt.wantErr)
		})
	}
}

func TestHandleTaskSubmitAgentNotFound(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	body := `{"agent_url": "http://localhost:59999", "prompt": "test"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleTaskSubmit(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_not_found")
}

func TestHandleTaskSubmitAgentBusy(t *testing.T) {
	t.Parallel()

	// Setup mock agent in working state
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "agent",
			"interfaces": []string{"statusable", "taskable"},
			"version":    "v1",
			"state":      "working",
		})
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	// Manually add the mock agent to discovery cache
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "working",
	}
	d.mu.Unlock()

	h := newTestHandlers(t, d, "test", nil)

	body := `{"agent_url": "` + agent.URL + `", "prompt": "test"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleTaskSubmit(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_busy")
}

func TestHandleTaskSubmitSuccess(t *testing.T) {
	t.Parallel()

	// Setup mock agent that accepts tasks
	taskReceived := false
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type": "agent", "state": "idle",
			})
		case "/task":
			taskReceived = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": "task-test-123",
			})
		}
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	// Manually add the mock agent to discovery cache
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()

	h := newTestHandlers(t, d, "test", nil)

	body := `{"agent_url": "` + agent.URL + `", "prompt": "test prompt"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleTaskSubmit(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, taskReceived, "Agent should have received task")

	var resp TaskSubmitResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "task-test-123", resp.TaskID)
	require.Equal(t, agent.URL, resp.AgentURL)
}

func TestHandleTaskStatusMissingAgentURL(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/task/task-123", nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-123")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_url query parameter is required")
}

func TestHandleTaskStatusForwarding(t *testing.T) {
	t.Parallel()

	// Setup mock agent
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/task/") {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": "task-123",
				"state":   "completed",
				"output":  "Task done",
			})
		}
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/task/task-123?agent_url="+agent.URL, nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-123")

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "completed", resp["state"])
}

func TestHandleDashboard(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboard(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "Agency Dashboard")
	require.Contains(t, rec.Body.String(), "alpinejs") // Alpine.js reference
}

func TestNewHandlersTemplateLoading(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "v1.2.3", nil)
	require.NotNil(t, h)
	require.Equal(t, "v1.2.3", h.version)
	require.False(t, h.startTime.IsZero())
}

func TestHandleStatusUptime(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Wait a bit to get measurable uptime
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()

	h.HandleStatus(rec, req)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	uptime := resp["uptime_seconds"].(float64)
	require.Greater(t, uptime, 0.0, "Uptime should be positive")
}

func TestHandleDashboardData(t *testing.T) {
	t.Parallel()

	// Create mock agent
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "agent",
			"interfaces": []string{"statusable", "taskable"},
			"version":    "agent-v1",
			"state":      "idle",
		})
	}))
	defer agent.Close()

	// Create mock director
	director := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "director",
			"interfaces": []string{"statusable", "observable", "taskable"},
			"version":    "dir-v1",
			"state":      "running",
		})
	}))
	defer director.Close()

	agentPort := extractPort(t, agent.URL)
	directorPort := extractPort(t, director.URL)

	// Instead of scanning a port range (which may include other processes),
	// directly check the specific ports we created
	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	d.checkPort(agentPort)
	d.checkPort(directorPort)

	h := newTestHandlers(t, d, "test", nil)

	// Add some sessions
	h.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	h.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "working", "prompt 2")

	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboardData(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var data DashboardData
	err := json.Unmarshal(rec.Body.Bytes(), &data)
	require.NoError(t, err)

	// Should have agents, directors, and sessions
	require.GreaterOrEqual(t, len(data.Agents), 1, "Should have at least 1 agent")
	require.GreaterOrEqual(t, len(data.Directors), 1, "Should have at least 1 director")
	require.Len(t, data.Sessions, 2, "Should have 2 sessions")
}

func TestHandleDashboardDataEmpty(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboardData(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var data DashboardData
	err := json.Unmarshal(rec.Body.Bytes(), &data)
	require.NoError(t, err)

	require.Empty(t, data.Agents)
	require.Empty(t, data.Directors)
	require.Empty(t, data.Sessions)
}

func TestHandleDashboardDataETag(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// First request - should return data and ETag
	req1 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec1 := httptest.NewRecorder()
	h.HandleDashboardData(rec1, req1)

	require.Equal(t, http.StatusOK, rec1.Code)
	etag := rec1.Header().Get("ETag")
	require.NotEmpty(t, etag, "First response should have ETag header")
	require.True(t, strings.HasPrefix(etag, `"`), "ETag should be quoted")
	require.True(t, strings.HasSuffix(etag, `"`), "ETag should be quoted")

	// Second request with matching ETag - should return 304
	req2 := httptest.NewRequest("GET", "/api/dashboard", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.HandleDashboardData(rec2, req2)

	require.Equal(t, http.StatusNotModified, rec2.Code)
	require.Empty(t, rec2.Body.Bytes(), "304 response should have no body")
}

func TestHandleDashboardDataETagChangesOnUpdate(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// First request
	req1 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec1 := httptest.NewRecorder()
	h.HandleDashboardData(rec1, req1)
	etag1 := rec1.Header().Get("ETag")

	// Add a session - data changes
	h.sessionStore.AddTask("new-session", "http://agent:9000", "task-1", "working", "prompt")

	// Second request - ETag should be different
	req2 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec2 := httptest.NewRecorder()
	h.HandleDashboardData(rec2, req2)
	etag2 := rec2.Header().Get("ETag")

	require.NotEqual(t, etag1, etag2, "ETag should change when data changes")

	// Request with old ETag should get new data, not 304
	req3 := httptest.NewRequest("GET", "/api/dashboard", nil)
	req3.Header.Set("If-None-Match", etag1)
	rec3 := httptest.NewRecorder()
	h.HandleDashboardData(rec3, req3)

	require.Equal(t, http.StatusOK, rec3.Code, "Old ETag should not match, returns 200")
}

func TestHandleDashboardDataETagMismatch(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Request with wrong ETag should return 200 with data
	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	req.Header.Set("If-None-Match", `"wrong-etag"`)
	rec := httptest.NewRecorder()
	h.HandleDashboardData(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, rec.Body.Bytes(), "Should return data for mismatched ETag")
}

func TestHandleDashboardDataSessionsSorted(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Add sessions with different timestamps
	h.sessionStore.AddTask("sess-old", "http://agent:9000", "task-1", "completed", "old")
	time.Sleep(10 * time.Millisecond)
	h.sessionStore.AddTask("sess-new", "http://agent:9001", "task-2", "working", "new")

	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()
	h.HandleDashboardData(rec, req)

	var data DashboardData
	json.Unmarshal(rec.Body.Bytes(), &data)

	require.Len(t, data.Sessions, 2)
	require.Equal(t, "sess-new", data.Sessions[0].ID, "Newest session should be first")
	require.Equal(t, "sess-old", data.Sessions[1].ID, "Older session should be second")
}

func TestHandleContextsNoContexts(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/contexts", nil)
	rec := httptest.NewRecorder()
	h.HandleContexts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var contexts []Context
	err := json.Unmarshal(rec.Body.Bytes(), &contexts)
	require.NoError(t, err)

	// Should have just manual context
	require.Len(t, contexts, 1)
	require.Equal(t, "manual", contexts[0].ID)
	require.Equal(t, "Manual", contexts[0].Name)
}

func TestHandleDashboardContainsSessionDetail(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboard(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Alpine.js dashboard uses x-data for state management
	require.Contains(t, body, `x-data="dashboard()"`, "Should have Alpine.js dashboard component")

	// Verify session card structure (Alpine.js version uses session-card instead of session-row)
	require.Contains(t, body, "session-card", "Should have session-card class")
	require.Contains(t, body, "session-body", "Should have session-body for expansion")
	require.Contains(t, body, "expandedSession", "Should track expanded session")

	// Verify session history functionality
	require.Contains(t, body, "loadSessionHistory", "Should have loadSessionHistory function")
	require.Contains(t, body, "sessionHistory", "Should track session history")
	require.Contains(t, body, "toggleSession", "Should have toggleSession function")
}

func TestHandleDashboardSessionDetailCSS(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboard(rec, req)

	body := rec.Body.String()

	// Verify CSS classes for Alpine.js session structure
	require.Contains(t, body, ".session-card", "Should have session-card CSS")
	require.Contains(t, body, ".session-header", "Should have session-header CSS")
	require.Contains(t, body, ".session-body", "Should have session-body CSS")
	require.Contains(t, body, ".io-block", "Should have io-block CSS for I/O display")
	require.Contains(t, body, ".io-content", "Should have io-content CSS for output")
}

func TestHandleDashboardContainsReconciliation(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboard(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Alpine.js dashboard polls active tasks for live updates
	require.Contains(t, body, "pollActiveTasks", "Should have pollActiveTasks function")
	require.Contains(t, body, "pollTaskStatus", "Should have pollTaskStatus function")

	// Verify it's called on page load and refresh
	require.Contains(t, body, "refresh()", "Should have refresh function")
	require.Contains(t, body, "startPolling", "Should have startPolling for auto-refresh")

	// Verify unknown state is handled in session status classes
	require.Contains(t, body, "session-status--unknown", "Should handle unknown state")

	// Verify poll error handling includes history fallback
	require.Contains(t, body, "/api/history/", "Should fall back to history on poll error")
}

func TestHandleTaskStatusFallbackToHistory(t *testing.T) {
	t.Parallel()

	// Setup mock agent that returns 404 for /task but has history
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/task/"):
			// Task not found (moved to history)
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "not_found",
				"message": "Task not found",
			})
		case strings.HasPrefix(r.URL.Path, "/history/"):
			// Task found in history with completed state
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": "task-123",
				"state":   "completed",
				"output":  "Task finished successfully",
			})
		}
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/task/task-123?agent_url="+agent.URL, nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-123")

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "completed", resp["state"])
	require.Equal(t, "Task finished successfully", resp["output"])
}

func TestHandleTaskStatusFallbackUpdatesSessionStore(t *testing.T) {
	t.Parallel()

	// Setup mock agent that returns 404 for /task but has history
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/task/"):
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "not_found"})
		case strings.HasPrefix(r.URL.Path, "/history/"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"task_id": "task-456",
				"state":   "failed",
			})
		}
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()
	h := newTestHandlers(t, d, "test", nil)

	// Pre-populate session store with task in "working" state
	h.sessionStore.AddTask("sess-abc", agent.URL, "task-456", "working", "test prompt")

	// Request with session_id should auto-update session store
	req := httptest.NewRequest("GET", "/api/task/task-456?agent_url="+agent.URL+"&session_id=sess-abc", nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-456")

	require.Equal(t, http.StatusOK, rec.Code)

	// Verify session store was updated
	session, ok := h.sessionStore.Get("sess-abc")
	require.True(t, ok)
	require.Len(t, session.Tasks, 1)
	require.Equal(t, "failed", session.Tasks[0].State, "Session store should be updated to failed state")
}

func TestHandleTaskStatusNotFoundInHistoryEither(t *testing.T) {
	t.Parallel()

	// Setup mock agent that returns 404 for both /task and /history
	agent := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "not_found",
			"message": "Not found",
		})
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.mu.Unlock()
	h := newTestHandlers(t, d, "test", nil)

	req := httptest.NewRequest("GET", "/api/task/task-missing?agent_url="+agent.URL, nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-missing")

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleContextsWithContexts(t *testing.T) {
	t.Parallel()

	thinking := true
	cfg := &ContextsConfig{
		Contexts: []Context{
			{
				ID:             "dev",
				Name:           "Development",
				Description:    "Dev workflow",
				Model:          "opus",
				Thinking:       &thinking,
				TimeoutSeconds: 1800,
				PromptPrefix:   "Dev prefix",
			},
			{
				ID:          "quick",
				Name:        "Quick Task",
				Description: "Fast responses",
				Model:       "haiku",
			},
		},
	}

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", cfg)

	req := httptest.NewRequest("GET", "/api/contexts", nil)
	rec := httptest.NewRecorder()
	h.HandleContexts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var contexts []Context
	err := json.Unmarshal(rec.Body.Bytes(), &contexts)
	require.NoError(t, err)

	// Should have manual + 2 configured contexts
	require.Len(t, contexts, 3)
	require.Equal(t, "manual", contexts[0].ID)
	require.Equal(t, "dev", contexts[1].ID)
	require.Equal(t, "quick", contexts[2].ID)

	// Verify dev context fields
	require.Equal(t, "Development", contexts[1].Name)
	require.Equal(t, "Dev workflow", contexts[1].Description)
	require.Equal(t, "opus", contexts[1].Model)
	require.NotNil(t, contexts[1].Thinking)
	require.True(t, *contexts[1].Thinking)
	require.Equal(t, 1800, contexts[1].TimeoutSeconds)
	require.Equal(t, "Dev prefix", contexts[1].PromptPrefix)
}

// Archive interaction tests for dashboard

func TestHandleDashboardDataExcludesArchivedSessions(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Add sessions, archive one
	h.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	h.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "working", "prompt 2")
	h.sessionStore.AddTask("sess-3", "http://agent:9002", "task-3", "completed", "prompt 3")
	h.sessionStore.Archive("sess-2")

	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboardData(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var data DashboardData
	err := json.Unmarshal(rec.Body.Bytes(), &data)
	require.NoError(t, err)

	// Should only have 2 sessions (archived one excluded)
	require.Len(t, data.Sessions, 2)

	// Verify archived session is not included
	for _, s := range data.Sessions {
		require.NotEqual(t, "sess-2", s.ID, "Archived session should not appear in dashboard data")
	}
}

func TestHandleDashboardDataETagChangesOnArchive(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Add sessions
	h.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	h.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "working", "prompt 2")

	// Get initial ETag
	req1 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec1 := httptest.NewRecorder()
	h.HandleDashboardData(rec1, req1)
	etag1 := rec1.Header().Get("ETag")

	// Archive a session
	h.sessionStore.Archive("sess-1")

	// ETag should change
	req2 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec2 := httptest.NewRecorder()
	h.HandleDashboardData(rec2, req2)
	etag2 := rec2.Header().Get("ETag")

	require.NotEqual(t, etag1, etag2, "ETag should change when session is archived")

	// Old ETag should not match
	req3 := httptest.NewRequest("GET", "/api/dashboard", nil)
	req3.Header.Set("If-None-Match", etag1)
	rec3 := httptest.NewRecorder()
	h.HandleDashboardData(rec3, req3)

	require.Equal(t, http.StatusOK, rec3.Code, "Old ETag should not match after archive")
}

func TestHandleDashboardDataAllSessionsArchived(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Add and archive all sessions
	h.sessionStore.AddTask("sess-1", "http://agent:9000", "task-1", "completed", "prompt 1")
	h.sessionStore.AddTask("sess-2", "http://agent:9001", "task-2", "completed", "prompt 2")
	h.sessionStore.Archive("sess-1")
	h.sessionStore.Archive("sess-2")

	req := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboardData(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var data DashboardData
	err := json.Unmarshal(rec.Body.Bytes(), &data)
	require.NoError(t, err)

	// Should have empty sessions list
	require.Empty(t, data.Sessions)
}

func TestHandleDashboardDataWithHelpers(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Manually add a helper with jobs to the discovery
	helperURL := "http://localhost:9001"
	d.mu.Lock()
	d.components[helperURL] = &ComponentStatus{
		URL:     helperURL,
		Type:    "helper",
		State:   "running",
		Version: "test-scheduler-v1",
		Jobs: []JobStatus{
			{
				Name:       "test-job",
				Schedule:   "0 * * * *",
				NextRun:    time.Now().Add(time.Hour),
				LastStatus: "",
			},
		},
	}
	d.mu.Unlock()

	// First request - should include helper with jobs
	req1 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec1 := httptest.NewRecorder()
	h.HandleDashboardData(rec1, req1)

	require.Equal(t, http.StatusOK, rec1.Code)

	var data1 DashboardData
	err := json.Unmarshal(rec1.Body.Bytes(), &data1)
	require.NoError(t, err)

	require.Len(t, data1.Helpers, 1)
	require.Len(t, data1.Helpers[0].Jobs, 1)
	require.Equal(t, "test-job", data1.Helpers[0].Jobs[0].Name)
	require.Empty(t, data1.Helpers[0].Jobs[0].LastStatus)

	etag1 := rec1.Header().Get("ETag")

	// Update job status (simulating job execution)
	d.mu.Lock()
	d.components[helperURL].Jobs[0].LastStatus = "submitted"
	d.components[helperURL].Jobs[0].LastTaskID = "task-123"
	d.mu.Unlock()

	// Second request - should return updated data with different ETag
	req2 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec2 := httptest.NewRecorder()
	h.HandleDashboardData(rec2, req2)

	require.Equal(t, http.StatusOK, rec2.Code)

	var data2 DashboardData
	err = json.Unmarshal(rec2.Body.Bytes(), &data2)
	require.NoError(t, err)

	require.Len(t, data2.Helpers, 1)
	require.Equal(t, "submitted", data2.Helpers[0].Jobs[0].LastStatus)
	require.Equal(t, "task-123", data2.Helpers[0].Jobs[0].LastTaskID)

	etag2 := rec2.Header().Get("ETag")
	require.NotEqual(t, etag1, etag2, "ETag should change when job status changes")
}

func TestHandleDashboardDataHelperJobStatusETagBehavior(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h := newTestHandlers(t, d, "test", nil)

	// Add a helper with a job
	helperURL := "http://localhost:9002"
	d.mu.Lock()
	d.components[helperURL] = &ComponentStatus{
		URL:     helperURL,
		Type:    "helper",
		State:   "running",
		Version: "v1",
		Jobs: []JobStatus{
			{
				Name:       "cron-job",
				Schedule:   "*/5 * * * *",
				NextRun:    time.Now().Add(5 * time.Minute),
				LastStatus: "pending",
			},
		},
	}
	d.mu.Unlock()

	// Get initial ETag
	req1 := httptest.NewRequest("GET", "/api/dashboard", nil)
	rec1 := httptest.NewRecorder()
	h.HandleDashboardData(rec1, req1)
	etag1 := rec1.Header().Get("ETag")

	// Request with same data - should return 304
	req2 := httptest.NewRequest("GET", "/api/dashboard", nil)
	req2.Header.Set("If-None-Match", etag1)
	rec2 := httptest.NewRecorder()
	h.HandleDashboardData(rec2, req2)

	require.Equal(t, http.StatusNotModified, rec2.Code, "Same data should return 304")

	// Update job status
	d.mu.Lock()
	d.components[helperURL].Jobs[0].LastStatus = "queued"
	d.mu.Unlock()

	// Request with old ETag after job status change - should return 200
	req3 := httptest.NewRequest("GET", "/api/dashboard", nil)
	req3.Header.Set("If-None-Match", etag1)
	rec3 := httptest.NewRecorder()
	h.HandleDashboardData(rec3, req3)

	require.Equal(t, http.StatusOK, rec3.Code, "Changed data should return 200 with new data")

	var data3 DashboardData
	err := json.Unmarshal(rec3.Body.Bytes(), &data3)
	require.NoError(t, err)
	require.Equal(t, "queued", data3.Helpers[0].Jobs[0].LastStatus)
}
