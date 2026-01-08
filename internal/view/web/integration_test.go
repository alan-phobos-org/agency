package web

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIntegrationDiscoveryAndAPI tests the full flow of discovery + API endpoints
func TestIntegrationDiscoveryAndAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create mock agent server
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":           "agent",
				"interfaces":     []string{"statusable", "taskable"},
				"version":        "mock-agent-v1",
				"state":          "idle",
				"uptime_seconds": 100,
			})
		case "/task":
			if r.Method == "POST" {
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id": "task-integration-123",
					"status":  "queued",
				})
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/task/") {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id":          "task-integration-123",
					"state":            "completed",
					"exit_code":        0,
					"output":           "Integration test completed",
					"duration_seconds": 1.5,
				})
			}
		}
	}))
	defer mockAgent.Close()

	// Create mock director server
	mockDirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":           "director",
			"interfaces":     []string{"statusable", "observable", "taskable"},
			"version":        "mock-director-v1",
			"state":          "running",
			"uptime_seconds": 50,
		})
	}))
	defer mockDirector.Close()

	agentPort := extractPort(t, mockAgent.URL)
	directorPort := extractPort(t, mockDirector.URL)

	// Create temp dir for TLS certs
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	// Create web director config
	cfg := &Config{
		Port:            0, // Will use test port
		Bind:            "127.0.0.1",
		Token:           "test-token-secret",
		PortStart:       agentPort,
		PortEnd:         directorPort,
		RefreshInterval: 100 * time.Millisecond,
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	// Ensure min/max are correct
	if cfg.PortStart > cfg.PortEnd {
		cfg.PortStart, cfg.PortEnd = cfg.PortEnd, cfg.PortStart
	}

	d, err := New(cfg, "test-integration")
	require.NoError(t, err)

	// Create test server instead of starting on TLS
	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	// Give discovery time to find components
	ctx, cancel := context.WithCancel(context.Background())
	go d.discovery.Start(ctx)
	defer cancel()
	time.Sleep(300 * time.Millisecond)

	// Create HTTP client
	client := ts.Client()

	// Test 1: Status endpoint (no auth needed for /status)
	t.Run("status endpoint", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/status")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var status map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&status)
		require.NoError(t, err)
		require.Equal(t, "view", status["type"])
		require.Equal(t, "test-integration", status["version"])
	})

	// Test 2: Auth required for API endpoints
	t.Run("auth required", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/api/agents")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test 3: Auth with bearer header
	t.Run("auth with bearer", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/agents", nil)
		req.Header.Set("Authorization", "Bearer test-token-secret")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Test 4: Auth with query param
	t.Run("auth with query param", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/api/agents?token=test-token-secret")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Test 5: Discovery finds agents
	t.Run("agents discovered", func(t *testing.T) {
		// Manually add mock agent to discovery to avoid port scanning timing issues
		d.discovery.mu.Lock()
		d.discovery.components[mockAgent.URL] = &ComponentStatus{
			URL:     mockAgent.URL,
			Type:    "agent",
			State:   "idle",
			Version: "mock-agent-v1",
		}
		d.discovery.mu.Unlock()

		resp, err := client.Get(ts.URL + "/api/agents?token=test-token-secret")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []*ComponentStatus
		err = json.NewDecoder(resp.Body).Decode(&agents)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(agents), 1, "Should discover at least 1 agent")

		// Find our mock agent
		found := false
		for _, a := range agents {
			if a.URL == mockAgent.URL {
				found = true
				require.Equal(t, "idle", a.State)
				require.Equal(t, "mock-agent-v1", a.Version)
			}
		}
		require.True(t, found, "Should find mock agent")
	})

	// Test 6: Discovery finds directors
	t.Run("directors discovered", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/api/directors?token=test-token-secret")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var directors []*ComponentStatus
		err = json.NewDecoder(resp.Body).Decode(&directors)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(directors), 1, "Should discover at least 1 director")
	})

	// Test 7: Task submission
	t.Run("task submission", func(t *testing.T) {
		// First, manually add agent to discovery (to avoid timing issues)
		d.discovery.mu.Lock()
		d.discovery.components[mockAgent.URL] = &ComponentStatus{
			URL:   mockAgent.URL,
			Type:  "agent",
			State: "idle",
		}
		d.discovery.mu.Unlock()

		body := fmt.Sprintf(`{
			"agent_url": %q,
			"prompt": "Integration test task"
		}`, mockAgent.URL)

		req, _ := http.NewRequest("POST", ts.URL+"/api/task", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token-secret")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		var taskResp TaskSubmitResponse
		err = json.NewDecoder(resp.Body).Decode(&taskResp)
		require.NoError(t, err)
		require.Equal(t, "task-integration-123", taskResp.TaskID)
		require.Equal(t, mockAgent.URL, taskResp.AgentURL)
	})

	// Test 8: Task status polling
	t.Run("task status", func(t *testing.T) {
		url := fmt.Sprintf("%s/api/task/task-integration-123?token=test-token-secret&agent_url=%s",
			ts.URL, mockAgent.URL)
		resp, err := client.Get(url)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var status map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&status)
		require.NoError(t, err)
		require.Equal(t, "completed", status["state"])
		require.Equal(t, "Integration test completed", status["output"])
	})

	// Test 9: Dashboard serves HTML
	t.Run("dashboard html", func(t *testing.T) {
		resp, err := client.Get(ts.URL + "/?token=test-token-secret")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	})
}

// TestIntegrationTLSCertGeneration tests TLS certificate generation
func TestIntegrationTLSCertGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	// Cert should not exist
	_, err := os.Stat(certPath)
	require.True(t, os.IsNotExist(err))

	// Generate cert
	err = EnsureTLSCert(TLSConfig{
		CertFile:     certPath,
		KeyFile:      keyPath,
		AutoGenerate: true,
	})
	require.NoError(t, err)

	// Cert should now exist
	_, err = os.Stat(certPath)
	require.NoError(t, err)
	_, err = os.Stat(keyPath)
	require.NoError(t, err)

	// Should be loadable
	_, err = tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)

	// Key file should have restricted permissions
	info, _ := os.Stat(keyPath)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())

	// Calling again should be no-op (not regenerate)
	originalCertInfo, _ := os.Stat(certPath)
	time.Sleep(10 * time.Millisecond)

	err = EnsureTLSCert(TLSConfig{
		CertFile:     certPath,
		KeyFile:      keyPath,
		AutoGenerate: true,
	})
	require.NoError(t, err)

	newCertInfo, _ := os.Stat(certPath)
	require.Equal(t, originalCertInfo.ModTime(), newCertInfo.ModTime(), "Cert should not be regenerated")
}

// TestIntegrationDiscoveryPolling tests that discovery polling updates state
func TestIntegrationDiscoveryPolling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create mock agent that changes state
	stateIdx := 0
	states := []string{"idle", "working", "completed", "idle"}
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := states[stateIdx%len(states)]
		stateIdx++
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":    "agent",
			"version": "v1",
			"state":   state,
		})
	}))
	defer mockAgent.Close()

	port := extractPort(t, mockAgent.URL)
	d := NewDiscovery(DiscoveryConfig{
		PortStart:       port,
		PortEnd:         port,
		RefreshInterval: 50 * time.Millisecond,
		MaxFailures:     3,
	})

	// Start discovery
	ctx, cancel := context.WithCancel(context.Background())
	go d.Start(ctx)
	defer cancel()

	// First poll - should be idle
	time.Sleep(100 * time.Millisecond)
	agents := d.Agents()
	require.Len(t, agents, 1)
	// State may have changed by now due to polling, just verify agent exists

	// Wait for more polls
	time.Sleep(200 * time.Millisecond)

	// Agent should still be discovered (hasn't failed 3 times)
	agents = d.Agents()
	require.Len(t, agents, 1)
}

// TestIntegrationRateLimiting tests that rate limiting blocks IPs after too many failed attempts
func TestIntegrationRateLimiting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59000, // Use high ports to avoid conflicts
		PortEnd:         59000,
		RefreshInterval: time.Hour, // Disable polling
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-rate-limit")
	require.NoError(t, err)

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	client := ts.Client()

	// Make requests with wrong token to trigger rate limiting
	// Set X-Real-IP to simulate consistent client IP
	testIP := "192.168.100.100"
	for i := 0; i < maxFailedAttempts; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/agents", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		req.Header.Set("X-Real-IP", testIP)
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		if i < maxFailedAttempts-1 {
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"Request %d should be unauthorized", i+1)
		}
	}

	// Next request should be rate limited (even with correct token)
	req, _ := http.NewRequest("GET", ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Real-IP", testIP)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
		"Should be rate limited after %d failed attempts", maxFailedAttempts)

	var errResp map[string]string
	json.NewDecoder(resp.Body).Decode(&errResp)
	require.Equal(t, "rate_limited", errResp["error"])
}

// TestIntegrationAccessLogging tests that access logging writes entries
func TestIntegrationAccessLogging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	accessLogPath := filepath.Join(tmpDir, "access.log")

	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59001,
		PortEnd:         59001,
		RefreshInterval: time.Hour,
		AccessLogPath:   accessLogPath,
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-access-log")
	require.NoError(t, err)

	ts := httptest.NewServer(d.Router())
	defer ts.Close()
	defer d.Shutdown(context.Background())

	client := ts.Client()

	// Make successful authenticated request
	req, _ := http.NewRequest("GET", ts.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Make failed auth request
	req2, _ := http.NewRequest("POST", ts.URL+"/api/task", strings.NewReader("{}"))
	req2.Header.Set("Authorization", "Bearer wrong-token")
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	resp2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode)

	// Shutdown to flush logs
	d.Shutdown(context.Background())

	// Read log file
	data, err := os.ReadFile(accessLogPath)
	require.NoError(t, err)

	content := string(data)
	require.Contains(t, content, "auth_ok", "Should log successful auth")
	require.Contains(t, content, "auth_fail", "Should log failed auth")
	require.Contains(t, content, "/api/agents", "Should log request path")
	require.Contains(t, content, "/api/task", "Should log request path")
}

// TestIntegrationMultiBrowserSession tests that two browsers can add tasks to the same session
func TestIntegrationMultiBrowserSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Track session IDs returned by mock agent
	var agentSessionMu sync.Mutex
	agentSessionID := ""
	taskCount := 0

	// Create mock agent that simulates real session behavior
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":       "agent",
				"interfaces": []string{"statusable", "taskable"},
				"version":    "mock-agent-v1",
				"state":      "idle",
			})
		case "/task":
			if r.Method == "POST" {
				var req map[string]interface{}
				json.NewDecoder(r.Body).Decode(&req)

				agentSessionMu.Lock()
				taskCount++
				taskID := fmt.Sprintf("task-%d", taskCount)

				// If client provides session_id, use it; otherwise generate new
				if sid, ok := req["session_id"].(string); ok && sid != "" {
					agentSessionID = sid
				} else if agentSessionID == "" {
					agentSessionID = "agent-generated-session-123"
				}
				returnSessionID := agentSessionID
				agentSessionMu.Unlock()

				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id":    taskID,
					"session_id": returnSessionID,
					"status":     "queued",
				})
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/task/") {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id": strings.TrimPrefix(r.URL.Path, "/task/"),
					"state":   "completed",
				})
			}
		}
	}))
	defer mockAgent.Close()

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59010,
		PortEnd:         59010,
		RefreshInterval: time.Hour,
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-multi-browser")
	require.NoError(t, err)

	// Manually register the mock agent
	d.discovery.mu.Lock()
	d.discovery.components[mockAgent.URL] = &ComponentStatus{
		URL:   mockAgent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.discovery.mu.Unlock()

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	// Create two independent clients (simulating two browsers)
	browserA := ts.Client()
	browserB := ts.Client()

	authRequest := func(client *http.Client, method, path string, body string) (*http.Response, error) {
		var req *http.Request
		if body != "" {
			req, _ = http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		} else {
			req, _ = http.NewRequest(method, ts.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer secret-token")
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}

	t.Run("browser A creates new session, browser B joins same session", func(t *testing.T) {
		// Browser A: Submit task to agent (creates new session)
		taskBody := fmt.Sprintf(`{
			"agent_url": %q,
			"prompt": "Browser A first task"
		}`, mockAgent.URL)
		resp, err := authRequest(browserA, "POST", "/api/task", taskBody)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		var taskResp TaskSubmitResponse
		json.NewDecoder(resp.Body).Decode(&taskResp)
		sessionID := taskResp.SessionID
		require.NotEmpty(t, sessionID, "Agent should return a session ID")

		// Browser A: Save task to web server's session store (simulating dashboard JS)
		sessionBody := fmt.Sprintf(`{
			"session_id": %q,
			"agent_url": %q,
			"task_id": %q,
			"state": "working",
			"prompt": "Browser A first task"
		}`, sessionID, mockAgent.URL, taskResp.TaskID)
		resp2, err := authRequest(browserA, "POST", "/api/sessions", sessionBody)
		require.NoError(t, err)
		resp2.Body.Close()
		require.Equal(t, http.StatusCreated, resp2.StatusCode)

		// Browser B: Fetch sessions and see Browser A's session
		resp3, err := authRequest(browserB, "GET", "/api/sessions", "")
		require.NoError(t, err)
		defer resp3.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp3.Body).Decode(&sessions)
		require.Len(t, sessions, 1, "Browser B should see 1 session")
		require.Equal(t, sessionID, sessions[0].ID)
		require.Len(t, sessions[0].Tasks, 1)

		// Browser B: Submit task to same session
		taskBody2 := fmt.Sprintf(`{
			"agent_url": %q,
			"prompt": "Browser B task",
			"session_id": %q
		}`, mockAgent.URL, sessionID)
		resp4, err := authRequest(browserB, "POST", "/api/task", taskBody2)
		require.NoError(t, err)
		defer resp4.Body.Close()
		require.Equal(t, http.StatusCreated, resp4.StatusCode)

		var taskResp2 TaskSubmitResponse
		json.NewDecoder(resp4.Body).Decode(&taskResp2)
		require.Equal(t, sessionID, taskResp2.SessionID, "Agent should return same session ID")

		// Browser B: Save task to session store
		sessionBody2 := fmt.Sprintf(`{
			"session_id": %q,
			"agent_url": %q,
			"task_id": %q,
			"state": "working",
			"prompt": "Browser B task"
		}`, sessionID, mockAgent.URL, taskResp2.TaskID)
		resp5, err := authRequest(browserB, "POST", "/api/sessions", sessionBody2)
		require.NoError(t, err)
		resp5.Body.Close()

		// Both browsers fetch sessions - should see same session with 2 tasks
		resp6, err := authRequest(browserA, "GET", "/api/sessions", "")
		require.NoError(t, err)
		defer resp6.Body.Close()

		var finalSessions []*Session
		json.NewDecoder(resp6.Body).Decode(&finalSessions)
		require.Len(t, finalSessions, 1, "Should still be 1 session")
		require.Len(t, finalSessions[0].Tasks, 2, "Session should have 2 tasks from both browsers")
	})
}

// TestIntegrationMultiBrowserSessionRace tests concurrent browser submissions without session_id
// This documents expected API behavior: when two browsers submit simultaneously
// with "New session" selected (no session_id), each gets a separate session.
// This is a fundamental race condition that can occur even with the frontend fix
// if both browsers submit before either receives a response.
func TestIntegrationMultiBrowserSessionRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	var agentMu sync.Mutex
	sessionCounter := 0

	// Mock agent that generates a new session ID for each request without session_id
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":       "agent",
				"interfaces": []string{"statusable", "taskable"},
				"version":    "mock-agent-v1",
				"state":      "idle",
			})
		case "/task":
			if r.Method == "POST" {
				var req map[string]interface{}
				json.NewDecoder(r.Body).Decode(&req)

				agentMu.Lock()
				sessionCounter++
				taskID := fmt.Sprintf("task-%d", sessionCounter)

				// Generate new session ID only if not provided
				var returnSessionID string
				if sid, ok := req["session_id"].(string); ok && sid != "" {
					returnSessionID = sid
				} else {
					returnSessionID = fmt.Sprintf("new-session-%d", sessionCounter)
				}
				agentMu.Unlock()

				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id":    taskID,
					"session_id": returnSessionID,
					"status":     "queued",
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockAgent.Close()

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59011,
		PortEnd:         59011,
		RefreshInterval: time.Hour,
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-race")
	require.NoError(t, err)

	d.discovery.mu.Lock()
	d.discovery.components[mockAgent.URL] = &ComponentStatus{
		URL:   mockAgent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.discovery.mu.Unlock()

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	client := ts.Client()

	authRequest := func(method, path string, body string) (*http.Response, error) {
		var req *http.Request
		if body != "" {
			req, _ = http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		} else {
			req, _ = http.NewRequest(method, ts.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer secret-token")
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}

	t.Run("concurrent new session creation causes multiple sessions", func(t *testing.T) {
		// Reset counter for this test
		agentMu.Lock()
		sessionCounter = 0
		agentMu.Unlock()

		// Simulate both browsers submitting "new session" tasks concurrently
		var wg sync.WaitGroup
		var sessionIDs sync.Map

		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(browserNum int) {
				defer wg.Done()

				// Submit task without session_id (new session)
				taskBody := fmt.Sprintf(`{
					"agent_url": %q,
					"prompt": "Browser %d new session task"
				}`, mockAgent.URL, browserNum)

				resp, err := authRequest("POST", "/api/task", taskBody)
				if err != nil {
					t.Logf("Browser %d error: %v", browserNum, err)
					return
				}
				defer resp.Body.Close()

				var taskResp TaskSubmitResponse
				json.NewDecoder(resp.Body).Decode(&taskResp)

				// Save to session store
				sessionBody := fmt.Sprintf(`{
					"session_id": %q,
					"agent_url": %q,
					"task_id": %q,
					"state": "working",
					"prompt": "Browser %d task"
				}`, taskResp.SessionID, mockAgent.URL, taskResp.TaskID, browserNum)

				resp2, _ := authRequest("POST", "/api/sessions", sessionBody)
				resp2.Body.Close()

				sessionIDs.Store(browserNum, taskResp.SessionID)
			}(i)
		}

		wg.Wait()

		// Check if different session IDs were generated
		var ids []string
		sessionIDs.Range(func(key, value interface{}) bool {
			ids = append(ids, value.(string))
			return true
		})

		// Expected behavior: Two different sessions were created because
		// both browsers submitted without session_id simultaneously
		resp, _ := authRequest("GET", "/api/sessions", "")
		defer resp.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp.Body).Decode(&sessions)

		t.Logf("Session IDs generated: %v", ids)
		t.Logf("Sessions in store: %d", len(sessions))
		for _, s := range sessions {
			t.Logf("  Session %s: %d tasks", s.ID, len(s.Tasks))
		}

		// When both browsers submit without session_id, each gets a separate session
		// This is expected API behavior for concurrent "new session" requests
		require.Len(t, sessions, 2, "Concurrent submissions without session_id create separate sessions")
	})
}

// TestIntegrationSessionBouncing tests multi-browser session sharing
// Verifies that when browsers properly track session IDs, all tasks
// end up in the same session regardless of timing
func TestIntegrationSessionBouncing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Scenario: Both browsers have an existing session selected, but due to
	// a dropdown refresh during polling, the selection may be lost or changed

	var agentMu sync.Mutex
	taskCounter := 0
	sessionStore := make(map[string][]string) // sessionID -> taskIDs

	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":       "agent",
				"interfaces": []string{"statusable", "taskable"},
				"version":    "mock-agent-v1",
				"state":      "idle",
			})
		case "/task":
			if r.Method == "POST" {
				var req map[string]interface{}
				json.NewDecoder(r.Body).Decode(&req)

				agentMu.Lock()
				taskCounter++
				taskID := fmt.Sprintf("task-%d", taskCounter)

				var returnSessionID string
				if sid, ok := req["session_id"].(string); ok && sid != "" {
					returnSessionID = sid
				} else {
					returnSessionID = fmt.Sprintf("auto-session-%d", taskCounter)
				}
				sessionStore[returnSessionID] = append(sessionStore[returnSessionID], taskID)
				agentMu.Unlock()

				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id":    taskID,
					"session_id": returnSessionID,
					"status":     "queued",
				})
			}
		default:
			if strings.HasPrefix(r.URL.Path, "/task/") {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"task_id": strings.TrimPrefix(r.URL.Path, "/task/"),
					"state":   "completed",
				})
			}
		}
	}))
	defer mockAgent.Close()

	tmpDir := t.TempDir()
	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59012,
		PortEnd:         59012,
		RefreshInterval: time.Hour,
		TLS: TLSConfig{
			CertFile:     filepath.Join(tmpDir, "cert.pem"),
			KeyFile:      filepath.Join(tmpDir, "key.pem"),
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-bouncing")
	require.NoError(t, err)

	d.discovery.mu.Lock()
	d.discovery.components[mockAgent.URL] = &ComponentStatus{
		URL:   mockAgent.URL,
		Type:  "agent",
		State: "idle",
	}
	d.discovery.mu.Unlock()

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	client := ts.Client()

	authRequest := func(method, path string, body string) (*http.Response, error) {
		var req *http.Request
		if body != "" {
			req, _ = http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		} else {
			req, _ = http.NewRequest(method, ts.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer secret-token")
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}

	// Helper to simulate the full browser flow
	submitTaskAndSave := func(browserName, sessionID string) (string, string, error) {
		var taskBody string
		if sessionID != "" {
			taskBody = fmt.Sprintf(`{
				"agent_url": %q,
				"prompt": "%s task",
				"session_id": %q
			}`, mockAgent.URL, browserName, sessionID)
		} else {
			taskBody = fmt.Sprintf(`{
				"agent_url": %q,
				"prompt": "%s task"
			}`, mockAgent.URL, browserName)
		}

		resp, err := authRequest("POST", "/api/task", taskBody)
		if err != nil {
			return "", "", err
		}
		defer resp.Body.Close()

		var taskResp TaskSubmitResponse
		json.NewDecoder(resp.Body).Decode(&taskResp)

		// Save to web server's session store (simulating dashboard JS)
		sessionBody := fmt.Sprintf(`{
			"session_id": %q,
			"agent_url": %q,
			"task_id": %q,
			"state": "working",
			"prompt": "%s task"
		}`, taskResp.SessionID, mockAgent.URL, taskResp.TaskID, browserName)

		resp2, _ := authRequest("POST", "/api/sessions", sessionBody)
		resp2.Body.Close()

		return taskResp.TaskID, taskResp.SessionID, nil
	}

	t.Run("sequential tasks to same session work correctly", func(t *testing.T) {
		// Browser A creates initial session
		taskID1, sessionID, err := submitTaskAndSave("BrowserA", "")
		require.NoError(t, err)
		require.NotEmpty(t, sessionID)
		t.Logf("Browser A created session %s with task %s", sessionID, taskID1)

		// Browser B adds to existing session (simulating selecting from dropdown)
		taskID2, returnedSessionID, err := submitTaskAndSave("BrowserB", sessionID)
		require.NoError(t, err)
		require.Equal(t, sessionID, returnedSessionID, "Browser B should get same session ID")
		t.Logf("Browser B added task %s to session %s", taskID2, returnedSessionID)

		// Browser A adds another task to same session
		taskID3, returnedSessionID2, err := submitTaskAndSave("BrowserA", sessionID)
		require.NoError(t, err)
		require.Equal(t, sessionID, returnedSessionID2, "Browser A should still use same session")
		t.Logf("Browser A added task %s to session %s", taskID3, returnedSessionID2)

		// Verify all tasks are in the same session
		resp, _ := authRequest("GET", "/api/sessions", "")
		defer resp.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp.Body).Decode(&sessions)

		require.Len(t, sessions, 1, "Should have exactly 1 session")
		require.Len(t, sessions[0].Tasks, 3, "Session should have all 3 tasks")
		t.Logf("Final session %s has %d tasks", sessions[0].ID, len(sessions[0].Tasks))
	})

	t.Run("browser B joins after seeing session in dropdown", func(t *testing.T) {
		// Clear previous state
		d.handlers.sessionStore = NewSessionStore()
		agentMu.Lock()
		taskCounter = 0
		agentMu.Unlock()

		// This tests the FIXED behavior:
		// 1. Browser A creates a session
		// 2. Browser B sees it in dropdown via polling
		// 3. Browser B selects the session and submits
		// 4. Both tasks end up in the same session

		// Browser A creates session
		_, sessionA, _ := submitTaskAndSave("BrowserA", "")
		t.Logf("Browser A created session: %s", sessionA)

		// Browser B loads sessions, sees sessionA
		resp, _ := authRequest("GET", "/api/sessions", "")
		var sessionsBeforeB []*Session
		json.NewDecoder(resp.Body).Decode(&sessionsBeforeB)
		resp.Body.Close()
		require.Len(t, sessionsBeforeB, 1)
		require.Equal(t, sessionA, sessionsBeforeB[0].ID)

		// Browser B selects sessionA from dropdown and submits WITH session_id
		// (This is the correct behavior after the frontend fix)
		_, sessionB, _ := submitTaskAndSave("BrowserB", sessionA) // <-- Uses session from dropdown
		t.Logf("Browser B added to session: %s", sessionB)

		// Both tasks should be in the same session
		resp2, _ := authRequest("GET", "/api/sessions", "")
		defer resp2.Body.Close()
		var finalSessions []*Session
		json.NewDecoder(resp2.Body).Decode(&finalSessions)

		t.Logf("Sessions after both browsers submitted:")
		for _, s := range finalSessions {
			t.Logf("  Session %s: %d tasks", s.ID, len(s.Tasks))
		}

		require.Len(t, finalSessions, 1, "Both browsers should use the same session")
		require.Len(t, finalSessions[0].Tasks, 2, "Session should have tasks from both browsers")
	})
}

// TestIntegrationSessionAPI tests the full session API flow
func TestIntegrationSessionAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59002,
		PortEnd:         59002,
		RefreshInterval: time.Hour,
		TLS: TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-sessions")
	require.NoError(t, err)

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	client := ts.Client()

	// Helper to make authenticated requests
	authRequest := func(method, path string, body string) (*http.Response, error) {
		var req *http.Request
		if body != "" {
			req, _ = http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		} else {
			req, _ = http.NewRequest(method, ts.URL+path, nil)
		}
		req.Header.Set("Authorization", "Bearer secret-token")
		req.Header.Set("Content-Type", "application/json")
		return client.Do(req)
	}

	// Test 1: Empty sessions list
	t.Run("empty sessions", func(t *testing.T) {
		resp, err := authRequest("GET", "/api/sessions", "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var sessions []*Session
		json.NewDecoder(resp.Body).Decode(&sessions)
		require.Empty(t, sessions)
	})

	// Test 2: Add task to session
	t.Run("add session task", func(t *testing.T) {
		body := `{
			"session_id": "sess-integration-1",
			"agent_url": "http://agent:9000",
			"task_id": "task-1",
			"state": "working",
			"prompt": "Integration test prompt"
		}`
		resp, err := authRequest("POST", "/api/sessions", body)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	})

	// Test 3: Sessions list now contains the session
	t.Run("sessions list populated", func(t *testing.T) {
		resp, err := authRequest("GET", "/api/sessions", "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var sessions []*Session
		json.NewDecoder(resp.Body).Decode(&sessions)
		require.Len(t, sessions, 1)
		require.Equal(t, "sess-integration-1", sessions[0].ID)
		require.Equal(t, "http://agent:9000", sessions[0].AgentURL)
		require.Len(t, sessions[0].Tasks, 1)
		require.Equal(t, "task-1", sessions[0].Tasks[0].TaskID)
		require.Equal(t, "working", sessions[0].Tasks[0].State)
	})

	// Test 4: Add another task to the same session
	t.Run("add second task", func(t *testing.T) {
		body := `{
			"session_id": "sess-integration-1",
			"agent_url": "http://agent:9000",
			"task_id": "task-2",
			"state": "working",
			"prompt": "Second prompt"
		}`
		resp, err := authRequest("POST", "/api/sessions", body)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// Verify session now has 2 tasks
		resp2, _ := authRequest("GET", "/api/sessions", "")
		defer resp2.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp2.Body).Decode(&sessions)
		require.Len(t, sessions, 1)
		require.Len(t, sessions[0].Tasks, 2)
	})

	// Test 5: Update task state
	t.Run("update task state", func(t *testing.T) {
		body := `{"state": "completed"}`
		resp, err := authRequest("PUT", "/api/sessions/sess-integration-1/tasks/task-1", body)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify state was updated
		resp2, _ := authRequest("GET", "/api/sessions", "")
		defer resp2.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp2.Body).Decode(&sessions)
		require.Len(t, sessions, 1)
		require.Equal(t, "completed", sessions[0].Tasks[0].State)
		require.Equal(t, "working", sessions[0].Tasks[1].State)
	})

	// Test 6: Update non-existent task returns 404
	t.Run("update non-existent task", func(t *testing.T) {
		body := `{"state": "completed"}`
		resp, err := authRequest("PUT", "/api/sessions/nonexistent/tasks/task-1", body)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	// Test 7: Add task to new session
	t.Run("add task to new session", func(t *testing.T) {
		body := `{
			"session_id": "sess-integration-2",
			"agent_url": "http://agent:9001",
			"task_id": "task-3",
			"state": "working",
			"prompt": "New session prompt"
		}`
		resp, err := authRequest("POST", "/api/sessions", body)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// Verify we now have 2 sessions
		resp2, _ := authRequest("GET", "/api/sessions", "")
		defer resp2.Body.Close()

		var sessions []*Session
		json.NewDecoder(resp2.Body).Decode(&sessions)
		require.Len(t, sessions, 2)
	})
}

// TestIntegrationConsolidatedDashboard tests the /api/dashboard endpoint
func TestIntegrationConsolidatedDashboard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create mock agent
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":           "agent",
			"interfaces":     []string{"statusable", "taskable"},
			"version":        "mock-agent-v1",
			"state":          "idle",
			"uptime_seconds": 100,
		})
	}))
	defer mockAgent.Close()

	tmpDir := t.TempDir()
	cfg := &Config{
		Port:            0,
		Bind:            "127.0.0.1",
		Token:           "secret-token",
		PortStart:       59020,
		PortEnd:         59020,
		RefreshInterval: time.Hour,
		TLS: TLSConfig{
			CertFile:     filepath.Join(tmpDir, "cert.pem"),
			KeyFile:      filepath.Join(tmpDir, "key.pem"),
			AutoGenerate: true,
		},
	}

	d, err := New(cfg, "test-dashboard")
	require.NoError(t, err)

	// Manually register mock agent
	d.discovery.mu.Lock()
	d.discovery.components[mockAgent.URL] = &ComponentStatus{
		URL:     mockAgent.URL,
		Type:    "agent",
		State:   "idle",
		Version: "mock-agent-v1",
	}
	d.discovery.mu.Unlock()

	// Add some sessions
	d.handlers.sessionStore.AddTask("sess-1", mockAgent.URL, "task-1", "completed", "prompt 1")
	d.handlers.sessionStore.AddTask("sess-2", mockAgent.URL, "task-2", "working", "prompt 2")

	ts := httptest.NewServer(d.Router())
	defer ts.Close()

	client := ts.Client()

	authRequest := func(method, path string, headers map[string]string) (*http.Response, error) {
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return client.Do(req)
	}

	t.Run("returns all data in one response", func(t *testing.T) {
		resp, err := authRequest("GET", "/api/dashboard", nil)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var data DashboardData
		json.NewDecoder(resp.Body).Decode(&data)

		require.GreaterOrEqual(t, len(data.Agents), 1, "Should have agent")
		require.NotNil(t, data.Directors, "Directors should not be nil")
		require.Len(t, data.Sessions, 2, "Should have 2 sessions")
	})

	t.Run("returns ETag header", func(t *testing.T) {
		resp, err := authRequest("GET", "/api/dashboard", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		etag := resp.Header.Get("ETag")
		require.NotEmpty(t, etag, "Should have ETag header")
		require.True(t, strings.HasPrefix(etag, `"`), "ETag should be quoted")
	})

	t.Run("returns 304 for matching ETag", func(t *testing.T) {
		// First request to get ETag
		resp1, _ := authRequest("GET", "/api/dashboard", nil)
		etag := resp1.Header.Get("ETag")
		resp1.Body.Close()

		// Second request with ETag
		resp2, err := authRequest("GET", "/api/dashboard", map[string]string{
			"If-None-Match": etag,
		})
		require.NoError(t, err)
		defer resp2.Body.Close()

		require.Equal(t, http.StatusNotModified, resp2.StatusCode)
	})

	t.Run("returns 200 when data changes", func(t *testing.T) {
		// Get initial ETag
		resp1, _ := authRequest("GET", "/api/dashboard", nil)
		etag1 := resp1.Header.Get("ETag")
		resp1.Body.Close()

		// Add new session
		d.handlers.sessionStore.AddTask("sess-3", mockAgent.URL, "task-3", "working", "new prompt")

		// Request with old ETag should get 200, not 304
		resp2, err := authRequest("GET", "/api/dashboard", map[string]string{
			"If-None-Match": etag1,
		})
		require.NoError(t, err)
		defer resp2.Body.Close()

		require.Equal(t, http.StatusOK, resp2.StatusCode, "Should return 200 when data changed")

		// New ETag should be different
		etag2 := resp2.Header.Get("ETag")
		require.NotEqual(t, etag1, etag2, "ETag should change")
	})

	t.Run("sessions are sorted by UpdatedAt", func(t *testing.T) {
		// Clear and recreate sessions with known order
		d.handlers.sessionStore = NewSessionStore()
		d.handlers.sessionStore.AddTask("old-sess", mockAgent.URL, "task-old", "completed", "old")
		time.Sleep(15 * time.Millisecond)
		d.handlers.sessionStore.AddTask("new-sess", mockAgent.URL, "task-new", "working", "new")

		resp, _ := authRequest("GET", "/api/dashboard", nil)
		defer resp.Body.Close()

		var data DashboardData
		json.NewDecoder(resp.Body).Decode(&data)

		require.Len(t, data.Sessions, 2)
		require.Equal(t, "new-sess", data.Sessions[0].ID, "Newest should be first")
		require.Equal(t, "old-sess", data.Sessions[1].ID, "Older should be second")
	})

	t.Run("auth required", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/dashboard", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}
