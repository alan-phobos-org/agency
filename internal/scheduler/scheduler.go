package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	Job        *Job
	Cron       *CronExpr
	mu         sync.RWMutex
	NextRun    time.Time
	LastRun    time.Time
	LastStatus string // "submitted", "skipped_busy", "skipped_error"
	LastTaskID string
}

// JobStatus represents a job in the status response
type JobStatus struct {
	Name       string    `json:"name"`
	Schedule   string    `json:"schedule"`
	NextRun    time.Time `json:"next_run"`
	LastRun    time.Time `json:"last_run,omitempty"`
	LastStatus string    `json:"last_status,omitempty"`
	LastTaskID string    `json:"last_task_id,omitempty"`
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
		s.jobs[i] = &jobState{
			Job:     job,
			Cron:    cron,
			NextRun: cron.Next(now),
		}
	}
	// Start HTTP server
	router := chi.NewRouter()
	router.Get("/status", s.handleStatus)
	router.Post("/shutdown", s.handleShutdown)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: router,
	}
	s.running = true
	s.mu.Unlock()

	// Start job runner
	go s.runJobs()

	log.Printf("scheduler starting on port %d with %d jobs", s.config.Port, len(s.jobs))
	s.mu.RLock()
	for _, js := range s.jobs {
		log.Printf("  job=%s schedule=%q next_run=%s", js.Job.Name, js.Job.Schedule, js.NextRun.Format(time.RFC3339))
	}
	s.mu.RUnlock()

	if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
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
		js.mu.RLock()
		nextRun := js.NextRun
		js.mu.RUnlock()

		if now.After(nextRun) || now.Equal(nextRun) {
			s.runJob(js)
		}
	}
}

// runJob executes a single job
func (s *Scheduler) runJob(js *jobState) {
	log.Printf("job=%s action=triggered", js.Job.Name)

	agentURL := s.config.GetAgentURL(js.Job)
	model := s.config.GetModel(js.Job)
	timeout := s.config.GetTimeout(js.Job)

	// Build task request
	taskReq := map[string]interface{}{
		"prompt":          js.Job.Prompt,
		"model":           model,
		"timeout_seconds": int(timeout.Seconds()),
	}

	body, _ := json.Marshal(taskReq)
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Post(agentURL+"/task", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("job=%s action=skipped reason=error error=%q", js.Job.Name, err)
		s.updateJobState(js, "skipped_error", "")
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		log.Printf("job=%s action=skipped reason=agent_busy", js.Job.Name)
		s.updateJobState(js, "skipped_busy", "")
		return
	}

	if resp.StatusCode != http.StatusCreated {
		log.Printf("job=%s action=skipped reason=error status=%d body=%q", js.Job.Name, resp.StatusCode, string(respBody))
		s.updateJobState(js, "skipped_error", "")
		return
	}

	// Parse response
	var taskResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(respBody, &taskResp); err != nil {
		log.Printf("job=%s action=submitted warning=parse_error", js.Job.Name)
		s.updateJobState(js, "submitted", "")
		return
	}

	log.Printf("job=%s action=submitted task_id=%s", js.Job.Name, taskResp.TaskID)
	s.updateJobState(js, "submitted", taskResp.TaskID)
}

// updateJobState updates job state after execution
func (s *Scheduler) updateJobState(js *jobState, status, taskID string) {
	js.mu.Lock()
	defer js.mu.Unlock()

	now := time.Now()
	js.LastRun = now
	js.LastStatus = status
	js.LastTaskID = taskID
	js.NextRun = js.Cron.Next(now)
}

// handleStatus returns scheduler status
func (s *Scheduler) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	jobs := s.jobs
	s.mu.RUnlock()

	jobStatuses := make([]JobStatus, len(jobs))
	for i, js := range jobs {
		js.mu.RLock()
		jobStatuses[i] = JobStatus{
			Name:       js.Job.Name,
			Schedule:   js.Job.Schedule,
			NextRun:    js.NextRun,
			LastRun:    js.LastRun,
			LastStatus: js.LastStatus,
			LastTaskID: js.LastTaskID,
		}
		js.mu.RUnlock()
	}

	resp := map[string]interface{}{
		"type":           api.TypeHelper,
		"interfaces":     []string{api.InterfaceStatusable, api.InterfaceObservable},
		"version":        s.version,
		"state":          "running",
		"uptime_seconds": time.Since(s.startTime).Seconds(),
		"config": map[string]interface{}{
			"agent_url": s.config.AgentURL,
		},
		"jobs": jobStatuses,
	}

	api.WriteJSON(w, http.StatusOK, resp)
}

// handleShutdown handles graceful shutdown requests
func (s *Scheduler) handleShutdown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	json.NewDecoder(r.Body).Decode(&req)

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
