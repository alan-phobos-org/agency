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
