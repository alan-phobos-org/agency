package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/anthropics/agency/internal/api"
)

var (
	writeJSON  = api.WriteJSON
	writeError = api.WriteError
)

//go:embed templates/*.html
var templatesFS embed.FS

// Handlers holds HTTP handler dependencies
type Handlers struct {
	discovery    *Discovery
	version      string
	startTime    time.Time
	tmpl         *template.Template
	sessionStore *SessionStore
	contexts     *ContextsConfig
	authStore    *AuthStore
	rateLimiter  *RateLimiter
	secureCookie bool // Whether to set Secure flag on cookies (HTTPS)
}

// NewHandlers creates handlers with dependencies
func NewHandlers(discovery *Discovery, version string, contexts *ContextsConfig, authStore *AuthStore, rateLimiter *RateLimiter, secureCookie bool) (*Handlers, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
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
		rateLimiter:  rateLimiter,
		secureCookie: secureCookie,
	}, nil
}

// HandleDashboard serves the main dashboard HTML page
func (h *Handlers) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", nil); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleStatus returns the web view's own status (universal /status endpoint)
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"type":           api.TypeView,
		"interfaces":     []string{api.InterfaceStatusable, api.InterfaceObservable},
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

// TaskSubmitRequest represents a task submission through the web view
type TaskSubmitRequest struct {
	AgentURL       string            `json:"agent_url"`
	Prompt         string            `json:"prompt"`
	Model          string            `json:"model,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	SessionID      string            `json:"session_id,omitempty"` // Continue existing session
	Env            map[string]string `json:"env,omitempty"`
	Thinking       *bool             `json:"thinking,omitempty"` // Enable extended thinking (default: true)
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

	// Build agent task request
	agentReq := map[string]interface{}{
		"prompt": req.Prompt,
	}
	if req.Model != "" {
		agentReq["model"] = req.Model
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
		TaskID    string `json:"task_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(respBody, &agentResp); err != nil {
		writeError(w, http.StatusBadGateway, "parse_error", "Invalid agent response")
		return
	}

	writeJSON(w, http.StatusCreated, TaskSubmitResponse{
		TaskID:    agentResp.TaskID,
		AgentURL:  req.AgentURL,
		SessionID: agentResp.SessionID,
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

// HandleTaskHistory proxies task history request to the agent
func (h *Handlers) HandleTaskHistory(w http.ResponseWriter, r *http.Request, taskID string) {
	agentURL := r.URL.Query().Get("agent_url")
	if agentURL == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "agent_url query parameter is required")
		return
	}

	// Forward to agent
	client := &http.Client{Timeout: 5 * time.Second}
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
		writeError(w, http.StatusNotFound, "not_found", "Session or task not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DashboardData represents the consolidated dashboard response
type DashboardData struct {
	Agents    []*ComponentStatus `json:"agents"`
	Directors []*ComponentStatus `json:"directors"`
	Sessions  []*Session         `json:"sessions"`
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

	sessions := h.sessionStore.GetAll()
	if sessions == nil {
		sessions = []*Session{}
	}

	data := DashboardData{
		Agents:    agents,
		Directors: directors,
		Sessions:  sessions,
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

	// Check rate limiting
	if h.rateLimiter != nil && h.rateLimiter.IsBlocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many failed attempts. Try again later.")
		return
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
		if h.rateLimiter != nil {
			h.rateLimiter.RecordFailure(ip)
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid password")
		return
	}

	// Create session
	session, err := h.authStore.CreateAuthSession(ip, r.UserAgent())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_error", "Failed to create session")
		return
	}

	if h.rateLimiter != nil {
		h.rateLimiter.RecordSuccess(ip)
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

	// Check rate limiting
	if h.rateLimiter != nil && h.rateLimiter.IsBlocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many failed attempts. Try again later.")
		return
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
		if h.rateLimiter != nil {
			h.rateLimiter.RecordFailure(ip)
		}
		writeError(w, http.StatusUnauthorized, "invalid_code", "Invalid or expired pairing code")
		return
	}

	if h.rateLimiter != nil {
		h.rateLimiter.RecordSuccess(ip)
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
		writeError(w, http.StatusNotFound, "not_found", "Device not found")
		return
	}

	h.authStore.DeleteSession(deviceID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
