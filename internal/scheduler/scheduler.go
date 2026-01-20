package scheduler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"phobos.org.uk/agency/internal/api"
)

// Scheduler manages scheduled jobs
type Scheduler struct {
	config    *Config
	version   string
	startTime time.Time

	mu       sync.RWMutex
	server   *http.Server
	jobs     []*jobState
	running  bool
	stopChan chan struct{}
}

// jobState tracks runtime state for a job
type jobState struct {
	Job         *Job
	Cron        *CronExpr
	mu          sync.RWMutex
	NextRun     time.Time
	LastRun     time.Time
	LastStatus  string // "queued", "submitted", "skipped_queue_full", "skipped_busy", "skipped_error"
	LastTaskID  string // Agent task ID (for direct submission)
	LastQueueID string // Queue ID (for queue submission)
	isRunning   bool   // prevents double-invocation if job execution takes >1s
}

// JobStatus represents a job in the status response
type JobStatus struct {
	Name        string     `json:"name"`
	Schedule    string     `json:"schedule"`
	NextRun     time.Time  `json:"next_run"`
	LastRun     *time.Time `json:"last_run,omitempty"`
	LastStatus  string     `json:"last_status,omitempty"`
	LastTaskID  string     `json:"last_task_id,omitempty"`
	LastQueueID string     `json:"last_queue_id,omitempty"`
}

// New creates a new scheduler
func New(config *Config, version string) *Scheduler {
	return &Scheduler{
		config:    config,
		version:   version,
		startTime: time.Now(),
		stopChan:  make(chan struct{}),
	}
}

// Start starts the scheduler
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}

	// Initialize job states
	now := time.Now()
	s.jobs = make([]*jobState, len(s.config.Jobs))
	for i := range s.config.Jobs {
		job := &s.config.Jobs[i]
		cron, _ := ParseCron(job.Schedule) // Already validated
		nextRun := cron.Next(now)
		if nextRun.IsZero() {
			// Defensive: if Next() can't find a match, skip far into the future
			nextRun = now.Add(24 * time.Hour)
		}
		s.jobs[i] = &jobState{
			Job:     job,
			Cron:    cron,
			NextRun: nextRun,
		}
	}
	// Start HTTP server
	router := chi.NewRouter()
	router.Get("/status", s.handleStatus)
	router.Post("/shutdown", s.handleShutdown)
	router.Post("/trigger/{job}", s.handleTrigger)

	// Setup TLS certificates
	certDir := filepath.Join(os.TempDir(), "agency", "scheduler-certs")
	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")

	if err := ensureTLSCert(certPath, keyPath); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("ensuring TLS cert: %w", err)
	}

	s.server = &http.Server{
		Addr:      fmt.Sprintf(":%d", s.config.Port),
		Handler:   router,
		TLSConfig: getTLSConfig(),
	}
	s.running = true
	s.mu.Unlock()

	// Start job runner
	go s.runJobs()

	log.Printf("scheduler starting on port %d with %d jobs (TLS enabled)", s.config.Port, len(s.jobs))
	s.mu.RLock()
	for _, js := range s.jobs {
		log.Printf("  job=%s schedule=%q next_run=%s", js.Job.Name, js.Job.Schedule, js.NextRun.Format(time.RFC3339))
	}
	s.mu.RUnlock()

	if err := s.server.ListenAndServeTLS(certPath, keyPath); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully shuts down the scheduler
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	close(s.stopChan)
	server := s.server
	s.mu.Unlock()

	if server != nil {
		return server.Shutdown(ctx)
	}
	return nil
}

// runJobs is the main job runner loop
func (s *Scheduler) runJobs() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case now := <-ticker.C:
			s.checkAndRunJobs(now)
		}
	}
}

// checkAndRunJobs checks if any jobs should run
func (s *Scheduler) checkAndRunJobs(now time.Time) {
	s.mu.RLock()
	jobs := s.jobs
	s.mu.RUnlock()

	for _, js := range jobs {
		js.mu.Lock()
		nextRun := js.NextRun
		running := js.isRunning
		if !running && (now.After(nextRun) || now.Equal(nextRun)) {
			js.isRunning = true
			js.mu.Unlock()
			s.runJob(js)
		} else {
			js.mu.Unlock()
		}
	}
}

// runJob executes a single job, trying queue API first then falling back to agent
func (s *Scheduler) runJob(js *jobState) {
	log.Printf("job=%s action=triggered", js.Job.Name)

	// Try queue API via director first (preferred path)
	if s.config.DirectorURL != "" {
		queueID, err := s.submitViaQueue(js)
		if err == nil {
			log.Printf("job=%s action=queued via=director queue_id=%s", js.Job.Name, queueID)
			s.updateJobStateQueue(js, "queued", queueID)
			return
		}
		// Check if it's a queue full error
		if strings.Contains(err.Error(), "queue full") || strings.Contains(err.Error(), "503") {
			log.Printf("job=%s action=skipped reason=queue_full error=%q", js.Job.Name, err)
			s.updateJobStateQueue(js, "skipped_queue_full", "")
			return
		}
		log.Printf("job=%s warning=director_unavailable error=%q", js.Job.Name, err)
	}

	// Fallback to direct agent submission
	taskID, status, err := s.submitViaAgent(js)
	if err != nil {
		log.Printf("job=%s action=skipped reason=%s error=%q", js.Job.Name, status, err)
		s.updateJobState(js, status, "")
		return
	}

	via := "agent"
	if s.config.DirectorURL != "" {
		via = "agent_fallback"
	}
	log.Printf("job=%s action=submitted via=%s task_id=%s", js.Job.Name, via, taskID)
	s.updateJobState(js, "submitted", taskID)
}

// submitViaQueue submits a task through the queue API
func (s *Scheduler) submitViaQueue(js *jobState) (string, error) {
	tier := s.config.GetTier(js.Job)
	timeout := s.config.GetTimeout(js.Job)
	agentKind := s.config.GetAgentKind(js.Job)

	// Build queue request
	queueReq := map[string]interface{}{
		"prompt":          js.Job.Prompt,
		"timeout_seconds": int(timeout.Seconds()),
		"source":          "scheduler",
		"source_job":      js.Job.Name,
		"agent_kind":      agentKind,
		"tier":            tier,
	}

	body, _ := json.Marshal(queueReq)
	client := s.createHTTPClient(s.config.DirectorURL)

	resp, err := client.Post(s.config.DirectorURL+"/api/queue/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("contacting director: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusServiceUnavailable {
		return "", fmt.Errorf("queue full (503)")
	}

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("director returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var queueResp struct {
		QueueID string `json:"queue_id"`
	}
	if err := json.Unmarshal(respBody, &queueResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	return queueResp.QueueID, nil
}

// submitViaAgent submits a task directly to the agent (fallback path)
func (s *Scheduler) submitViaAgent(js *jobState) (taskID string, status string, err error) {
	agentURL := s.config.GetAgentURL(js.Job)
	tier := s.config.GetTier(js.Job)
	timeout := s.config.GetTimeout(js.Job)

	taskReq := map[string]interface{}{
		"prompt":          js.Job.Prompt,
		"timeout_seconds": int(timeout.Seconds()),
		"tier":            tier,
	}

	body, _ := json.Marshal(taskReq)
	client := s.createHTTPClient(agentURL)

	resp, err := client.Post(agentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "skipped_error", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		return "", "skipped_busy", fmt.Errorf("agent busy")
	}

	if resp.StatusCode != http.StatusCreated {
		return "", "skipped_error", fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var taskResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		// Task was submitted but we couldn't parse the response
		return "", "submitted", nil
	}

	return taskResp.TaskID, "submitted", nil
}

// createHTTPClient creates an HTTP client, with TLS skip verification for localhost HTTPS
func (s *Scheduler) createHTTPClient(targetURL string) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}

	// Skip TLS verification for localhost HTTPS (self-signed certs)
	if strings.HasPrefix(targetURL, "https://localhost") ||
		strings.HasPrefix(targetURL, "https://127.0.0.1") {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	return client
}

// updateJobState updates job state after execution (for direct agent submission)
func (s *Scheduler) updateJobState(js *jobState, status, taskID string) {
	js.mu.Lock()
	defer js.mu.Unlock()

	now := time.Now()
	js.LastRun = now
	js.LastStatus = status
	js.LastTaskID = taskID
	js.LastQueueID = "" // Clear queue ID for direct submissions
	nextRun := js.Cron.Next(now)
	if nextRun.IsZero() {
		// Defensive: if Next() can't find a match, skip far into the future
		nextRun = now.Add(24 * time.Hour)
	}
	js.NextRun = nextRun
	js.isRunning = false
}

// updateJobStateQueue updates job state after queue submission
func (s *Scheduler) updateJobStateQueue(js *jobState, status, queueID string) {
	js.mu.Lock()
	defer js.mu.Unlock()

	now := time.Now()
	js.LastRun = now
	js.LastStatus = status
	js.LastTaskID = "" // Clear task ID for queue submissions
	js.LastQueueID = queueID
	nextRun := js.Cron.Next(now)
	if nextRun.IsZero() {
		// Defensive: if Next() can't find a match, skip far into the future
		nextRun = now.Add(24 * time.Hour)
	}
	js.NextRun = nextRun
	js.isRunning = false
}

// handleStatus returns scheduler status
func (s *Scheduler) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	jobs := s.jobs
	s.mu.RUnlock()

	jobStatuses := make([]JobStatus, len(jobs))
	for i, js := range jobs {
		js.mu.RLock()
		status := JobStatus{
			Name:        js.Job.Name,
			Schedule:    js.Job.Schedule,
			NextRun:     js.NextRun,
			LastStatus:  js.LastStatus,
			LastTaskID:  js.LastTaskID,
			LastQueueID: js.LastQueueID,
		}
		if !js.LastRun.IsZero() {
			lastRun := js.LastRun
			status.LastRun = &lastRun
		}
		jobStatuses[i] = status
		js.mu.RUnlock()
	}

	configInfo := map[string]interface{}{
		"agent_url": s.config.AgentURL,
	}
	if s.config.DirectorURL != "" {
		configInfo["director_url"] = s.config.DirectorURL
	}

	resp := map[string]interface{}{
		"type":           api.TypeHelper,
		"interfaces":     []string{api.InterfaceStatusable, api.InterfaceObservable},
		"version":        s.version,
		"state":          "running",
		"uptime_seconds": time.Since(s.startTime).Seconds(),
		"config":         configInfo,
		"jobs":           jobStatuses,
	}

	api.WriteJSON(w, http.StatusOK, resp)
}

// handleShutdown handles graceful shutdown requests
func (s *Scheduler) handleShutdown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	// Ignore decode errors - Force defaults to false which is safe
	_ = json.NewDecoder(r.Body).Decode(&req)

	api.WriteJSON(w, http.StatusOK, map[string]string{"status": "shutting_down"})

	go func() {
		ctx := context.Background()
		if !req.Force {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
		}
		s.Shutdown(ctx)
	}()
}

// handleTrigger manually triggers a job by name
func (s *Scheduler) handleTrigger(w http.ResponseWriter, r *http.Request) {
	jobName := chi.URLParam(r, "job")

	s.mu.RLock()
	var target *jobState
	for _, js := range s.jobs {
		if js.Job.Name == jobName {
			target = js
			break
		}
	}
	s.mu.RUnlock()

	if target == nil {
		api.WriteJSON(w, http.StatusNotFound, map[string]string{
			"error": api.ErrorJobNotFound,
			"name":  jobName,
		})
		return
	}

	// Check if already running
	target.mu.Lock()
	if target.isRunning {
		target.mu.Unlock()
		api.WriteJSON(w, http.StatusConflict, map[string]string{
			"error": api.ErrorJobAlreadyRunning,
			"name":  jobName,
		})
		return
	}
	target.isRunning = true
	target.mu.Unlock()

	// Run job synchronously so caller can see result
	s.runJob(target)

	// Return current state
	target.mu.RLock()
	resp := map[string]interface{}{
		"name":        target.Job.Name,
		"last_status": target.LastStatus,
	}
	if target.LastTaskID != "" {
		resp["last_task_id"] = target.LastTaskID
	}
	if target.LastQueueID != "" {
		resp["last_queue_id"] = target.LastQueueID
	}
	target.mu.RUnlock()

	api.WriteJSON(w, http.StatusOK, resp)
}
