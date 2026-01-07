package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Director is a simple CLI director that submits tasks to an agent
type Director struct {
	agentURL string
	client   *http.Client
}

// TaskResult represents the result of a completed task
type TaskResult struct {
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	ExitCode  *int   `json:"exit_code"`
	Output    string `json:"output"`
	SessionID string `json:"session_id"`
}

// New creates a new Director
func New(agentURL string) *Director {
	return &Director{
		agentURL: agentURL,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

// Run submits a task and polls until completion
func (d *Director) Run(prompt string, timeout time.Duration) (*TaskResult, error) {
	// Submit task
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

	// Poll for completion
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusResp, err := d.client.Get(d.agentURL + "/task/" + taskResp.TaskID)
		if err != nil {
			return nil, fmt.Errorf("polling task: %w", err)
		}

		var result TaskResult
		if err := json.NewDecoder(statusResp.Body).Decode(&result); err != nil {
			statusResp.Body.Close()
			return nil, fmt.Errorf("decoding status: %w", err)
		}
		statusResp.Body.Close()

		if result.State == "completed" || result.State == "failed" || result.State == "cancelled" {
			return &result, nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return nil, fmt.Errorf("task did not complete within timeout")
}
