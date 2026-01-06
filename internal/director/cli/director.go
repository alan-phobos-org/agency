package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Director is a CLI director that sends prompts to an agent
type Director struct {
	agentURL string
	client   *http.Client
}

// New creates a new CLI director
func New(agentURL string) *Director {
	return &Director{
		agentURL: agentURL,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// TaskRequest represents a task submission
type TaskRequest struct {
	Prompt         string `json:"prompt"`
	Workdir        string `json:"workdir"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// TaskResponse represents the task creation response
type TaskResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// TaskStatus represents the task status response
type TaskStatus struct {
	TaskID          string  `json:"task_id"`
	State           string  `json:"state"`
	ExitCode        *int    `json:"exit_code"`
	Output          string  `json:"output"`
	Error           *Error  `json:"error"`
	DurationSeconds float64 `json:"duration_seconds"`
}

// Error represents a task error
type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Run submits a prompt to the agent and waits for completion.
// Uses a default polling timeout of 1 hour.
func (d *Director) Run(prompt, workdir string, timeout time.Duration) (*TaskStatus, error) {
	return d.RunWithTimeout(prompt, workdir, timeout, time.Hour)
}

// RunWithTimeout submits a prompt and waits for completion with an explicit polling timeout.
// The timeout parameter is the task timeout sent to the agent.
// The pollTimeout parameter is how long to wait for the task to complete before giving up.
func (d *Director) RunWithTimeout(prompt, workdir string, timeout, pollTimeout time.Duration) (*TaskStatus, error) {
	// Submit task
	req := TaskRequest{
		Prompt:  prompt,
		Workdir: workdir,
	}
	if timeout > 0 {
		req.TimeoutSeconds = int(timeout.Seconds())
	}

	taskResp, err := d.submitTask(req)
	if err != nil {
		return nil, fmt.Errorf("submitting task: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Task submitted: %s\n", taskResp.TaskID)

	// Poll for completion with timeout
	return d.waitForCompletion(taskResp.TaskID, pollTimeout)
}

func (d *Director) submitTask(req TaskRequest) (*TaskResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := d.client.Post(d.agentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var taskResp TaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		return nil, err
	}

	return &taskResp, nil
}

func (d *Director) waitForCompletion(taskID string, timeout time.Duration) (*TaskStatus, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.After(timeout)

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("polling timeout: task %s did not complete within %v", taskID, timeout)
		case <-ticker.C:
			status, err := d.getTaskStatus(taskID)
			if err != nil {
				return nil, err
			}

			switch status.State {
			case "completed", "failed", "cancelled":
				return status, nil
			case "working", "queued":
				// Continue polling
				fmt.Fprintf(os.Stderr, ".")
			default:
				return nil, fmt.Errorf("unknown state: %s", status.State)
			}
		}
	}
}

func (d *Director) getTaskStatus(taskID string) (*TaskStatus, error) {
	resp, err := d.client.Get(d.agentURL + "/task/" + taskID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var status TaskStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return &status, nil
}

// Status gets the agent status
func (d *Director) Status() (map[string]interface{}, error) {
	resp, err := d.client.Get(d.agentURL + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return status, nil
}
