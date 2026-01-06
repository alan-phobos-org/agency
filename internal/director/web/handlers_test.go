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

func TestHandleStatus(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test-version")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()

	h.HandleStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	require.Equal(t, []interface{}{"director"}, resp["roles"])
	require.Equal(t, "test-version", resp["version"])
	require.Equal(t, "running", resp["state"])
	require.NotNil(t, resp["uptime_seconds"])
}

func TestHandleAgents(t *testing.T) {
	t.Parallel()

	// Setup mock agent
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"roles":   []string{"agent"},
			"version": "agent-v1",
			"state":   "idle",
		})
	}))
	defer agent.Close()

	port := extractPort(t, agent.URL)
	d := NewDiscovery(DiscoveryConfig{PortStart: port, PortEnd: port})
	d.scan()

	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	rec := httptest.NewRecorder()

	h.HandleAgents(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var agents []*ComponentStatus
	err = json.Unmarshal(rec.Body.Bytes(), &agents)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	require.Equal(t, "idle", agents[0].State)
}

func TestHandleAgentsEmpty(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/agents", nil)
	rec := httptest.NewRecorder()

	h.HandleAgents(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var agents []interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &agents)
	require.NoError(t, err)
	require.Len(t, agents, 0)
}

func TestHandleDirectors(t *testing.T) {
	t.Parallel()

	// Setup mock director
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"roles":   []string{"director"},
			"version": "dir-v1",
			"state":   "running",
		})
	}))
	defer director.Close()

	port := extractPort(t, director.URL)
	d := NewDiscovery(DiscoveryConfig{PortStart: port, PortEnd: port})
	d.scan()

	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/directors", nil)
	rec := httptest.NewRecorder()

	h.HandleDirectors(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var directors []*ComponentStatus
	err = json.Unmarshal(rec.Body.Bytes(), &directors)
	require.NoError(t, err)
	require.Len(t, directors, 1)
	require.Equal(t, "running", directors[0].State)
}

func TestHandleTaskSubmitValidation(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing agent_url",
			body:    `{"prompt": "test", "workdir": "/tmp"}`,
			wantErr: "agent_url is required",
		},
		{
			name:    "missing prompt",
			body:    `{"agent_url": "http://localhost:9000", "workdir": "/tmp"}`,
			wantErr: "prompt is required",
		},
		{
			name:    "missing workdir",
			body:    `{"agent_url": "http://localhost:9000", "prompt": "test"}`,
			wantErr: "workdir is required",
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
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	body := `{"agent_url": "http://localhost:59999", "prompt": "test", "workdir": "/tmp"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleTaskSubmit(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_not_found")
}

func TestHandleTaskSubmitAgentBusy(t *testing.T) {
	t.Parallel()

	// Setup mock agent in working state
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"roles":   []string{"agent"},
			"version": "v1",
			"state":   "working",
		})
	}))
	defer agent.Close()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	// Manually add the mock agent to discovery cache
	d.mu.Lock()
	d.components[agent.URL] = &ComponentStatus{
		URL:   agent.URL,
		Roles: []string{"agent"},
		State: "working",
	}
	d.mu.Unlock()

	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	body := `{"agent_url": "` + agent.URL + `", "prompt": "test", "workdir": "/tmp"}`
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
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"roles": []string{"agent"}, "state": "idle",
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
		Roles: []string{"agent"},
		State: "idle",
	}
	d.mu.Unlock()

	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	body := `{"agent_url": "` + agent.URL + `", "prompt": "test prompt", "workdir": "/tmp"}`
	req := httptest.NewRequest("POST", "/api/task", strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.HandleTaskSubmit(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, taskReceived, "Agent should have received task")

	var resp TaskSubmitResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "task-test-123", resp.TaskID)
	require.Equal(t, agent.URL, resp.AgentURL)
}

func TestHandleTaskStatusMissingAgentURL(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/task/task-123", nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-123")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "agent_url query parameter is required")
}

func TestHandleTaskStatusForwarding(t *testing.T) {
	t.Parallel()

	// Setup mock agent
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/task/task-123?agent_url="+agent.URL, nil)
	rec := httptest.NewRecorder()

	h.HandleTaskStatus(rec, req, "task-123")

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "completed", resp["state"])
}

func TestHandleDashboard(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	h.HandleDashboard(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Contains(t, rec.Body.String(), "Agency Dashboard")
	require.Contains(t, rec.Body.String(), "tabler") // Tabler CSS reference (lowercase in URL)
}

func TestNewHandlersTemplateLoading(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "v1.2.3")
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Equal(t, "v1.2.3", h.version)
	require.False(t, h.startTime.IsZero())
}

func TestHandleStatusUptime(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{PortStart: 50000, PortEnd: 50000})
	h, err := NewHandlers(d, "test")
	require.NoError(t, err)

	// Wait a bit to get measurable uptime
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()

	h.HandleStatus(rec, req)

	var resp map[string]interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)

	uptime := resp["uptime_seconds"].(float64)
	require.Greater(t, uptime, 0.0, "Uptime should be positive")
}
