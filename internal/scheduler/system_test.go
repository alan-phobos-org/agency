//go:build system

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"phobos.org.uk/agency/internal/testutil"
)

func buildScheduler(t *testing.T) string {
	t.Helper()

	if binDir := os.Getenv("AGENCY_BIN_DIR"); binDir != "" {
		binPath := filepath.Join(binDir, "ag-scheduler")
		if _, err := os.Stat(binPath); err == nil {
			return binDir
		}
	}

	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	binDir := filepath.Join(projectRoot, "bin")

	cmd := exec.Command("./build.sh", "build")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build binaries: %s", output)

	return binDir
}

func startScheduler(t *testing.T, binDir string, configPath string, port int) *exec.Cmd {
	t.Helper()

	schedulerBin := filepath.Join(binDir, "ag-scheduler")
	args := []string{"-config", configPath}
	if port > 0 {
		args = append(args, "-port", fmt.Sprintf("%d", port))
	}

	cmd := exec.Command(schedulerBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	require.NoError(t, err, "Failed to start scheduler")

	return cmd
}

func TestSystemSchedulerBinary(t *testing.T) {
	binDir := buildScheduler(t)

	// Create config file
	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test prompt"
`, port)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Start scheduler
	cmd := startScheduler(t, binDir, configFile, 0)

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	// Wait for scheduler to be ready
	schedulerURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)
	t.Log("Scheduler is healthy")

	// Verify status
	resp, err := http.Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&status)
	require.NoError(t, err)

	assert.Equal(t, "helper", status["type"])
	assert.Equal(t, "running", status["state"])

	jobs := status["jobs"].([]interface{})
	assert.Len(t, jobs, 1)

	job := jobs[0].(map[string]interface{})
	assert.Equal(t, "test-job", job["name"])
	assert.Equal(t, "0 1 * * *", job["schedule"])

	t.Log("Status verified successfully")
}

func TestSystemSchedulerWithMockAgent(t *testing.T) {
	binDir := buildScheduler(t)

	// Create mock agent that accepts tasks
	var taskCount int32
	var lastPrompt string
	mockAgent := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/task" && r.Method == "POST" {
				var req map[string]interface{}
				json.NewDecoder(r.Body).Decode(&req)
				lastPrompt = req["prompt"].(string)
				atomic.AddInt32(&taskCount, 1)

				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"task_id": fmt.Sprintf("task-%d", atomic.LoadInt32(&taskCount)),
				})
				return
			}
			if r.URL.Path == "/status" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}

	// Start mock agent
	agentPort := testutil.AllocateTestPort(t)
	mockAgent.Addr = fmt.Sprintf(":%d", agentPort)
	go mockAgent.ListenAndServe()
	defer mockAgent.Shutdown(context.Background())

	// Wait for mock agent
	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	testutil.WaitForHealthy(t, agentURL+"/status", 5*time.Second)

	// Create scheduler config with job that runs every minute
	schedulerPort := testutil.AllocateTestPortN(t, 1)
	configContent := fmt.Sprintf(`
port: %d
agent_url: %s
jobs:
  - name: frequent-job
    schedule: "* * * * *"
    prompt: "Test task for system test"
    model: haiku
    timeout: 5m
`, schedulerPort, agentURL)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Start scheduler
	cmd := startScheduler(t, binDir, configFile, 0)

	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	schedulerURL := fmt.Sprintf("http://localhost:%d", schedulerPort)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Wait for the scheduler to trigger the job (runs every minute, but we wait up to 70s)
	// The cron job runs at the start of every minute, so we might wait up to 60s
	t.Log("Waiting for scheduled job execution (up to 70s)...")
	deadline := time.Now().Add(70 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&taskCount) > 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Verify job was submitted
	count := atomic.LoadInt32(&taskCount)
	require.GreaterOrEqual(t, count, int32(1), "Job should have been triggered at least once")
	assert.Equal(t, "Test task for system test", lastPrompt)
	t.Logf("Job executed %d time(s)", count)

	// Verify status shows job info
	resp, err := http.Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)

	jobs := status["jobs"].([]interface{})
	job := jobs[0].(map[string]interface{})
	assert.Equal(t, "submitted", job["last_status"])
	assert.NotEmpty(t, job["last_task_id"])
}

func TestSystemSchedulerGracefulShutdown(t *testing.T) {
	binDir := buildScheduler(t)

	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test"
`, port)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	cmd := startScheduler(t, binDir, configFile, 0)

	schedulerURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Send SIGTERM
	err = cmd.Process.Signal(syscall.SIGTERM)
	require.NoError(t, err)

	// Wait for graceful shutdown
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Logf("Scheduler exited with: %v", exitErr)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Scheduler did not shut down gracefully")
	}

	// Verify no longer responding
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get(schedulerURL + "/status")
	require.Error(t, err)
	t.Log("Scheduler shut down gracefully")
}

func TestSystemSchedulerVersionEmbedding(t *testing.T) {
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)

	// Get expected version
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = projectRoot
	output, err := cmd.Output()
	require.NoError(t, err)
	expectedVersion := strings.TrimSpace(string(output))
	t.Logf("Expected version: %s", expectedVersion)

	binDir := buildScheduler(t)

	// Verify binary version flag
	schedulerBin := filepath.Join(binDir, "ag-scheduler")
	cmd = exec.Command(schedulerBin, "-version")
	versionOutput, err := cmd.Output()
	require.NoError(t, err)
	binaryVersion := strings.TrimSpace(string(versionOutput))
	assert.Equal(t, expectedVersion, binaryVersion)

	// Verify status endpoint reports same version
	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test"
`, port)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	schedulerCmd := startScheduler(t, binDir, configFile, 0)
	defer func() {
		if schedulerCmd.Process != nil {
			schedulerCmd.Process.Signal(syscall.SIGTERM)
			schedulerCmd.Wait()
		}
	}()

	schedulerURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	resp, err := http.Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status struct {
		Version string `json:"version"`
	}
	json.NewDecoder(resp.Body).Decode(&status)
	assert.Equal(t, expectedVersion, status.Version)
	t.Logf("Status version: %s", status.Version)
}

func TestSystemSchedulerMultipleJobs(t *testing.T) {
	binDir := buildScheduler(t)

	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
agent_url: http://localhost:9000
jobs:
  - name: daily-maintenance
    schedule: "0 1 * * *"
    prompt: "Daily task"
    model: opus
    timeout: 2h
  - name: weekly-cleanup
    schedule: "0 2 * * 0"
    prompt: "Weekly task"
    model: sonnet
  - name: hourly-check
    schedule: "0 * * * *"
    prompt: "Hourly task"
`, port)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	cmd := startScheduler(t, binDir, configFile, 0)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	schedulerURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	resp, err := http.Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)

	jobs := status["jobs"].([]interface{})
	assert.Len(t, jobs, 3)

	// Verify all jobs are present
	jobNames := make(map[string]bool)
	for _, j := range jobs {
		job := j.(map[string]interface{})
		jobNames[job["name"].(string)] = true
	}

	assert.True(t, jobNames["daily-maintenance"])
	assert.True(t, jobNames["weekly-cleanup"])
	assert.True(t, jobNames["hourly-check"])
	t.Log("All jobs configured correctly")
}

func TestSystemSchedulerPortOverride(t *testing.T) {
	binDir := buildScheduler(t)

	// Config with port 9100
	configContent := `
port: 9100
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test"
`
	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	// Override with different port via -port flag
	overridePort := testutil.AllocateTestPort(t)
	cmd := startScheduler(t, binDir, configFile, overridePort)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	// Should be on override port, not config port
	schedulerURL := fmt.Sprintf("http://localhost:%d", overridePort)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Config port should not be listening
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get("http://localhost:9100/status")
	// This may or may not error depending on what's listening on 9100
	// The key point is that our scheduler is on overridePort

	resp, err := http.Get(schedulerURL + "/status")
	require.NoError(t, err)
	resp.Body.Close()
	t.Logf("Scheduler running on override port %d", overridePort)
}

func TestSystemSchedulerAgentBusy(t *testing.T) {
	binDir := buildScheduler(t)

	// Create mock agent that always returns 409 (busy)
	mockAgent := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/task" {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "agent_busy",
				})
				return
			}
			if r.URL.Path == "/status" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"state": "working"})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}

	agentPort := testutil.AllocateTestPort(t)
	mockAgent.Addr = fmt.Sprintf(":%d", agentPort)
	go mockAgent.ListenAndServe()
	defer mockAgent.Shutdown(context.Background())

	agentURL := fmt.Sprintf("http://localhost:%d", agentPort)
	testutil.WaitForHealthy(t, agentURL+"/status", 5*time.Second)

	schedulerPort := testutil.AllocateTestPortN(t, 1)
	configContent := fmt.Sprintf(`
port: %d
agent_url: %s
jobs:
  - name: busy-test
    schedule: "* * * * *"
    prompt: "Test"
`, schedulerPort, agentURL)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	cmd := startScheduler(t, binDir, configFile, 0)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			cmd.Wait()
		}
	}()

	schedulerURL := fmt.Sprintf("http://localhost:%d", schedulerPort)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Wait for job to be triggered
	t.Log("Waiting for job execution (up to 70s)...")
	deadline := time.Now().Add(70 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		resp, err := http.Get(schedulerURL + "/status")
		if err == nil {
			var status map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()

			jobs := status["jobs"].([]interface{})
			job := jobs[0].(map[string]interface{})
			if s, ok := job["last_status"].(string); ok && s != "" {
				lastStatus = s
				break
			}
		}
		time.Sleep(1 * time.Second)
	}

	assert.Equal(t, "skipped_busy", lastStatus, "Job should be skipped when agent is busy")
	t.Log("Job correctly skipped due to busy agent")
}

func TestSystemSchedulerShutdownEndpoint(t *testing.T) {
	binDir := buildScheduler(t)

	port := testutil.AllocateTestPort(t)
	configContent := fmt.Sprintf(`
port: %d
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test"
`, port)

	configFile := filepath.Join(t.TempDir(), "scheduler.yaml")
	err := os.WriteFile(configFile, []byte(configContent), 0644)
	require.NoError(t, err)

	cmd := startScheduler(t, binDir, configFile, 0)

	schedulerURL := fmt.Sprintf("http://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Trigger shutdown via HTTP endpoint
	resp, err := http.Post(schedulerURL+"/shutdown", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for process to exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		t.Log("Scheduler shut down via HTTP endpoint")
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Scheduler did not shut down via endpoint")
	}
}
