package cli

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Director is a simple CLI director that submits tasks to an agent
type Director struct {
	directorURL string // Primary target for session tracking (optional)
	agentURL    string // Direct agent URL (fallback if director unavailable)
	client      *http.Client
}

// TaskResult represents the result of a completed task
type TaskResult struct {
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	ExitCode  *int   `json:"exit_code"`
	Output    string `json:"output"`
	SessionID string `json:"session_id"`
}

// New creates a new Director with optional director routing
func New(agentURL string, opts ...Option) *Director {
	d := &Director{
		agentURL: agentURL,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
	for _, opt := range opts {
		opt(d)
	}
	// Update client for TLS if needed
	d.client = createHTTPClient(d.directorURL, d.agentURL)
	return d
}

// Option is a functional option for Director
type Option func(*Director)

// WithDirectorURL configures routing through the web director for session tracking
func WithDirectorURL(url string) Option {
	return func(d *Director) {
		d.directorURL = url
	}
}

// createHTTPClient creates an HTTP client with TLS skip for localhost HTTPS
func createHTTPClient(directorURL, agentURL string) *http.Client {
	client := &http.Client{Timeout: 5 * time.Minute}

	// Skip TLS verification for localhost HTTPS (self-signed certs)
	needsSkipVerify := strings.HasPrefix(directorURL, "https://localhost") ||
		strings.HasPrefix(directorURL, "https://127.0.0.1") ||
		strings.HasPrefix(agentURL, "https://localhost") ||
		strings.HasPrefix(agentURL, "https://127.0.0.1")

	if needsSkipVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	return client
}

// Run submits a task and polls until completion
func (d *Director) Run(prompt string, timeout time.Duration) (*TaskResult, error) {
	// Try director first (for session tracking)
	if d.directorURL != "" {
		result, err := d.runViaDirector(prompt, timeout)
		if err == nil {
			return result, nil
		}
		// Fall back to direct agent on director failure
		fmt.Printf("Warning: director unavailable (%v), falling back to agent\n", err)
	}

	// Submit directly to agent
	return d.runViaAgent(prompt, timeout)
}

// runViaDirector submits a task through the web director for session tracking
func (d *Director) runViaDirector(prompt string, timeout time.Duration) (*TaskResult, error) {
	taskReq := map[string]interface{}{
		"agent_url":       d.agentURL,
		"prompt":          prompt,
		"timeout_seconds": int(timeout.Seconds()),
		"source":          "cli",
	}
	body, _ := json.Marshal(taskReq)

	resp, err := d.client.Post(d.directorURL+"/api/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("contacting director: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("director returned status %d", resp.StatusCode)
	}

	var taskResp struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	// Poll agent directly for status (director doesn't add value for polling)
	return d.pollTask(taskResp.TaskID, taskResp.SessionID, timeout)
}

// runViaAgent submits a task directly to the agent
func (d *Director) runViaAgent(prompt string, timeout time.Duration) (*TaskResult, error) {
	taskReq := map[string]interface{}{
		"prompt":          prompt,
		"timeout_seconds": int(timeout.Seconds()),
	}
	body, _ := json.Marshal(taskReq)

	resp, err := d.client.Post(d.agentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("submitting task: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var taskResp struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return d.pollTask(taskResp.TaskID, taskResp.SessionID, timeout)
}

// pollTask polls the agent for task completion
func (d *Director) pollTask(taskID, sessionID string, timeout time.Duration) (*TaskResult, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusResp, err := d.client.Get(d.agentURL + "/task/" + taskID)
		if err != nil {
			return nil, fmt.Errorf("polling task: %w", err)
		}

		var result TaskResult
		if err := json.NewDecoder(statusResp.Body).Decode(&result); err != nil {
			statusResp.Body.Close()
			return nil, fmt.Errorf("decoding status: %w", err)
		}
		statusResp.Body.Close()

		// Ensure session ID is preserved
		if result.SessionID == "" {
			result.SessionID = sessionID
		}

		if result.State == "completed" || result.State == "failed" || result.State == "cancelled" {
			return &result, nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return nil, fmt.Errorf("task did not complete within timeout")
}
