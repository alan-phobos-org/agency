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

	"github.com/anthropics/agency/internal/testutil"
	"github.com/stretchr/testify/require"
)

// buildBinaries builds all binaries and returns the bin directory
func buildBinaries(t *testing.T) string {
	t.Helper()

	projectRoot, err := filepath.Abs("../../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	cmd := exec.Command("./build.sh", "build")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binaries: %s", output)

	return binDir
}

// startWebView starts the web view binary
func startWebView(t *testing.T, binDir string, port int, token string, agentPortStart, agentPortEnd int) *exec.Cmd {
	t.Helper()

	webBin := filepath.Join(binDir, "ag-view-web")
	cmd := exec.Command(webBin,
		"-port", fmt.Sprintf("%d", port),
		"-port-start", fmt.Sprintf("%d", agentPortStart),
		"-port-end", fmt.Sprintf("%d", agentPortEnd),
	)
	cmd.Env = append(os.Environ(), fmt.Sprintf("AG_WEB_TOKEN=%s", token))
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
