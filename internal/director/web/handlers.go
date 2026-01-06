package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers holds HTTP handler dependencies
type Handlers struct {
	discovery *Discovery
	version   string
	startTime time.Time
	tmpl      *template.Template
}

// NewHandlers creates handlers with dependencies
func NewHandlers(discovery *Discovery, version string) (*Handlers, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	return &Handlers{
		discovery: discovery,
		version:   version,
		startTime: time.Now(),
		tmpl:      tmpl,
	}, nil
}

// HandleDashboard serves the main dashboard HTML page
func (h *Handlers) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", nil); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleStatus returns the web director's own status (universal /status endpoint)
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"roles":          []string{"director"},
		"version":        h.version,
		"state":          "running",
		"uptime_seconds": time.Since(h.startTime).Seconds(),
		"config": map[string]interface{}{
			"type": "web",
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleAgents returns discovered agents
func (h *Handlers) HandleAgents(w http.ResponseWriter, r *http.Request) {
	agents := h.discovery.Agents()
	if agents == nil {
		agents = []*ComponentStatus{}
	}
	writeJSON(w, http.StatusOK, agents)
}

// HandleDirectors returns discovered directors
func (h *Handlers) HandleDirectors(w http.ResponseWriter, r *http.Request) {
	directors := h.discovery.Directors()
	if directors == nil {
		directors = []*ComponentStatus{}
	}
	writeJSON(w, http.StatusOK, directors)
}

// TaskSubmitRequest represents a task submission through the web director
type TaskSubmitRequest struct {
	AgentURL       string            `json:"agent_url"`
	Prompt         string            `json:"prompt"`
	Workdir        string            `json:"workdir"`
	Model          string            `json:"model,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

// TaskSubmitResponse is returned after successful task submission
type TaskSubmitResponse struct {
	TaskID   string `json:"task_id"`
	AgentURL string `json:"agent_url"`
}

// HandleTaskSubmit proxies task submission to the selected agent
func (h *Handlers) HandleTaskSubmit(w http.ResponseWriter, r *http.Request) {
	var req TaskSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.AgentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url is required")
		return
	}
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "prompt is required")
		return
	}
	if req.Workdir == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "workdir is required")
		return
	}

	// Verify agent exists and is idle
	agent, ok := h.discovery.GetComponent(req.AgentURL)
	if !ok {
		writeError(w, http.StatusBadRequest, "agent_not_found", "Agent not found: "+req.AgentURL)
		return
	}
	if agent.State != "idle" {
		writeError(w, http.StatusConflict, "agent_busy", fmt.Sprintf("Agent is %s, not idle", agent.State))
		return
	}

	// Build agent task request (different format than our input)
	agentReq := map[string]interface{}{
		"prompt":  req.Prompt,
		"workdir": req.Workdir,
	}
	if req.Model != "" {
		agentReq["model"] = req.Model
	}
	if req.TimeoutSeconds > 0 {
		agentReq["timeout_seconds"] = req.TimeoutSeconds
	}
	if len(req.Env) > 0 {
		agentReq["env"] = req.Env
	}

	// Forward to agent
	body, _ := json.Marshal(agentReq)
	client := &http.Client{Timeout: 10 * time.Second}
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
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(respBody, &agentResp); err != nil {
		writeError(w, http.StatusBadGateway, "parse_error", "Invalid agent response")
		return
	}

	writeJSON(w, http.StatusCreated, TaskSubmitResponse{
		TaskID:   agentResp.TaskID,
		AgentURL: req.AgentURL,
	})
}

// HandleTaskStatus proxies task status request to the agent
func (h *Handlers) HandleTaskStatus(w http.ResponseWriter, r *http.Request, taskID string) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}

	// Forward to agent
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(agentURL + "/task/" + taskID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent_error", "Failed to contact agent: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Forward response as-is
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}
