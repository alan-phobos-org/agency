package web

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"time"

	"phobos.org.uk/agency/internal/api"
)

var (
	writeJSON  = api.WriteJSON
	writeError = api.WriteError
)

//go:embed templates/*.html
var assetsFS embed.FS

// Handlers holds HTTP handler dependencies
type Handlers struct {
	discovery    *Discovery
	version      string
	startTime    time.Time
	tmpl         *template.Template
	sessionStore *SessionStore
	contexts     *ContextsConfig
	authStore    *AuthStore
	secureCookie bool       // Whether to set Secure flag on cookies (HTTPS)
	shutdownFunc func()     // Callback to trigger graceful shutdown
	queue        *WorkQueue // Work queue for status reporting
}

// NewHandlers creates handlers with dependencies
func NewHandlers(discovery *Discovery, version string, contexts *ContextsConfig, authStore *AuthStore, secureCookie bool) (*Handlers, error) {
	tmpl, err := template.ParseFS(assetsFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	return &Handlers{
		discovery:    discovery,
		version:      version,
		startTime:    time.Now(),
		tmpl:         tmpl,
		sessionStore: NewSessionStore(),
		contexts:     contexts,
		authStore:    authStore,
		secureCookie: secureCookie,
	}, nil
}

// SetShutdownFunc sets the callback for graceful shutdown
func (h *Handlers) SetShutdownFunc(fn func()) {
	h.shutdownFunc = fn
}

// SetQueue sets the work queue for status reporting
func (h *Handlers) SetQueue(q *WorkQueue) {
	h.queue = q
}

// createHTTPClient creates an HTTP client that accepts self-signed certificates for localhost
func createHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Accept self-signed certificates for localhost
			},
		},
	}
}

// HandleDashboard serves the main dashboard HTML page
func (h *Handlers) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]interface{}{
		"Version": h.version,
	}
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleStatus returns the web view's own status (universal /status endpoint)
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"type":           api.TypeView,
		"interfaces":     []string{api.InterfaceStatusable, api.InterfaceObservable, api.InterfaceTaskable},
		"version":        h.version,
		"state":          "running",
		"uptime_seconds": time.Since(h.startTime).Seconds(),
		"config": map[string]interface{}{
			"type": "web",
		},
	}
	// Add queue status if available
	if h.queue != nil {
		resp["queue"] = map[string]interface{}{
			"depth":              h.queue.Depth(),
			"max_size":           h.queue.Config().MaxSize,
			"oldest_age_seconds": h.queue.OldestAge(),
			"dispatched_count":   h.queue.DispatchedCount(),
		}
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

// TaskSubmitRequest represents a task submission through the web view
type TaskSubmitRequest struct {
	AgentURL       string            `json:"agent_url"`
	AgentKind      string            `json:"agent_kind,omitempty"`
	Prompt         string            `json:"prompt"`
	Model          string            `json:"model,omitempty"`
	Tier           string            `json:"tier,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	SessionID      string            `json:"session_id,omitempty"` // Continue existing session
	Env            map[string]string `json:"env,omitempty"`
	Thinking       *bool             `json:"thinking,omitempty"`   // Enable extended thinking (default: true)
	Source         string            `json:"source,omitempty"`     // "web", "scheduler", "cli" (default: "web")
	SourceJob      string            `json:"source_job,omitempty"` // Job name for scheduler
}

// TaskSubmitResponse is returned after successful task submission
type TaskSubmitResponse struct {
	TaskID    string `json:"task_id"`
	AgentURL  string `json:"agent_url"`
	SessionID string `json:"session_id,omitempty"` // Session ID from agent
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
	if req.Model == "" && req.Tier != "" && !api.IsValidTier(req.Tier) {
		writeError(w, http.StatusBadRequest, "validation_error", "tier must be fast, standard, or heavy")
		return
	}
	if req.AgentKind != "" && req.AgentKind != api.AgentKindClaude && req.AgentKind != api.AgentKindCodex {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_kind must be claude or codex")
		return
	}

	// Verify agent exists and is idle
	agent, ok := h.discovery.GetComponent(req.AgentURL)
	if !ok {
		writeError(w, http.StatusBadRequest, "agent_not_found", "Agent not found: "+req.AgentURL)
		return
	}
	if req.AgentKind != "" && agent.AgentKind != "" && agent.AgentKind != req.AgentKind {
		writeError(w, http.StatusBadRequest, "agent_kind_mismatch",
			fmt.Sprintf("Agent kind %q does not match requested %q", agent.AgentKind, req.AgentKind))
		return
	}
	if agent.State != "idle" {
		writeError(w, http.StatusConflict, api.ErrorAgentBusy, fmt.Sprintf("Agent is %s, not idle", agent.State))
		return
	}

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
		source = "web" // Default source is web UI
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

// HandleTaskStatus proxies task status request to the agent.
// If the agent returns 404 (task completed and moved to history),
// falls back to checking /history/:id to get the terminal state.
// If session_id is provided and a terminal state is found in history,
// automatically updates the session store to fix race condition where
// client closes before updating state.
func (h *Handlers) HandleTaskStatus(w http.ResponseWriter, r *http.Request, taskID string) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}
	sessionID := r.URL.Query().Get("session_id") // Optional: for auto-updating session state

	client := createHTTPClient(5 * time.Second)

	// Try the active task endpoint first
	resp, err := client.Get(agentURL + "/task/" + taskID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "agent_error", "Failed to contact agent: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// If task not found, check history for terminal state
	if resp.StatusCode == http.StatusNotFound {
		historyResp, err := client.Get(agentURL + "/history/" + taskID)
		if err != nil {
			// History check failed, return original 404
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   api.ErrorNotFound,
				"message": "Task not found",
			})
			return
		}
		defer historyResp.Body.Close()

		if historyResp.StatusCode == http.StatusOK {
			// Read history response to parse state and return
			body, err := io.ReadAll(historyResp.Body)
			if err != nil {
				writeError(w, http.StatusInternalServerError, api.ErrorReadError, "Failed to read history response")
				return
			}

			// Auto-update session store if session_id provided
			if sessionID != "" {
				var historyData struct {
					State string `json:"state"`
				}
				if json.Unmarshal(body, &historyData) == nil && historyData.State != "" {
					// Update session store with terminal state from history
					h.sessionStore.UpdateTaskState(sessionID, taskID, historyData.State)
				}
			}

			// Task found in history - return its state
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(body)
			return
		}

		// Task not in history either, return 404
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   api.ErrorNotFound,
			"message": "Task not found",
		})
		return
	}

	// Forward response as-is
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode == http.StatusOK && sessionID != "" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, api.ErrorReadError, "Failed to read task response")
			return
		}
		var taskData struct {
			State string `json:"state"`
		}
		if json.Unmarshal(body, &taskData) == nil && taskData.State != "" {
			switch taskData.State {
			case "completed", "failed", "cancelled":
				h.sessionStore.UpdateTaskState(sessionID, taskID, taskData.State)
			}
		}
		w.Write(body)
		return
	}
	io.Copy(w, resp.Body)
}

// HandleTaskHistory proxies task history request to the agent
func (h *Handlers) HandleTaskHistory(w http.ResponseWriter, r *http.Request, taskID string) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}

	// Forward to agent
	client := createHTTPClient(5 * time.Second)
	resp, err := client.Get(agentURL + "/history/" + taskID)
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

// HandleAgentLogs proxies log requests to the agent
func (h *Handlers) HandleAgentLogs(w http.ResponseWriter, r *http.Request) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}

	// Build the proxy URL with query parameters
	proxyURL := agentURL + "/logs"
	queryParams := []string{}
	if taskID := r.URL.Query().Get("task_id"); taskID != "" {
		queryParams = append(queryParams, "task_id="+taskID)
	}
	if level := r.URL.Query().Get("level"); level != "" {
		queryParams = append(queryParams, "level="+level)
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		queryParams = append(queryParams, "limit="+limit)
	}
	if since := r.URL.Query().Get("since"); since != "" {
		queryParams = append(queryParams, "since="+since)
	}
	if until := r.URL.Query().Get("until"); until != "" {
		queryParams = append(queryParams, "until="+until)
	}
	if len(queryParams) > 0 {
		proxyURL += "?"
		for i, param := range queryParams {
			if i > 0 {
				proxyURL += "&"
			}
			proxyURL += param
		}
	}

	// Forward to agent
	client := createHTTPClient(5 * time.Second)
	resp, err := client.Get(proxyURL)
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

// HandleAgentLogStats proxies log stats requests to the agent
func (h *Handlers) HandleAgentLogStats(w http.ResponseWriter, r *http.Request) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}

	// Forward to agent
	client := createHTTPClient(5 * time.Second)
	resp, err := client.Get(agentURL + "/logs/stats")
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

// HandleSessions returns all sessions
func (h *Handlers) HandleSessions(w http.ResponseWriter, r *http.Request) {
	sessions := h.sessionStore.GetAll()
	if sessions == nil {
		sessions = []*Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// SessionTaskRequest represents a request to add a task to a session
type SessionTaskRequest struct {
	SessionID string `json:"session_id"`
	AgentURL  string `json:"agent_url"`
	TaskID    string `json:"task_id"`
	State     string `json:"state"`
	Prompt    string `json:"prompt"`
}

// HandleAddSessionTask adds a task to a session
func (h *Handlers) HandleAddSessionTask(w http.ResponseWriter, r *http.Request) {
	var req SessionTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if req.SessionID == "" || req.TaskID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "session_id and task_id are required")
		return
	}

	h.sessionStore.AddTask(req.SessionID, req.AgentURL, req.TaskID, req.State, req.Prompt)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// SessionTaskUpdateRequest represents a request to update a task state
type SessionTaskUpdateRequest struct {
	State string `json:"state"`
}

// HandleUpdateSessionTask updates a task's state within a session
func (h *Handlers) HandleUpdateSessionTask(w http.ResponseWriter, r *http.Request, sessionID, taskID string) {
	var req SessionTaskUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Invalid JSON: "+err.Error())
		return
	}

	if !h.sessionStore.UpdateTaskState(sessionID, taskID, req.State) {
		writeError(w, http.StatusNotFound, api.ErrorNotFound, "Session or task not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DashboardData represents the consolidated dashboard response
type DashboardData struct {
	Agents    []*ComponentStatus `json:"agents"`
	Directors []*ComponentStatus `json:"directors"`
	Helpers   []*ComponentStatus `json:"helpers"`
	Sessions  []*Session         `json:"sessions"`
	Queue     *QueueInfo         `json:"queue,omitempty"`
}

// QueueInfo represents queue status in dashboard data
type QueueInfo struct {
	Depth            int                 `json:"depth"`
	MaxSize          int                 `json:"max_size"`
	OldestAgeSeconds float64             `json:"oldest_age_seconds"`
	DispatchedCount  int                 `json:"dispatched_count"`
	Tasks            []QueuedTaskSummary `json:"tasks"`
}

// HandleDashboardData returns all dashboard data in a single request with ETag support
func (h *Handlers) HandleDashboardData(w http.ResponseWriter, r *http.Request) {
	agents := h.discovery.Agents()
	if agents == nil {
		agents = []*ComponentStatus{}
	}

	directors := h.discovery.Directors()
	if directors == nil {
		directors = []*ComponentStatus{}
	}

	helpers := h.discovery.Helpers()
	if helpers == nil {
		helpers = []*ComponentStatus{}
	}

	sessions := h.sessionStore.GetAll()
	if sessions == nil {
		sessions = []*Session{}
	}

	data := DashboardData{
		Agents:    agents,
		Directors: directors,
		Helpers:   helpers,
		Sessions:  sessions,
	}

	// Add queue info if available
	if h.queue != nil {
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
		data.Queue = &QueueInfo{
			Depth:            h.queue.Depth(),
			MaxSize:          h.queue.Config().MaxSize,
			OldestAgeSeconds: h.queue.OldestAge(),
			DispatchedCount:  h.queue.DispatchedCount(),
			Tasks:            summaries,
		}
	}

	// Generate ETag from JSON content
	jsonData, err := json.Marshal(data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal_error", err.Error())
		return
	}

	hash := sha256.Sum256(jsonData)
	etag := `"` + hex.EncodeToString(hash[:8]) + `"`

	// Check If-None-Match header
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	w.Write(jsonData)
}

// HandleContexts returns available contexts
func (h *Handlers) HandleContexts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.contexts.GetAllContexts())
}

// HandleLoginPage renders the login form
func (h *Handlers) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to dashboard
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		if session := h.authStore.GetSession(cookie.Value); session != nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "login.html", nil); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleLogin processes login form submission
func (h *Handlers) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ip = realIP
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	password := r.FormValue("password")
	if password == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "Password is required")
		return
	}

	// Validate password
	if !h.authStore.ValidatePassword(password) {
		writeError(w, http.StatusUnauthorized, api.ErrorUnauthorized, "Invalid password")
		return
	}

	// Create session
	session, err := h.authStore.CreateAuthSession(ip, r.UserAgent())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "Failed to create session")
		return
	}

	// Set cookie and redirect
	SetSessionCookie(w, session.ID, h.secureCookie)
	http.Redirect(w, r, "/", http.StatusFound)
}

// HandleLogout destroys the session
func (h *Handlers) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		h.authStore.DeleteSession(cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// HandlePairPage renders the pairing form
func (h *Handlers) HandlePairPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to dashboard
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		if session := h.authStore.GetSession(cookie.Value); session != nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "pair.html", nil); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandlePair processes pairing code submission
func (h *Handlers) HandlePair(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ip = realIP
	}

	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid form data")
		return
	}

	code := r.FormValue("code")
	label := r.FormValue("label")
	if label == "" {
		label = "Unknown Device"
	}

	if code == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "Pairing code is required")
		return
	}

	// Create device session
	session, err := h.authStore.CreateDeviceSession(code, label, ip, r.UserAgent())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_code", "Invalid or expired pairing code")
		return
	}

	// Set long-lived cookie for device session
	SetDeviceSessionCookie(w, session.ID, h.secureCookie)
	http.Redirect(w, r, "/", http.StatusFound)
}

// PairingCodeResponse is returned when generating a pairing code
type PairingCodeResponse struct {
	Code      string `json:"code"`
	ExpiresIn int    `json:"expires_in"` // seconds
}

// HandleGeneratePairingCode creates a new pairing code (requires session)
func (h *Handlers) HandleGeneratePairingCode(w http.ResponseWriter, r *http.Request) {
	code, err := h.authStore.CreatePairingCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generation_error", "Failed to generate pairing code")
		return
	}

	writeJSON(w, http.StatusCreated, PairingCodeResponse{
		Code:      code,
		ExpiresIn: int(PairingCodeTTL.Seconds()),
	})
}

// DeviceInfo represents a paired device
type DeviceInfo struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	IPAddress string    `json:"ip_address"`
	IsCurrent bool      `json:"is_current"` // Is this the current session?
}

// HandleListDevices returns all paired devices (requires session)
func (h *Handlers) HandleListDevices(w http.ResponseWriter, r *http.Request) {
	currentSession := GetSessionFromContext(r.Context())

	sessions := h.authStore.ListAllSessions()
	devices := make([]DeviceInfo, 0, len(sessions))

	for _, s := range sessions {
		devices = append(devices, DeviceInfo{
			ID:        s.ID,
			Label:     s.Label,
			CreatedAt: s.CreatedAt,
			LastSeen:  s.LastSeen,
			IPAddress: s.IPAddress,
			IsCurrent: currentSession != nil && s.ID == currentSession.ID,
		})
	}

	writeJSON(w, http.StatusOK, devices)
}

// HandleRevokeDevice removes a device session (requires session)
func (h *Handlers) HandleRevokeDevice(w http.ResponseWriter, r *http.Request, deviceID string) {
	currentSession := GetSessionFromContext(r.Context())

	// Prevent revoking own session
	if currentSession != nil && deviceID == currentSession.ID {
		writeError(w, http.StatusBadRequest, "invalid_request", "Cannot revoke your own session")
		return
	}

	// Check if device exists
	session := h.authStore.GetSession(deviceID)
	if session == nil {
		writeError(w, http.StatusNotFound, api.ErrorNotFound, "Device not found")
		return
	}

	h.authStore.DeleteSession(deviceID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleArchiveSession archives a session (hides it from UI but keeps it in storage)
func (h *Handlers) HandleArchiveSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	if !h.sessionStore.Archive(sessionID) {
		writeError(w, http.StatusNotFound, api.ErrorNotFound, "Session not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleTriggerJob proxies a job trigger request to a scheduler
func (h *Handlers) HandleTriggerJob(w http.ResponseWriter, r *http.Request, schedulerURL, jobName string) {
	client := createHTTPClient(10 * time.Second)

	req, err := http.NewRequest(http.MethodPost, schedulerURL+"/trigger/"+jobName, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_error", "Failed to create request: "+err.Error())
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "scheduler_error", "Failed to contact scheduler: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Forward the scheduler's response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// HandleShutdown initiates graceful shutdown of all services.
// Sends shutdown requests to discovered agents and helpers, then shuts down self.
func (h *Handlers) HandleShutdown(w http.ResponseWriter, r *http.Request) {
	if h.shutdownFunc == nil {
		writeError(w, http.StatusServiceUnavailable, "shutdown_unavailable", "Shutdown not configured")
		return
	}

	// Collect all discovered services
	agents := h.discovery.Agents()
	helpers := h.discovery.Helpers()

	client := createHTTPClient(5 * time.Second)
	var shutdownErrors []string

	// Send shutdown to agents
	for _, agent := range agents {
		req, err := http.NewRequest(http.MethodPost, agent.URL+"/shutdown", nil)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Sprintf("agent %s: %v", agent.URL, err))
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Sprintf("agent %s: %v", agent.URL, err))
			continue
		}
		resp.Body.Close()
	}

	// Send shutdown to helpers (schedulers, etc.)
	for _, helper := range helpers {
		req, err := http.NewRequest(http.MethodPost, helper.URL+"/shutdown", nil)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Sprintf("helper %s: %v", helper.URL, err))
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Sprintf("helper %s: %v", helper.URL, err))
			continue
		}
		resp.Body.Close()
	}

	// Respond before shutting down self
	resp := map[string]interface{}{
		"status":           "shutting_down",
		"agents_notified":  len(agents),
		"helpers_notified": len(helpers),
	}
	if len(shutdownErrors) > 0 {
		resp["errors"] = shutdownErrors
	}
	writeJSON(w, http.StatusOK, resp)

	// Trigger self-shutdown in background (allows response to be sent)
	go func() {
		time.Sleep(100 * time.Millisecond)
		h.shutdownFunc()
	}()
}
