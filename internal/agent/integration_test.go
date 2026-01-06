//go:build integration

package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/agency/internal/config"
	"github.com/anthropics/agency/internal/director/cli"
	"github.com/anthropics/agency/internal/testutil"
	"github.com/gavv/httpexpect/v2"
	"github.com/stretchr/testify/require"
)

func TestIntegrationAgentDirectorFlow(t *testing.T) {
	// Get project root for mock-claude
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude")

	// Ensure mock-claude exists
	_, err = os.Stat(mockClaude)
	require.NoError(t, err, "mock-claude not found at %s", mockClaude)

	// Set mock Claude
	t.Setenv("CLAUDE_BIN", mockClaude)

	// Start agent
	port := testutil.AllocateTestPort(t)
	cfg := &config.Config{
		Port:     port,
		LogLevel: "debug",
		Claude: config.ClaudeConfig{
			Model:   "sonnet",
			Timeout: 30 * time.Second,
		},
	}

	agent := New(cfg, "test-version")
	agentURL := fmt.Sprintf("http://localhost:%d", port)

	// Start agent in background
	go func() {
		agent.Start()
	}()

	// Wait for agent to be ready
	testutil.WaitForHealthy(t, agentURL+"/status", 5*time.Second)

	// Cleanup
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Shutdown(ctx)
	}()

	// Test using httpexpect
	e := httpexpect.Default(t, agentURL)

	// Check status
	e.GET("/status").
		Expect().
		Status(http.StatusOK).
		JSON().Object().
		HasValue("state", "idle").
		HasValue("version", "test-version").
		ContainsKey("uptime_seconds")

	// Submit task via director
	workdir := t.TempDir()
	director := cli.New(agentURL)

	result, err := director.Run("Hello, please respond", workdir, 30*time.Second)
	require.NoError(t, err)

	require.Equal(t, "completed", result.State)
	require.NotNil(t, result.ExitCode)
	require.Equal(t, 0, *result.ExitCode)
	require.Contains(t, result.Output, "Task completed successfully")

	// Check task history
	e.GET("/task/"+result.TaskID).
		Expect().
		Status(http.StatusOK).
		JSON().Object().
		HasValue("state", "completed").
		HasValue("exit_code", 0)
}

func TestIntegrationTaskCancellation(t *testing.T) {
	// Get project root for mock-claude-slow
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude-slow")

	// Ensure mock exists
	_, err = os.Stat(mockClaude)
	require.NoError(t, err, "mock-claude-slow not found at %s", mockClaude)

	t.Setenv("CLAUDE_BIN", mockClaude)

	port := testutil.AllocateTestPort(t)
	cfg := &config.Config{
		Port:     port,
		LogLevel: "debug",
		Claude: config.ClaudeConfig{
			Model:   "sonnet",
			Timeout: 60 * time.Second,
		},
	}

	agent := New(cfg, "test")
	agentURL := fmt.Sprintf("http://localhost:%d", port)

	go agent.Start()
	testutil.WaitForHealthy(t, agentURL+"/status", 5*time.Second)

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Shutdown(ctx)
	}()

	e := httpexpect.Default(t, agentURL)
	workdir := t.TempDir()

	// Submit long-running task
	resp := e.POST("/task").
		WithJSON(map[string]interface{}{
			"prompt":  "slow task",
			"workdir": workdir,
		}).
		Expect().
		Status(http.StatusCreated).
		JSON().Object()

	taskID := resp.Value("task_id").String().Raw()

	// Wait for task to be in working state
	testutil.Eventually(t, 2*time.Second, func() bool {
		statusResp := e.GET("/status").Expect().Status(http.StatusOK).JSON().Object()
		state := statusResp.Value("state").String().Raw()
		return state == "working"
	})

	// Cancel the task
	e.POST("/task/{id}/cancel", taskID).
		Expect().
		Status(http.StatusOK).
		JSON().Object().
		HasValue("state", "cancelled")

	// Verify agent returns to idle
	testutil.Eventually(t, 5*time.Second, func() bool {
		statusResp := e.GET("/status").Expect().Status(http.StatusOK).JSON().Object()
		state := statusResp.Value("state").String().Raw()
		return state == "idle"
	})
}

func TestIntegrationPromptWithDashes(t *testing.T) {
	// Test that prompts starting with dashes are handled correctly
	// and not interpreted as CLI flags
	projectRoot, err := filepath.Abs("../../")
	require.NoError(t, err)
	mockClaude := filepath.Join(projectRoot, "testdata", "mock-claude-args")

	_, err = os.Stat(mockClaude)
	require.NoError(t, err, "mock-claude-args not found at %s", mockClaude)

	t.Setenv("CLAUDE_BIN", mockClaude)

	port := testutil.AllocateTestPort(t)
	cfg := &config.Config{
		Port:     port,
		LogLevel: "debug",
		Claude: config.ClaudeConfig{
			Model:   "sonnet",
			Timeout: 30 * time.Second,
		},
	}

	agent := New(cfg, "test")
	agentURL := fmt.Sprintf("http://localhost:%d", port)

	go agent.Start()
	testutil.WaitForHealthy(t, agentURL+"/status", 5*time.Second)

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Shutdown(ctx)
	}()

	workdir := t.TempDir()

	// Create a file to capture the prompt
	promptFile := filepath.Join(workdir, "captured_prompt.txt")
	t.Setenv("MOCK_CLAUDE_OUTPUT", promptFile)

	// Test prompts that could be misinterpreted as flags
	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "prompt with leading dash",
			prompt: "- clone https://github.com/example/repo",
		},
		{
			name:   "prompt with bullet points",
			prompt: "- clone repo\n- remove file\n- commit and push",
		},
		{
			name:   "prompt starting with double dash",
			prompt: "--help me understand this code",
		},
	}

	director := cli.New(agentURL)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := director.Run(tt.prompt, workdir, 30*time.Second)
			require.NoError(t, err)
			require.Equal(t, "completed", result.State)

			// Verify the prompt was received correctly by the mock
			captured, err := os.ReadFile(promptFile)
			require.NoError(t, err)
			require.Equal(t, tt.prompt, strings.TrimSpace(string(captured)))
		})
	}
}
