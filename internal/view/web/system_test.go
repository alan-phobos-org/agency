//go:build system

package web

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"phobos.org.uk/agency/internal/testutil"
)

// buildBinaries builds all binaries and returns the bin directory
func buildBinaries(t *testing.T) string {
	t.Helper()

	if binDir := os.Getenv("AGENCY_BIN_DIR"); binDir != "" {
		verifyBinaries(t, binDir)
		return binDir
	}

	projectRoot, err := filepath.Abs("../../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	cmd := exec.Command("./build.sh", "build")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binaries: %s", output)

	verifyBinaries(t, binDir)

	return binDir
}

func verifyBinaries(t *testing.T, binDir string) {
	t.Helper()

	for _, bin := range []string{"ag-agent-claude", "ag-cli", "ag-view-web"} {
		binPath := filepath.Join(binDir, bin)
		_, err := os.Stat(binPath)
		require.NoError(t, err, "Binary not found: %s", binPath)
	}
}

// startWebView starts the web view binary
func startWebView(t *testing.T, binDir string, port int, token string, agentPortStart, agentPortEnd int) *exec.Cmd {
	return startWebViewWithContexts(t, binDir, port, token, agentPortStart, agentPortEnd, "")
}

// startWebViewWithContexts starts the web view binary with optional contexts file
func startWebViewWithContexts(t *testing.T, binDir string, port int, token string, agentPortStart, agentPortEnd int, contextsPath string) *exec.Cmd {
	t.Helper()

	webBin := filepath.Join(binDir, "ag-view-web")
	args := []string{
		"-port", fmt.Sprintf("%d", port),
		"-port-start", fmt.Sprintf("%d", agentPortStart),
		"-port-end", fmt.Sprintf("%d", agentPortEnd),
	}
	if contextsPath != "" {
		args = append(args, "-contexts", contextsPath)
	}

	cmd := exec.Command(webBin, args...)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("AG_WEB_PASSWORD=%s", token),
		"AGENCY_ROOT="+t.TempDir(),
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start web view")

	return cmd
}

// startAgent starts an agent binary
func startAgent(t *testing.T, binDir string, port int) *exec.Cmd {
	t.Helper()

	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd := exec.Command(agentBin, "-port", fmt.Sprintf("%d", port))
	cmd.Env = append(os.Environ(), "AGENCY_ROOT="+t.TempDir())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start agent")

	return cmd
}

// waitForHTTPS waits for an HTTPS endpoint to be ready
func waitForHTTPS(t *testing.T, url string, timeout time.Duration) {
	t.Helper()

	client := &http.Client{
		Timeout: 1 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Timeout waiting for %s to be ready", url)
}

func TestSystemWebViewDashboardEndpoint(t *testing.T) {
	binDir := buildBinaries(t)

	// Start agent first (use index 0 for agent port)
	agentPort := testutil.AllocateTestPortN(t, 0)
	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)
	t.Log("Agent is healthy")

	// Start web view (use index 1 for web port)
	webPort := testutil.AllocateTestPortN(t, 1)
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	token := "test-system-token"
	webCmd := startWebView(t, binDir, webPort, token, agentPort, agentPort)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	// Wait for web view (uses HTTPS with self-signed cert)
	waitForHTTPS(t, webURL+"/status", 15*time.Second)
	t.Log("Web view is healthy")

	// Create HTTPS client that skips cert verification
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	t.Run("dashboard endpoint returns data", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var data DashboardData
		body, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(body, &data)
		require.NoError(t, err)

		// Should discover our agent
		require.GreaterOrEqual(t, len(data.Agents), 1, "Should discover at least 1 agent")
		require.NotNil(t, data.Directors)
		require.NotNil(t, data.Sessions)
	})

	t.Run("dashboard endpoint has ETag", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		etag := resp.Header.Get("ETag")
		require.NotEmpty(t, etag, "Response should have ETag header")
	})

	t.Run("dashboard endpoint returns 304 for matching ETag", func(t *testing.T) {
		// First request to get ETag
		req1, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req1.Header.Set("Authorization", "Bearer "+token)
		resp1, err := client.Do(req1)
		require.NoError(t, err)
		etag := resp1.Header.Get("ETag")
		resp1.Body.Close()

		// Second request with ETag
		req2, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		req2.Header.Set("If-None-Match", etag)
		resp2, err := client.Do(req2)
		require.NoError(t, err)
		defer resp2.Body.Close()

		require.Equal(t, http.StatusNotModified, resp2.StatusCode)
	})

	t.Run("dashboard requires auth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestSystemWebViewSessionOrdering(t *testing.T) {
	binDir := buildBinaries(t)

	// Start web view only (no agent needed for session testing)
	webPort := testutil.AllocateTestPortN(t, 0)
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	token := "test-session-token"

	// Use a port range with no agents
	webCmd := startWebView(t, binDir, webPort, token, 59900, 59900)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	waitForHTTPS(t, webURL+"/status", 15*time.Second)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Add sessions with different timestamps
	addSession := func(sessionID, taskID string) {
		req, _ := http.NewRequest("POST", webURL+"/api/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		body := fmt.Sprintf(`{
			"session_id": "%s",
			"agent_url": "http://agent:9000",
			"task_id": "%s",
			"state": "completed",
			"prompt": "test"
		}`, sessionID, taskID)
		req.Body = io.NopCloser(strings.NewReader(body))

		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Add sessions
	addSession("sess-old", "task-1")
	time.Sleep(50 * time.Millisecond)
	addSession("sess-new", "task-2")

	// Fetch dashboard and verify order
	req, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var data DashboardData
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &data)

	require.Len(t, data.Sessions, 2)
	require.Equal(t, "sess-new", data.Sessions[0].ID, "Newest session should be first")
	require.Equal(t, "sess-old", data.Sessions[1].ID, "Older session should be second")
}

func TestSystemWebViewContexts(t *testing.T) {
	binDir := buildBinaries(t)

	// Create a test contexts file
	tmpDir := t.TempDir()
	contextsPath := filepath.Join(tmpDir, "contexts.yaml")
	contextsContent := `contexts:
  - id: test-dev
    name: Test Development
    description: Test development workflow
    model: opus
    thinking: true
    timeout_seconds: 1800
    prompt_prefix: |
      Test prefix for development tasks.
  - id: test-quick
    name: Quick Task
    description: Fast responses
    model: haiku
    thinking: false
    timeout_seconds: 300
`
	err := os.WriteFile(contextsPath, []byte(contextsContent), 0644)
	require.NoError(t, err)

	// Start web view with contexts
	webPort := testutil.AllocateTestPortN(t, 0)
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	token := "test-contexts-token"

	webCmd := startWebViewWithContexts(t, binDir, webPort, token, 59900, 59900, contextsPath)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	waitForHTTPS(t, webURL+"/status", 15*time.Second)
	t.Log("Web view with contexts is healthy")

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	t.Run("contexts endpoint returns configured contexts", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/contexts", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var contexts []Context
		body, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(body, &contexts)
		require.NoError(t, err)

		// Should have manual + 2 configured contexts
		require.Len(t, contexts, 3)
		require.Equal(t, "manual", contexts[0].ID)
		require.Equal(t, "test-dev", contexts[1].ID)
		require.Equal(t, "test-quick", contexts[2].ID)
	})

	t.Run("contexts have correct settings", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/contexts", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		var contexts []Context
		body, _ := io.ReadAll(resp.Body)
		json.Unmarshal(body, &contexts)

		// Find test-dev context
		var devCtx *Context
		for i := range contexts {
			if contexts[i].ID == "test-dev" {
				devCtx = &contexts[i]
				break
			}
		}
		require.NotNil(t, devCtx, "test-dev context should exist")
		require.Equal(t, "Test Development", devCtx.Name)
		require.Equal(t, "Test development workflow", devCtx.Description)
		require.Equal(t, "opus", devCtx.Model)
		require.NotNil(t, devCtx.Thinking)
		require.True(t, *devCtx.Thinking)
		require.Equal(t, 1800, devCtx.TimeoutSeconds)
		require.Contains(t, devCtx.PromptPrefix, "Test prefix")

		// Find test-quick context
		var quickCtx *Context
		for i := range contexts {
			if contexts[i].ID == "test-quick" {
				quickCtx = &contexts[i]
				break
			}
		}
		require.NotNil(t, quickCtx, "test-quick context should exist")
		require.Equal(t, "Quick Task", quickCtx.Name)
		require.Equal(t, "haiku", quickCtx.Model)
		require.NotNil(t, quickCtx.Thinking)
		require.False(t, *quickCtx.Thinking)
		require.Equal(t, 300, quickCtx.TimeoutSeconds)
	})

	t.Run("contexts requires auth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/contexts", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestSystemWebViewSessionDetail(t *testing.T) {
	binDir := buildBinaries(t)

	// Start agent that provides history endpoint
	agentPort := testutil.AllocateTestPortN(t, 0)
	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)
	t.Log("Agent is healthy")

	// Start web view
	webPort := testutil.AllocateTestPortN(t, 1)
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	token := "test-session-detail-token"
	webCmd := startWebView(t, binDir, webPort, token, agentPort, agentPort)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	waitForHTTPS(t, webURL+"/status", 15*time.Second)
	t.Log("Web view is healthy")

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	t.Run("dashboard contains session detail elements", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/?token="+token, nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, _ := io.ReadAll(resp.Body)
		html := string(body)

		// Check for Alpine.js dashboard structure
		require.Contains(t, html, `x-data="dashboard()"`, "Should have Alpine.js dashboard component")
		require.Contains(t, html, "session-card", "Should have session-card class")
		require.Contains(t, html, "session-body", "Should have session-body for expansion")
		require.Contains(t, html, "expandedSession", "Should track expanded session")

		// Check for session history functionality
		require.Contains(t, html, "loadSessionHistory", "Should have loadSessionHistory function")
		require.Contains(t, html, "sessionHistory", "Should track session history")
		require.Contains(t, html, "toggleSession", "Should have toggleSession function")

		// Check for CSS classes
		require.Contains(t, html, ".session-card", "Should have session-card CSS")
		require.Contains(t, html, ".session-header", "Should have session-header CSS")
		require.Contains(t, html, ".session-body", "Should have session-body CSS")
		require.Contains(t, html, ".io-block", "Should have io-block CSS")
		require.Contains(t, html, ".io-content", "Should have io-content CSS")
	})

	t.Run("session detail with task shows in dashboard", func(t *testing.T) {
		// Add a session via API
		sessionBody := fmt.Sprintf(`{
			"session_id": "system-test-session",
			"agent_url": %q,
			"task_id": "system-test-task-1",
			"state": "completed",
			"prompt": "System test prompt"
		}`, agentURL)

		req, _ := http.NewRequest("POST", webURL+"/api/sessions", strings.NewReader(sessionBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// Verify session appears in dashboard data
		req2, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2, err := client.Do(req2)
		require.NoError(t, err)
		defer resp2.Body.Close()

		var data DashboardData
		body, _ := io.ReadAll(resp2.Body)
		err = json.Unmarshal(body, &data)
		require.NoError(t, err)

		require.GreaterOrEqual(t, len(data.Sessions), 1, "Should have at least 1 session")

		// Find our session
		var found bool
		for _, s := range data.Sessions {
			if s.ID == "system-test-session" {
				found = true
				require.Equal(t, agentURL, s.AgentURL)
				require.Len(t, s.Tasks, 1)
				require.Equal(t, "system-test-task-1", s.Tasks[0].TaskID)
				require.Equal(t, "completed", s.Tasks[0].State)
			}
		}
		require.True(t, found, "Should find the system-test-session")
	})

	t.Run("adding multiple tasks to session works", func(t *testing.T) {
		// Add second task to same session
		sessionBody := fmt.Sprintf(`{
			"session_id": "system-test-session",
			"agent_url": %q,
			"task_id": "system-test-task-2",
			"state": "working",
			"prompt": "Second system test prompt"
		}`, agentURL)

		req, _ := http.NewRequest("POST", webURL+"/api/sessions", strings.NewReader(sessionBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		// Verify session now has 2 tasks
		req2, _ := http.NewRequest("GET", webURL+"/api/dashboard", nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2, err := client.Do(req2)
		require.NoError(t, err)
		defer resp2.Body.Close()

		var data DashboardData
		body, _ := io.ReadAll(resp2.Body)
		json.Unmarshal(body, &data)

		for _, s := range data.Sessions {
			if s.ID == "system-test-session" {
				require.Len(t, s.Tasks, 2, "Session should have 2 tasks")
			}
		}
	})
}

func TestSystemWebViewNoContexts(t *testing.T) {
	binDir := buildBinaries(t)

	// Start web view without contexts
	webPort := testutil.AllocateTestPortN(t, 0)
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	token := "test-no-contexts-token"

	webCmd := startWebView(t, binDir, webPort, token, 59900, 59900)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	waitForHTTPS(t, webURL+"/status", 15*time.Second)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	t.Run("contexts endpoint returns only manual without config", func(t *testing.T) {
		req, _ := http.NewRequest("GET", webURL+"/api/contexts", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var contexts []Context
		body, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(body, &contexts)
		require.NoError(t, err)

		// Should have only manual context
		require.Len(t, contexts, 1)
		require.Equal(t, "manual", contexts[0].ID)
		require.Equal(t, "Manual", contexts[0].Name)
	})
}
