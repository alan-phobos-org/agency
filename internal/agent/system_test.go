//go:build system

package agent

import (
	"bytes"
	"context"
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

// buildBinaries builds the agent and director binaries and returns the path to the bin directory
func buildBinaries(t *testing.T) string {
	t.Helper()

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	// Build binaries
	cmd := exec.Command("./build.sh", "build")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binaries: %s", output)

	// Verify binaries exist
	for _, bin := range []string{"ag-agent-claude", "ag-director-cli", "ag-director-web"} {
		binPath := filepath.Join(binDir, bin)
		_, err := os.Stat(binPath)
		require.NoError(t, err, "Binary not found: %s", binPath)
	}

	return binDir
}

// startAgent starts the ag-agent-claude binary as a subprocess
func startAgent(t *testing.T, binDir string, port int, mockClaudePath string) *exec.Cmd {
	t.Helper()

	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd := exec.Command(agentBin, "-port", fmt.Sprintf("%d", port))
	cmd.Env = append(os.Environ(), "CLAUDE_BIN="+mockClaudePath)
	cmd.Stdout = os.Stderr // Forward to test output
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start agent")

	return cmd
}

// runDirector runs the ag-director-cli binary and returns its output
func runDirector(t *testing.T, binDir string, agentURL, prompt, workdir string, timeout time.Duration) (string, error) {
	t.Helper()

	directorBin := filepath.Join(binDir, "ag-director-cli")
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, directorBin,
		"-agent", agentURL,
		"-workdir", workdir,
		"-timeout", timeout.String(),
		prompt,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

// runDirectorStatus runs the ag-director-cli binary with -status flag
func runDirectorStatus(t *testing.T, binDir, agentURL string) (string, error) {
	t.Helper()

	directorBin := filepath.Join(binDir, "ag-director-cli")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, directorBin, "-agent", agentURL, "-status")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func TestSystemAgentDirectorBinaries(t *testing.T) {
	// Build real binaries
	binDir := buildBinaries(t)

	// Get mock claude path
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	_, err = os.Stat(mockClaude)
	require.NoError(t, err, "mock-claude not found")

	// Start agent process
	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port, mockClaude)

	// Cleanup: kill agent on test completion
	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	// Wait for agent to be ready
	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)
	t.Log("Agent is healthy")

	// Run director to get status
	statusOutput, err := runDirectorStatus(t, binDir, agentURL)
	require.NoError(t, err)
	require.Contains(t, statusOutput, "idle")
	t.Logf("Status output: %s", statusOutput)

	// Run director with a task
	workdir := t.TempDir()
	output, err := runDirector(t, binDir, agentURL, "Hello, test task", workdir, 30*time.Second)
	require.NoError(t, err, "Director failed: %s", output)

	t.Logf("Director output: %s", output)

	// Verify task completed
	require.Contains(t, output, "State: completed")
	require.Contains(t, output, "Exit code: 0")
	require.Contains(t, output, "Task completed successfully")
}

func TestSystemMultipleTasks(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port, mockClaude)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Run multiple sequential tasks
	for i := 0; i < 3; i++ {
		workdir := t.TempDir()
		output, err := runDirector(t, binDir, agentURL, fmt.Sprintf("Task %d", i+1), workdir, 30*time.Second)
		require.NoError(t, err, "Task %d failed: %s", i+1, output)
		require.Contains(t, output, "State: completed")
		t.Logf("Task %d completed successfully", i+1)
	}

	// Verify agent is still healthy
	statusOutput, err := runDirectorStatus(t, binDir, agentURL)
	require.NoError(t, err)
	require.Contains(t, statusOutput, "idle")
}

func TestSystemGracefulShutdown(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port, mockClaude)

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Send SIGTERM for graceful shutdown
	err = agentCmd.Process.Signal(syscall.SIGTERM)
	require.NoError(t, err)

	// Wait for process to exit (should be graceful)
	done := make(chan error, 1)
	go func() {
		done <- agentCmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited - check it wasn't killed
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code 0 is success, or signal termination is acceptable
			t.Logf("Agent exited with: %v", exitErr)
		}
	case <-time.After(5 * time.Second):
		agentCmd.Process.Kill()
		t.Fatal("Agent did not shut down gracefully within 5 seconds")
	}

	// Verify agent is no longer responding
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get(agentURL + "/status")
	require.Error(t, err, "Agent should not be responding after shutdown")
}

func TestSystemConfigFile(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	// Create a temp config file
	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
log_level: debug
claude:
  model: sonnet
  timeout: 30s
`, port)

	configFile := filepath.Join(t.TempDir(), "agent.yaml")
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Start agent with config file
	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd := exec.Command(agentBin, "-config", configFile)
	cmd.Env = append(os.Environ(), "CLAUDE_BIN="+mockClaude)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err)

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	agentURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Verify agent is running with correct config
	resp, err := http.Get(agentURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var status map[string]interface{}
	err = json.Unmarshal(body, &status)
	require.NoError(t, err)

	require.Equal(t, "idle", status["state"])
}

func TestSystemHTTPAPIDirectly(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port, mockClaude)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Test submitting task via HTTP directly (not through director binary)
	workdir := t.TempDir()
	taskReq := map[string]interface{}{
		"prompt":  "Direct HTTP task",
		"workdir": workdir,
	}
	taskBody, _ := json.Marshal(taskReq)

	resp, err := http.Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var taskResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&taskResp)
	resp.Body.Close()
	require.NoError(t, err)

	taskID := taskResp["task_id"].(string)
	require.NotEmpty(t, taskID)

	// Poll for completion
	var finalState string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(agentURL + "/task/" + taskID)
		require.NoError(t, err)

		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		state := status["state"].(string)
		if state == "completed" || state == "failed" {
			finalState = state
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	require.Equal(t, "completed", finalState)
}

func TestSystemConcurrentTaskRejection(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaudeSlow := filepath.Join(projectRoot, "testdata", "mock-claude-slow")

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port, mockClaudeSlow)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Submit first task (slow)
	workdir := t.TempDir()
	taskReq := map[string]interface{}{
		"prompt":  "Slow task",
		"workdir": workdir,
	}
	taskBody, _ := json.Marshal(taskReq)

	resp, err := http.Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Wait for agent to be in working state
	testutil.Eventually(t, 5*time.Second, func() bool {
		resp, err := http.Get(agentURL + "/status")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		return status["state"] == "working"
	})

	// Try to submit second task - should be rejected
	workdir2 := t.TempDir()
	taskReq2 := map[string]interface{}{
		"prompt":  "Second task",
		"workdir": workdir2,
	}
	taskBody2, _ := json.Marshal(taskReq2)

	resp2, err := http.Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody2))
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, resp2.StatusCode, "Should reject concurrent task")

	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.True(t, strings.Contains(string(body), "busy") || strings.Contains(string(body), "already processing"))
}

// startWebDirector starts the ag-director-web binary as a subprocess
func startWebDirector(t *testing.T, binDir string, port, agentPort int, token string) *exec.Cmd {
	t.Helper()

	tmpDir := t.TempDir()

	webBin := filepath.Join(binDir, "ag-director-web")
	cmd := exec.Command(webBin,
		"-port", fmt.Sprintf("%d", port),
		"-bind", "127.0.0.1",
		"-port-start", fmt.Sprintf("%d", agentPort),
		"-port-end", fmt.Sprintf("%d", agentPort),
	)
	cmd.Env = append(os.Environ(),
		"AG_WEB_TOKEN="+token,
		"AGENCY_ROOT="+tmpDir,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start web director")

	return cmd
}

// waitForHTTPS waits for an HTTPS endpoint to become healthy
func waitForHTTPS(t *testing.T, url string, timeout time.Duration) {
	t.Helper()

	// Create client that skips TLS verification (for self-signed certs)
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("Service at %s did not become healthy within %v", url, timeout)
}

func TestSystemWebDirectorDiscovery(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	// Start agent
	agentPort := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort, mockClaude)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)
	t.Log("Agent is healthy")

	// Start web director
	webPort := testutil.AllocateTestPort(t) + 1000 // Offset to avoid collision
	token := "test-system-token"
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	webCmd := startWebDirector(t, binDir, webPort, agentPort, token)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	// Wait for web director to be ready
	waitForHTTPS(t, webURL+"/status", 15*time.Second)
	t.Log("Web director is healthy")

	// Create HTTPS client
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Test 1: Status endpoint (no auth required)
	t.Log("Testing status endpoint...")
	resp, err := client.Get(webURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)
	require.Equal(t, []interface{}{"director"}, status["roles"])

	// Test 2: API requires auth
	t.Log("Testing auth required...")
	resp2, err := client.Get(webURL + "/api/agents")
	require.NoError(t, err)
	resp2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode)

	// Test 3: Auth with token works
	t.Log("Testing auth with token...")
	resp3, err := client.Get(webURL + "/api/agents?token=" + token)
	require.NoError(t, err)
	defer resp3.Body.Close()
	require.Equal(t, http.StatusOK, resp3.StatusCode)

	// Wait for discovery to find the agent
	t.Log("Waiting for discovery to find agent...")
	time.Sleep(2 * time.Second)

	// Test 4: Discovery finds the agent
	resp4, err := client.Get(webURL + "/api/agents?token=" + token)
	require.NoError(t, err)
	defer resp4.Body.Close()

	var agents []map[string]interface{}
	json.NewDecoder(resp4.Body).Decode(&agents)
	require.GreaterOrEqual(t, len(agents), 1, "Should discover agent")

	// Find our agent
	found := false
	for _, agent := range agents {
		if agent["url"] == agentURL {
			found = true
			require.Equal(t, "idle", agent["state"])
			break
		}
	}
	require.True(t, found, "Should find our specific agent")
	t.Log("Agent discovered successfully")

	// Test 5: Dashboard serves HTML
	t.Log("Testing dashboard...")
	resp5, err := client.Get(webURL + "/?token=" + token)
	require.NoError(t, err)
	defer resp5.Body.Close()
	require.Equal(t, http.StatusOK, resp5.StatusCode)
	require.Contains(t, resp5.Header.Get("Content-Type"), "text/html")
}

func TestSystemWebDirectorTaskSubmission(t *testing.T) {
	binDir := buildBinaries(t)

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	// Start agent
	agentPort := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort, mockClaude)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Start web director
	webPort := testutil.AllocateTestPort(t) + 2000 // Offset to avoid collision
	token := "test-task-token"
	webURL := fmt.Sprintf("https://localhost:%d", webPort)
	webCmd := startWebDirector(t, binDir, webPort, agentPort, token)

	defer func() {
		if webCmd.Process != nil {
			webCmd.Process.Signal(syscall.SIGTERM)
			webCmd.Wait()
		}
	}()

	waitForHTTPS(t, webURL+"/status", 15*time.Second)

	// Wait for discovery
	time.Sleep(2 * time.Second)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Submit task via web director
	workdir := t.TempDir()
	taskReq := map[string]interface{}{
		"agent_url": agentURL,
		"prompt":    "System test task via web director",
		"workdir":   workdir,
	}
	taskBody, _ := json.Marshal(taskReq)

	req, _ := http.NewRequest("POST", webURL+"/api/task?token="+token, bytes.NewReader(taskBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var taskResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&taskResp)
	taskID := taskResp["task_id"].(string)
	require.NotEmpty(t, taskID)
	t.Logf("Task submitted: %s", taskID)

	// Poll for completion via web director
	deadline := time.Now().Add(30 * time.Second)
	var finalState string
	for time.Now().Before(deadline) {
		statusURL := fmt.Sprintf("%s/api/task/%s?token=%s&agent_url=%s",
			webURL, taskID, token, agentURL)
		resp, err := client.Get(statusURL)
		require.NoError(t, err)

		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		state := status["state"].(string)
		if state == "completed" || state == "failed" {
			finalState = state
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	require.Equal(t, "completed", finalState, "Task should complete successfully")
	t.Log("Task completed successfully via web director")
}
