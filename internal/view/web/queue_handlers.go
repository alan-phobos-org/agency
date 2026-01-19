package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"phobos.org.uk/agency/internal/api"
)

// QueueHandlers holds HTTP handler dependencies for queue operations
type QueueHandlers struct {
	queue        *WorkQueue
	discovery    *Discovery
	sessionStore *SessionStore
}

// NewQueueHandlers creates handlers for queue operations
func NewQueueHandlers(queue *WorkQueue, discovery *Discovery, sessionStore *SessionStore) *QueueHandlers {
	return &QueueHandlers{
		queue:        queue,
		discovery:    discovery,
		sessionStore: sessionStore,
	}
}

// QueueSubmitResponse is returned after successful queue submission
type QueueSubmitResponse struct {
	QueueID  string `json:"queue_id"`
	Position int    `json:"position"`
	State    string `json:"state"`
}

// HandleQueueSubmit adds a task to the queue
func (h *QueueHandlers) HandleQueueSubmit(w http.ResponseWriter, r *http.Request) {
	var req QueueSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "prompt is required")
		return
	}
	if req.Model == "" && req.Tier != "" && !api.IsValidTier(req.Tier) {
		writeError(w, http.StatusBadRequest, "validation_error", "tier must be fast, standard, or heavy")
		return
	}
	if req.AgentKind != "" && req.AgentKind != api.AgentKindClaude && req.AgentKind != api.AgentKindCodex {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_kind must be claude or codex")
		return
	}

	task, position, err := h.queue.Add(req)
	if err == ErrQueueFull {
		writeError(w, http.StatusServiceUnavailable, "queue_full",
			fmt.Sprintf("Queue is at capacity (%d tasks)", h.queue.Config().MaxSize))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_error", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, QueueSubmitResponse{
		QueueID:  task.QueueID,
		Position: position,
		State:    task.State,
	})
}

// QueueStatusResponse represents the queue status
type QueueStatusResponse struct {
	Depth            int                 `json:"depth"`
	MaxSize          int                 `json:"max_size"`
	OldestAgeSeconds float64             `json:"oldest_age_seconds"`
	DispatchedCount  int                 `json:"dispatched_count"`
	Tasks            []QueuedTaskSummary `json:"tasks"`
}

// QueuedTaskSummary is a summary of a queued task for list responses
type QueuedTaskSummary struct {
	QueueID       string    `json:"queue_id"`
	State         string    `json:"state"`
	Position      int       `json:"position,omitempty"` // Only for pending tasks
	CreatedAt     time.Time `json:"created_at"`
	PromptPreview string    `json:"prompt_preview"`
	Source        string    `json:"source"`
	SourceJob     string    `json:"source_job,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`   // If dispatched
	AgentURL      string    `json:"agent_url,omitempty"` // If dispatched
}

// HandleQueueStatus returns the current queue status
func (h *QueueHandlers) HandleQueueStatus(w http.ResponseWriter, r *http.Request) {
	tasks := h.queue.GetAll()
	summaries := make([]QueuedTaskSummary, 0, len(tasks))

	pendingPos := 0
	for _, task := range tasks {
		if task.State == TaskStatePending {
			pendingPos++
		}

		preview := task.Prompt
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}

		summary := QueuedTaskSummary{
			QueueID:       task.QueueID,
			State:         task.State,
			CreatedAt:     task.CreatedAt,
			PromptPreview: preview,
			Source:        task.Source,
			SourceJob:     task.SourceJob,
			TaskID:        task.TaskID,
			AgentURL:      task.AgentURL,
		}
		if task.State == TaskStatePending {
			summary.Position = pendingPos
		}
		summaries = append(summaries, summary)
	}

	writeJSON(w, http.StatusOK, QueueStatusResponse{
		Depth:            h.queue.Depth(),
		MaxSize:          h.queue.Config().MaxSize,
		OldestAgeSeconds: h.queue.OldestAge(),
		DispatchedCount:  h.queue.DispatchedCount(),
		Tasks:            summaries,
	})
}

// QueuedTaskDetail is the detailed status of a queued task
type QueuedTaskDetail struct {
	QueueID      string     `json:"queue_id"`
	State        string     `json:"state"`
	Position     int        `json:"position,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
	TaskID       string     `json:"task_id,omitempty"`
	AgentURL     string     `json:"agent_url,omitempty"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error,omitempty"`
	Source       string     `json:"source"`
	SourceJob    string     `json:"source_job,omitempty"`
}

// HandleQueueTaskStatus returns the status of a specific queued task
func (h *QueueHandlers) HandleQueueTaskStatus(w http.ResponseWriter, r *http.Request, queueID string) {
	task := h.queue.Get(queueID)
	if task == nil {
		writeError(w, http.StatusNotFound, "not_found", "Queued task not found")
		return
	}

	detail := QueuedTaskDetail{
		QueueID:      task.QueueID,
		State:        task.State,
		CreatedAt:    task.CreatedAt,
		DispatchedAt: task.DispatchedAt,
		TaskID:       task.TaskID,
		AgentURL:     task.AgentURL,
		Attempts:     task.Attempts,
		LastError:    task.LastError,
		Source:       task.Source,
		SourceJob:    task.SourceJob,
	}

	if task.State == TaskStatePending {
		detail.Position = h.queue.Position(queueID)
	}

	writeJSON(w, http.StatusOK, detail)
}

// QueueCancelResponse is returned after cancelling a queued task
type QueueCancelResponse struct {
	QueueID       string `json:"queue_id"`
	State         string `json:"state"`
	WasDispatched bool   `json:"was_dispatched"`
	AgentURL      string `json:"agent_url,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
}

// HandleQueueCancel cancels a queued task
func (h *QueueHandlers) HandleQueueCancel(w http.ResponseWriter, r *http.Request, queueID string) {
	task := h.queue.Get(queueID)
	if task == nil {
		writeError(w, http.StatusNotFound, "not_found", "Queued task not found")
		return
	}

	wasDispatched := task.State == TaskStateWorking || task.State == TaskStateDispatching
	agentURL := task.AgentURL
	taskID := task.TaskID

	// If task was dispatched, try to cancel on agent
	if wasDispatched && agentURL != "" && taskID != "" {
		client := createHTTPClient(10 * time.Second)
		req, _ := http.NewRequest(http.MethodPost, agentURL+"/task/"+taskID+"/cancel", nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Remove from queue
	h.queue.Cancel(queueID)

	writeJSON(w, http.StatusOK, QueueCancelResponse{
		QueueID:       queueID,
		State:         TaskStateCancelled,
		WasDispatched: wasDispatched,
		AgentURL:      agentURL,
		TaskID:        taskID,
	})
}

// HandleTaskSubmitViaQueue routes task submission through the queue
// This replaces direct agent submission with queue-based submission
func (h *QueueHandlers) HandleTaskSubmitViaQueue(w http.ResponseWriter, r *http.Request) {
	var req TaskSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "prompt is required")
		return
	}
	if req.Model == "" && req.Tier != "" && !api.IsValidTier(req.Tier) {
		writeError(w, http.StatusBadRequest, "validation_error", "tier must be fast, standard, or heavy")
		return
	}
	if req.AgentKind != "" && req.AgentKind != api.AgentKindClaude && req.AgentKind != api.AgentKindCodex {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_kind must be claude or codex")
		return
	}

	// If agent_url is specified and agent is idle, submit directly for backward compatibility
	// Otherwise, queue the task
	if req.AgentURL != "" {
		agent, ok := h.discovery.GetComponent(req.AgentURL)
		if ok && agent.State == "idle" {
			if req.AgentKind != "" && agent.AgentKind != "" && agent.AgentKind != req.AgentKind {
				writeError(w, http.StatusBadRequest, "agent_kind_mismatch",
					fmt.Sprintf("Agent kind %q does not match requested %q", agent.AgentKind, req.AgentKind))
				return
			}
			// Direct submission to idle agent
			h.submitDirectly(w, r, req, agent)
			return
		}
	}

	// Queue the task
	source := req.Source
	if source == "" {
		source = "web"
	}

	queueReq := QueueSubmitRequest{
		Prompt:         req.Prompt,
		Model:          req.Model,
		Tier:           req.Tier,
		TimeoutSeconds: req.TimeoutSeconds,
		SessionID:      req.SessionID,
		Env:            req.Env,
		Thinking:       req.Thinking,
		Source:         source,
		SourceJob:      req.SourceJob,
		AgentKind:      req.AgentKind,
	}

	task, position, err := h.queue.Add(queueReq)
	if err == ErrQueueFull {
		writeError(w, http.StatusServiceUnavailable, "queue_full",
			"Queue is at capacity. Please try again later.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_error", err.Error())
		return
	}

	// Return queue info (202 Accepted for queued tasks)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"queue_id": task.QueueID,
		"position": position,
		"state":    "pending",
		"message":  "Task queued for execution",
	})
}

// submitDirectly handles direct submission to an idle agent (backward compatible path)
func (h *QueueHandlers) submitDirectly(w http.ResponseWriter, r *http.Request, req TaskSubmitRequest, agent *ComponentStatus) {
	// Build agent task request
	agentReq := map[string]interface{}{
		"prompt": req.Prompt,
	}
	if req.Model != "" {
		agentReq["model"] = req.Model
	}
	if req.Tier != "" {
		agentReq["tier"] = req.Tier
	}
	if req.TimeoutSeconds > 0 {
		agentReq["timeout_seconds"] = req.TimeoutSeconds
	}
	if req.SessionID != "" {
		agentReq["session_id"] = req.SessionID
	}
	if len(req.Env) > 0 {
		agentReq["env"] = req.Env
	}
	if req.Thinking != nil {
		agentReq["thinking"] = *req.Thinking
	}

	// Forward to agent
	body, _ := json.Marshal(agentReq)
	client := createHTTPClient(10 * time.Second)
	resp, err := client.Post(req.AgentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent_error", "Failed to contact agent: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		// Forward agent error
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Parse agent response
	var agentResp struct {
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(respBody, &agentResp); err != nil {
		writeError(w, http.StatusBadGateway, "parse_error", "Invalid agent response")
		return
	}

	// Track session in session store
	source := req.Source
	if source == "" {
		source = "web"
	}
	opts := []AddTaskOption{WithSource(source)}
	if req.SourceJob != "" {
		opts = append(opts, WithSourceJob(req.SourceJob))
	}
	h.sessionStore.AddTask(agentResp.SessionID, req.AgentURL, agentResp.TaskID, "working", req.Prompt, opts...)

	writeJSON(w, http.StatusCreated, TaskSubmitResponse{
		TaskID:    agentResp.TaskID,
		AgentURL:  req.AgentURL,
		SessionID: agentResp.SessionID,
	})
}
