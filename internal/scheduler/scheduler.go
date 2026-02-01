package scheduler

import (
	"bytes"
	"context"
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
	"phobos.org.uk/agency/internal/tlsutil"
)

// Scheduler manages scheduled jobs
type Scheduler struct {
	config               *Config
	configPath           string        // Path to config file for hot-reload
	configModTime        time.Time     // Last known modification time of config file
	configReloadInterval time.Duration // How often to check for config changes
	version              string
	startTime            time.Time

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
func New(config *Config, configPath string, configReloadInterval time.Duration, version string) *Scheduler {
	return &Scheduler{
		config:               config,
		configPath:           configPath,
		configReloadInterval: configReloadInterval,
		version:              version,
		startTime:            time.Now(),
		stopChan:             make(chan struct{}),
	}
}

// Start starts the scheduler
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}

	// Initialize config modification time for hot-reload
	if fileInfo, err := os.Stat(s.configPath); err == nil {
		s.configModTime = fileInfo.ModTime()
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

	// Start config watcher
	go s.watchConfig()

	log.Printf("scheduler action=starting port=%d jobs=%d config_reload_interval=%s", s.config.Port, len(s.jobs), s.configReloadInterval)
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

// watchConfig watches for config file changes and reloads
func (s *Scheduler) watchConfig() {
	ticker := time.NewTicker(s.configReloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.checkAndReloadConfig()
		}
	}
}

// checkAndReloadConfig checks if config file has changed and reloads it
func (s *Scheduler) checkAndReloadConfig() {
	// Check modification time (no lock needed, read-only operation)
	fileInfo, err := os.Stat(s.configPath)
	if err != nil {
		log.Printf("config_reload action=check_failed error=%q", err)
		return
	}

	modTime := fileInfo.ModTime()
	s.mu.RLock()
	lastModTime := s.configModTime
	s.mu.RUnlock()

	if !modTime.After(lastModTime) {
		return // No change
	}

	log.Printf("config_reload action=detected_change old_mtime=%s new_mtime=%s", lastModTime.Format(time.RFC3339), modTime.Format(time.RFC3339))

	// Load new config (no lock needed, filesystem I/O)
	newConfig, err := Load(s.configPath)
	if err != nil {
		log.Printf("config_reload action=load_failed error=%q config=kept_current", err)
		return
	}

	// Check if port changed (requires restart)
	s.mu.RLock()
	oldPort := s.config.Port
	s.mu.RUnlock()
	if newConfig.Port != oldPort {
		log.Printf("config_reload warning=port_change old=%d new=%d requires_restart=true", oldPort, newConfig.Port)
	}

	// Apply the new config
	s.applyConfig(newConfig, modTime)
}

// applyConfig safely applies a new config, preserving job state where possible
func (s *Scheduler) applyConfig(newConfig *Config, modTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldJobs := s.jobs
	s.config = newConfig
	s.configModTime = modTime

	// Build new jobs array, preserving state where possible
	now := time.Now()
	newJobs := make([]*jobState, len(newConfig.Jobs))

	preserved := 0
	added := 0

	for i := range newConfig.Jobs {
		job := &newConfig.Jobs[i]
		cron, _ := ParseCron(job.Schedule) // Already validated in Load()

		// Find matching old job by name (unique identifier)
		var oldState *jobState
		for _, oldJob := range oldJobs {
			if oldJob.Job.Name == job.Name {
				oldState = oldJob
				break
			}
		}

		if oldState != nil {
			// Preserve execution state but update definition
			oldState.mu.Lock()
			wasRunning := oldState.isRunning
			oldState.Job = job   // Use new definition (prompt, timeout, tier, etc.)
			oldState.Cron = cron // Use new schedule
			if !wasRunning {
				oldState.NextRun = cron.Next(now) // Recalculate if not running
			}
			// Keep: LastRun, LastStatus, LastTaskID, LastQueueID, isRunning
			oldState.mu.Unlock()
			newJobs[i] = oldState
			preserved++
		} else {
			// New job - initialize fresh
			nextRun := cron.Next(now)
			if nextRun.IsZero() {
				nextRun = now.Add(24 * time.Hour)
			}
			newJobs[i] = &jobState{
				Job:     job,
				Cron:    cron,
				NextRun: nextRun,
			}
			added++
		}
	}

	removed := len(oldJobs) - preserved

	s.jobs = newJobs

	log.Printf("config_reload action=applied jobs_before=%d jobs_after=%d preserved=%d added=%d removed=%d",
		len(oldJobs), len(newJobs), preserved, added, removed)
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
	queueReq := map[string]any{
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

	taskReq := map[string]any{
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
	return tlsutil.NewHTTPClient(30*time.Second, targetURL)
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

	configInfo := map[string]any{
		"agent_url": s.config.AgentURL,
	}
	if s.config.DirectorURL != "" {
		configInfo["director_url"] = s.config.DirectorURL
	}

	resp := map[string]any{
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

	// Find the job while holding read lock
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

	// Run job synchronously (lock is now released, allowing hot reloads to proceed)
	s.runJob(target)

	// Return current state
	target.mu.RLock()
	resp := map[string]any{
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
