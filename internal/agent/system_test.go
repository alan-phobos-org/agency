//go:build system

package agent

import (
	"bytes"
	"context"
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
	for _, bin := range []string{"ag-agent-claude", "ag-director-cli"} {
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
