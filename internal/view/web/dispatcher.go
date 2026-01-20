package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"phobos.org.uk/agency/internal/api"
)

// Dispatcher dispatches queued tasks to idle agents
type Dispatcher struct {
	queue        *WorkQueue
	discovery    *Discovery
	sessionStore *SessionStore
	client       *http.Client
	pollInterval time.Duration
}

// NewDispatcher creates a new dispatcher
func NewDispatcher(queue *WorkQueue, discovery *Discovery, sessionStore *SessionStore) *Dispatcher {
	return &Dispatcher{
		queue:        queue,
		discovery:    discovery,
		sessionStore: sessionStore,
		client:       createHTTPClient(queue.Config().DispatchTimeout),
		pollInterval: time.Second,
	}
}

// Start runs the dispatcher loop until the context is cancelled
func (d *Dispatcher) Start(ctx context.Context) {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.dispatchNext()
		}
	}
}

func (d *Dispatcher) dispatchNext() {
	// Get next pending task
	task := d.queue.NextPending()
	if task == nil {
		return // Queue empty
	}

	// Find first idle agent
	agent := d.findFirstIdleAgent(task.AgentKind)
	if agent == nil {
		return // No idle agents
	}

	// Mark as dispatching
	d.queue.SetState(task, TaskStateDispatching)

	// Submit to agent
	taskID, sessionID, err := d.submitToAgent(agent, task)
	if err != nil {
		d.handleDispatchError(task, err)
		return
	}

	// Success - update task with agent info
	d.queue.SetDispatched(task, agent.URL, taskID, sessionID)

	// Track in session store
	source := task.Source
	if source == "" {
		source = "queue"
	}
	opts := []AddTaskOption{WithSource(source)}
	if task.SourceJob != "" {
		opts = append(opts, WithSourceJob(task.SourceJob))
	}
	d.sessionStore.AddTask(sessionID, agent.URL, taskID, "working", task.Prompt, opts...)

	fmt.Fprintf(os.Stderr, "queue: dispatched %s to %s (task_id=%s)\n",
		task.QueueID, agent.URL, taskID)

	// Start tracking completion in background
	go d.trackCompletion(task)
}

func (d *Dispatcher) findFirstIdleAgent(agentKind string) *ComponentStatus {
	if agentKind == "" {
		agentKind = api.AgentKindClaude
	}
	agents := d.discovery.Agents()
	for _, agent := range agents {
		if agent.State == "idle" && agent.FailCount == 0 {
			if agentKind == api.AgentKindCodex {
				if agent.AgentKind != api.AgentKindCodex {
					continue
				}
			} else {
				if agent.AgentKind != "" && agent.AgentKind != api.AgentKindClaude {
					continue
				}
			}
			return agent
		}
	}
	return nil
}

func (d *Dispatcher) submitToAgent(agent *ComponentStatus, task *QueuedTask) (taskID, sessionID string, err error) {
	// Build agent request
	agentReq := buildAgentRequest(task.Prompt, task.Tier, task.TimeoutSeconds, task.SessionID, task.Env)

	body, _ := json.Marshal(agentReq)
	resp, err := d.client.Post(agent.URL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("contacting agent: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return "", "", &HTTPError{StatusCode: resp.StatusCode, Message: "agent busy"}
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("agent returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var agentResp struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(respBody, &agentResp); err != nil {
		return "", "", fmt.Errorf("parsing agent response: %w", err)
	}

	return agentResp.TaskID, agentResp.SessionID, nil
}

func (d *Dispatcher) handleDispatchError(task *QueuedTask, err error) {
	task.Attempts++
	task.LastError = err.Error()

	// Check if agent busy (409)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
		// Agent became busy between check and submit - requeue at back
		d.queue.RequeueAtBack(task)
		fmt.Fprintf(os.Stderr, "queue: requeued %s (agent busy)\n", task.QueueID)
		return
	}

	if task.Attempts >= d.queue.Config().MaxAttempts {
		// Max attempts reached - fail the task
		d.queue.SetState(task, TaskStateFailed)
		d.queue.Remove(task)
		fmt.Fprintf(os.Stderr, "queue: failed %s after %d attempts: %v\n",
			task.QueueID, task.Attempts, err)
		return
	}

	// Retryable error - back to pending
	d.queue.SetState(task, TaskStatePending)
	fmt.Fprintf(os.Stderr, "queue: retry %s (attempt %d/%d): %v\n",
		task.QueueID, task.Attempts, d.queue.Config().MaxAttempts, err)
}

// trackCompletion polls the agent for task status until it's terminal
func (d *Dispatcher) trackCompletion(task *QueuedTask) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Check if task still in queue (might have been cancelled)
		current := d.queue.Get(task.QueueID)
		if current == nil {
			return // Task removed
		}

		status, err := d.getTaskStatus(task.AgentURL, task.TaskID)
		if err != nil {
			// Agent unreachable - keep polling
			continue
		}

		if isTerminalState(status) {
			// Update session store
			if task.SessionID != "" {
				d.sessionStore.UpdateTaskState(task.SessionID, task.TaskID, status)
			}
			// Remove from queue
			d.queue.Remove(task)
			fmt.Fprintf(os.Stderr, "queue: completed %s (status=%s)\n", task.QueueID, status)
			return
		}
	}
}

func (d *Dispatcher) getTaskStatus(agentURL, taskID string) (string, error) {
	resp, err := d.client.Get(agentURL + "/task/" + taskID)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// If 404, task might be in history (completed)
	if resp.StatusCode == http.StatusNotFound {
		// Check history
		histResp, err := d.client.Get(agentURL + "/history/" + taskID)
		if err != nil {
			return "", err
		}
		defer histResp.Body.Close()

		if histResp.StatusCode == http.StatusOK {
			var data struct {
				State string `json:"state"`
			}
			json.NewDecoder(histResp.Body).Decode(&data)
			return data.State, nil
		}
		return "", fmt.Errorf("task not found")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var data struct {
		State string `json:"state"`
	}
	json.NewDecoder(resp.Body).Decode(&data)
	return data.State, nil
}

func isTerminalState(state string) bool {
	switch state {
	case TaskStateCompleted, TaskStateFailed, TaskStateCancelled:
		return true
	}
	return false
}

// HTTPError represents an HTTP error with status code
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}
