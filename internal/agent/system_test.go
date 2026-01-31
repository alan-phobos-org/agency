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

	"github.com/stretchr/testify/require"
	"phobos.org.uk/agency/internal/testutil"
)

// buildBinaries builds the agent and director binaries and returns the path to the bin directory
func buildBinaries(t *testing.T) string {
	t.Helper()

	if binDir := os.Getenv("AGENCY_BIN_DIR"); binDir != "" {
		verifyBinaries(t, binDir)
		return binDir
	}

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	// Build binaries
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

// startAgent starts the ag-agent-claude binary as a subprocess
func startAgent(t *testing.T, binDir string, port int) *exec.Cmd {
	t.Helper()

	// Set up temp AGENCY_ROOT with prompts
	agencyRoot := t.TempDir()
	promptsDir := filepath.Join(agencyRoot, "prompts")
	err := os.MkdirAll(promptsDir, 0755)
	require.NoError(t, err, "Failed to create prompts directory")

	// Copy test prompt file (supports both dev and prod modes)
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	testPrompt := filepath.Join(projectRoot, "testdata", "test-agency-prompt.md")
	promptData, err := os.ReadFile(testPrompt)
	require.NoError(t, err, "Failed to read test prompt")

	// Write prompt file for both dev and prod modes
	for _, mode := range []string{"dev", "prod"} {
		promptFile := filepath.Join(promptsDir, fmt.Sprintf("claude-%s.md", mode))
		err = os.WriteFile(promptFile, promptData, 0644)
		require.NoError(t, err, "Failed to write prompt file")
	}

	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd := exec.Command(agentBin, "-port", fmt.Sprintf("%d", port))
	cmd.Env = append(os.Environ(), "AGENCY_ROOT="+agencyRoot)
	cmd.Stdout = os.Stderr // Forward to test output
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	require.NoError(t, err, "Failed to start agent")

	return cmd
}

// runCLI runs the ag-cli binary with task command and returns its output
func runCLI(t *testing.T, binDir string, agentURL, prompt string, timeout time.Duration) (string, error) {
	t.Helper()

	cliBin := filepath.Join(binDir, "ag-cli")
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliBin, "task",
		"-agent", agentURL,
		"-timeout", timeout.String(),
		prompt,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

// runCLIStatus runs the ag-cli binary with status command
func runCLIStatus(t *testing.T, binDir, agentURL string) (string, error) {
	t.Helper()

	cliBin := filepath.Join(binDir, "ag-cli")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliBin, "status", agentURL)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

func TestSystemAgentDirectorBinaries(t *testing.T) {
	// Build real binaries
	binDir := buildBinaries(t)

	// Start agent process (uses real Claude CLI)
	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

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

	// Run CLI to get status
	statusOutput, err := runCLIStatus(t, binDir, agentURL)
	require.NoError(t, err)
	require.Contains(t, statusOutput, "idle")
	t.Logf("Status output: %s", statusOutput)

	// Run CLI with a task using haiku model and short prompt
	output, err := runCLI(t, binDir, agentURL, "Reply with exactly: OK", 60*time.Second)
	require.NoError(t, err, "Task failed: %s", output)

	t.Logf("Task output: %s", output)

	// Verify task completed
	require.Contains(t, output, "State: completed")
	require.Contains(t, output, "Exit code: 0")
}

func TestSystemMultipleTasks(t *testing.T) {
	binDir := buildBinaries(t)

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Run multiple sequential tasks with short prompts
	prompts := []string{"Say: one", "Say: two", "Say: three"}
	for i, prompt := range prompts {
		output, err := runCLI(t, binDir, agentURL, prompt, 60*time.Second)
		require.NoError(t, err, "Task %d failed: %s", i+1, output)
		require.Contains(t, output, "State: completed")
		t.Logf("Task %d completed successfully", i+1)
	}

	// Verify agent is still healthy
	statusOutput, err := runCLIStatus(t, binDir, agentURL)
	require.NoError(t, err)
	require.Contains(t, statusOutput, "idle")
}

func TestSystemGracefulShutdown(t *testing.T) {
	binDir := buildBinaries(t)

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Send SIGTERM for graceful shutdown
	err := agentCmd.Process.Signal(syscall.SIGTERM)
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

	// Create a temp config file with haiku model
	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
log_level: debug
claude:
  model: haiku
  timeout: 60s
`, port)

	configFile := filepath.Join(t.TempDir(), "agent.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Start agent with config file
	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd := exec.Command(agentBin, "-config", configFile)
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

	agentURL := fmt.Sprintf("https://localhost:%d", port)
	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Verify agent is running with correct config
	resp, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/status")
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

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Test submitting task via HTTP directly with haiku and short prompt
	taskReq := map[string]interface{}{
		"prompt": "Reply: hi",
		"model":  "haiku",
	}
	taskBody, _ := json.Marshal(taskReq)

	resp, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody))
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
		resp, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/task/" + taskID)
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

	// Use slow mock for this test so agent stays busy long enough
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude-concurrent")
	t.Setenv("CLAUDE_BIN", mockClaude)

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Submit first task - even with haiku this takes a few seconds
	taskReq := map[string]interface{}{
		"prompt": "Count from 1 to 20 slowly",
		"model":  "haiku",
	}
	taskBody, _ := json.Marshal(taskReq)

	resp, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Wait for agent to be in working state
	testutil.Eventually(t, 10*time.Second, func() bool {
		resp, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/status")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		return status["state"] == "working"
	})

	// Try to submit second task - should be rejected
	taskReq2 := map[string]interface{}{
		"prompt": "Say hello",
		"model":  "haiku",
	}
	taskBody2, _ := json.Marshal(taskReq2)

	resp2, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody2))
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, resp2.StatusCode, "Should reject concurrent task")

	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.True(t, strings.Contains(string(body), "busy") || strings.Contains(string(body), "already processing"))
}

// startWebView starts the ag-view-web binary as a subprocess
func startWebView(t *testing.T, binDir string, port, agentPort int, token string) *exec.Cmd {
	t.Helper()

	tmpDir := t.TempDir()

	webBin := filepath.Join(binDir, "ag-view-web")
	cmd := exec.Command(webBin,
		"-port", fmt.Sprintf("%d", port),
		"-bind", "127.0.0.1",
		"-port-start", fmt.Sprintf("%d", agentPort),
		"-port-end", fmt.Sprintf("%d", agentPort),
	)
	cmd.Env = append(os.Environ(),
		"AG_WEB_PASSWORD="+token,
		"AGENCY_ROOT="+tmpDir,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start web view")

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

	// Start agent
	agentPort := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort)

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
	webCmd := startWebView(t, binDir, webPort, agentPort, token)

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
	require.Equal(t, "view", status["type"])

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

	// Start agent
	agentPort := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", agentPort)
	agentCmd := startAgent(t, binDir, agentPort)

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
	webCmd := startWebView(t, binDir, webPort, agentPort, token)

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
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Submit task via web director with haiku and short prompt
	taskReq := map[string]interface{}{
		"agent_url": agentURL,
		"prompt":    "Reply: test",
		"model":     "haiku",
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
	var taskStatus map[string]interface{}
	for time.Now().Before(deadline) {
		statusURL := fmt.Sprintf("%s/api/task/%s?token=%s&agent_url=%s",
			webURL, taskID, token, agentURL)
		resp, err := client.Get(statusURL)
		require.NoError(t, err)

		json.NewDecoder(resp.Body).Decode(&taskStatus)
		resp.Body.Close()

		state := taskStatus["state"].(string)
		if state == "completed" || state == "failed" {
			finalState = state
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if finalState == "failed" {
		t.Logf("Task failed. Status: %+v", taskStatus)
	}
	require.Equal(t, "completed", finalState, "Task should complete successfully")
	t.Log("Task completed successfully via web director")
}

func TestSystemSessionContinuation(t *testing.T) {
	binDir := buildBinaries(t)

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Task 1: New session (no session_id provided)
	// Claude should generate and return a session_id
	t.Log("Task 1: Starting new session")
	taskReq1 := map[string]interface{}{
		"prompt": "Reply: first",
		"model":  "haiku",
	}
	taskBody1, _ := json.Marshal(taskReq1)

	resp1, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody1))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp1.StatusCode)

	var taskResp1 map[string]interface{}
	json.NewDecoder(resp1.Body).Decode(&taskResp1)
	resp1.Body.Close()

	taskID1 := taskResp1["task_id"].(string)
	require.NotEmpty(t, taskID1)

	// Wait for task 1 to complete
	waitForTaskCompletion(t, agentURL, taskID1, 60*time.Second)

	// Get the session_id from the completed task (Claude generates it)
	resp1Status, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/task/" + taskID1)
	require.NoError(t, err)
	var task1Status map[string]interface{}
	json.NewDecoder(resp1Status.Body).Decode(&task1Status)
	resp1Status.Body.Close()

	require.Equal(t, "completed", task1Status["state"], "Task 1 should complete")
	sessionID := task1Status["session_id"].(string)
	require.NotEmpty(t, sessionID, "Claude should have generated a session_id")
	t.Logf("Session created by Claude: %s", sessionID)

	// Task 2: Continue session (same session_id provided)
	t.Log("Task 2: Continuing existing session")
	taskReq2 := map[string]interface{}{
		"prompt":     "Reply: second",
		"model":      "haiku",
		"session_id": sessionID,
	}
	taskBody2, _ := json.Marshal(taskReq2)

	resp2, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody2))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp2.StatusCode)

	var taskResp2 map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&taskResp2)
	resp2.Body.Close()

	taskID2 := taskResp2["task_id"].(string)
	returnedSessionID := taskResp2["session_id"].(string)
	require.Equal(t, sessionID, returnedSessionID, "Session ID should be preserved")

	// Wait for task 2 to complete
	waitForTaskCompletion(t, agentURL, taskID2, 60*time.Second)

	// Verify task 2 completed successfully
	resp2Status, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/task/" + taskID2)
	require.NoError(t, err)
	var task2Status map[string]interface{}
	json.NewDecoder(resp2Status.Body).Decode(&task2Status)
	resp2Status.Body.Close()

	// Log full status for debugging
	task2StatusJSON, _ := json.MarshalIndent(task2Status, "", "  ")
	t.Logf("Task 2 status: %s", task2StatusJSON)

	require.Equal(t, "completed", task2Status["state"], "Task 2 should complete - session continuation failed if this errors")
	t.Log("Session continuation succeeded")
}

func TestSystemNewSessionWithoutSessionID(t *testing.T) {
	// Test that when no session_id is provided, Claude generates one
	binDir := buildBinaries(t)

	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Submit task without session_id - Claude should generate one
	taskReq := map[string]interface{}{
		"prompt": "Reply: test",
		"model":  "haiku",
	}
	taskBody, _ := json.Marshal(taskReq)

	resp, err := testutil.HTTPClient(5*time.Second).Post(agentURL+"/task", "application/json", bytes.NewReader(taskBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var taskResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&taskResp)
	resp.Body.Close()

	taskID := taskResp["task_id"].(string)
	require.NotEmpty(t, taskID)

	// Wait for task to complete
	waitForTaskCompletion(t, agentURL, taskID, 60*time.Second)

	// Get task status - session_id should be set by Claude
	resp2, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/task/" + taskID)
	require.NoError(t, err)

	var taskStatus map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&taskStatus)
	resp2.Body.Close()

	require.Equal(t, "completed", taskStatus["state"], "Task should complete")
	sessionID := taskStatus["session_id"].(string)
	require.NotEmpty(t, sessionID, "Claude should have generated a session_id")
	t.Logf("Session ID from Claude: %s", sessionID)
}

// waitForTaskCompletion polls until a task is completed or failed
func waitForTaskCompletion(t *testing.T, agentURL, taskID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/task/" + taskID)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		state := status["state"].(string)
		if state == "completed" || state == "failed" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Task %s did not complete within %v", taskID, timeout)
}

// TestSystemVersionEmbedding verifies that version is correctly embedded in binaries
// and reported by the status endpoint. This catches issues where:
// - Version isn't embedded during cross-compilation
// - Wrong architecture binaries are left in bin/ after deploy
// - Status endpoint doesn't report the embedded version
func TestSystemVersionEmbedding(t *testing.T) {
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	// Step 1: Get expected version from git describe (same as build.sh)
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = projectRoot
	gitOutput, err := cmd.Output()
	require.NoError(t, err, "git describe failed - not in a git repo?")
	expectedVersion := strings.TrimSpace(string(gitOutput))
	t.Logf("Expected version from git: %s", expectedVersion)

	// Step 2: Build binaries (this should embed the version)
	binDir := buildBinaries(t)

	// Step 3: Verify binary can run and reports correct version via -version flag
	agentBin := filepath.Join(binDir, "ag-agent-claude")
	cmd = exec.Command(agentBin, "-version")
	versionOutput, err := cmd.Output()
	require.NoError(t, err, "Binary failed to run -version (wrong architecture?)")
	binaryVersion := strings.TrimSpace(string(versionOutput))
	t.Logf("Binary -version output: %s", binaryVersion)

	require.Equal(t, expectedVersion, binaryVersion,
		"Version from -version flag doesn't match git describe. Binary may have stale version or wrong ldflags.")

	// Step 4: Start agent and verify /status reports same version
	port := testutil.AllocateTestPort(t)
	agentURL := fmt.Sprintf("https://localhost:%d", port)
	agentCmd := startAgent(t, binDir, port)

	defer func() {
		if agentCmd.Process != nil {
			agentCmd.Process.Signal(syscall.SIGTERM)
			agentCmd.Wait()
		}
	}()

	testutil.WaitForHealthy(t, agentURL+"/status", 10*time.Second)

	// Query status endpoint
	resp, err := testutil.HTTPClient(5 * time.Second).Get(agentURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status struct {
		Version string `json:"version"`
	}
	err = json.NewDecoder(resp.Body).Decode(&status)
	require.NoError(t, err)
	t.Logf("Status endpoint version: %s", status.Version)

	require.Equal(t, expectedVersion, status.Version,
		"Version from /status doesn't match git describe. Agent may be running old binary or version not passed correctly.")
}

// TestSystemBinaryArchitecture verifies binaries match host architecture.
// This catches the bug where deploy-agency.sh leaves Linux binaries in bin/
// which then causes local agency.sh or system tests to fail silently.
func TestSystemBinaryArchitecture(t *testing.T) {
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	// Check if binaries exist before building
	agentBin := filepath.Join(binDir, "ag-agent-claude")
	if _, err := os.Stat(agentBin); err == nil {
		// Binary exists - verify it can actually execute on this system
		cmd := exec.Command(agentBin, "-version")
		_, err := cmd.Output()
		if err != nil {
			t.Logf("Existing binary failed to run: %v", err)
			t.Logf("This likely means bin/ contains cross-compiled binaries (e.g., Linux binaries on macOS)")
			t.Logf("Run './build.sh build' to rebuild native binaries")

			// Now rebuild and verify the new binaries work
			t.Log("Rebuilding binaries for native architecture...")
		}
	}

	// Build native binaries
	binDir = buildBinaries(t)
	agentBin = filepath.Join(binDir, "ag-agent-claude")

	// Verify the rebuilt binary can execute
	cmd := exec.Command(agentBin, "-version")
	output, err := cmd.Output()
	require.NoError(t, err, "Rebuilt binary failed to run - build.sh may have wrong GOOS/GOARCH")
	t.Logf("Binary version: %s", strings.TrimSpace(string(output)))
}
