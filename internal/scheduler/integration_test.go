//go:build integration

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"phobos.org.uk/agency/internal/testutil"
)

func TestIntegrationSchedulerStartStop(t *testing.T) {
	t.Parallel()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:     port,
		AgentURL: "https://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test-version")

	// Start in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Wait for scheduler to be ready
	schedulerURL := fmt.Sprintf("https://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Verify status endpoint works
	resp, err := testutil.HTTPClient(5 * time.Second).Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&status)
	require.NoError(t, err)

	assert.Equal(t, "helper", status["type"])
	assert.Equal(t, "running", status["state"])
	assert.Equal(t, "test-version", status["version"])

	jobs, ok := status["jobs"].([]interface{})
	require.True(t, ok)
	assert.Len(t, jobs, 1)

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = s.Shutdown(ctx)
	require.NoError(t, err)

	// Verify stopped
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}

	// Verify no longer responding
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get(schedulerURL + "/status")
	require.Error(t, err)
}

func TestIntegrationSchedulerJobExecution(t *testing.T) {
	t.Parallel()

	// Create mock agent that tracks submissions
	var submissions int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			atomic.AddInt32(&submissions, 1)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:     port,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "frequent-job",
				Schedule: "* * * * *", // Every minute
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	// Initialize with NextRun in the past to trigger immediately
	s.mu.Lock()
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	s.jobs = []*jobState{{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: time.Now().Add(-time.Minute),
	}}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	// Manually check and run jobs
	s.checkAndRunJobs(time.Now())

	// Verify job was submitted
	assert.Equal(t, int32(1), atomic.LoadInt32(&submissions))

	// Verify job state was updated
	s.mu.RLock()
	js := s.jobs[0]
	s.mu.RUnlock()

	js.mu.RLock()
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-123", js.LastTaskID)
	assert.False(t, js.LastRun.IsZero())
	assert.True(t, js.NextRun.After(time.Now()))
	js.mu.RUnlock()
}

func TestIntegrationSchedulerConcurrentJobs(t *testing.T) {
	t.Parallel()

	// Create mock agent with delay to simulate real processing
	var submissions int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			n := atomic.AddInt32(&submissions, 1)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": fmt.Sprintf("task-%d", n),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:     port,
		AgentURL: agent.URL,
		Jobs: []Job{
			{Name: "job-1", Schedule: "* * * * *", Prompt: "Prompt 1"},
			{Name: "job-2", Schedule: "* * * * *", Prompt: "Prompt 2"},
			{Name: "job-3", Schedule: "* * * * *", Prompt: "Prompt 3"},
		},
	}

	s := New(cfg, "test")

	// Initialize all jobs with past NextRun
	s.mu.Lock()
	s.jobs = make([]*jobState, len(cfg.Jobs))
	for i := range cfg.Jobs {
		cron, _ := ParseCron(cfg.Jobs[i].Schedule)
		s.jobs[i] = &jobState{
			Job:     &cfg.Jobs[i],
			Cron:    cron,
			NextRun: time.Now().Add(-time.Minute),
		}
	}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	// Run jobs
	s.checkAndRunJobs(time.Now())

	// All 3 jobs should have been submitted
	assert.Equal(t, int32(3), atomic.LoadInt32(&submissions))

	// Verify all jobs were updated
	s.mu.RLock()
	for _, js := range s.jobs {
		js.mu.RLock()
		assert.Equal(t, "submitted", js.LastStatus)
		assert.False(t, js.LastRun.IsZero())
		js.mu.RUnlock()
	}
	s.mu.RUnlock()
}

func TestIntegrationSchedulerShutdownEndpoint(t *testing.T) {
	t.Parallel()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:     port,
		AgentURL: "https://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test",
			},
		},
	}

	s := New(cfg, "test")

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	schedulerURL := fmt.Sprintf("https://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Call shutdown endpoint
	resp, err := testutil.HTTPClient(5*time.Second).Post(schedulerURL+"/shutdown", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "shutting_down", result["status"])

	// Wait for shutdown
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not stop after shutdown endpoint")
	}
}

// --- Session Routing Integration Tests ---

func TestIntegrationSchedulerDirectorRouting(t *testing.T) {
	t.Parallel()

	var directorReceived struct {
		AgentURL  string `json:"agent_url"`
		Prompt    string `json:"prompt"`
		Source    string `json:"source"`
		SourceJob string `json:"source_job"`
	}
	var directorCalled atomic.Bool

	// Mock director that receives the task submission via queue API
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/queue/task" && r.Method == "POST" {
			directorCalled.Store(true)
			json.NewDecoder(r.Body).Decode(&directorReceived)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"queue_id": "queue-director-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer director.Close()

	// Agent URL for config (not actually called when queue succeeds)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Agent should not be called when queue submission succeeds")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer agent.Close()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:        port,
		DirectorURL: director.URL,
		AgentURL:    agent.URL,
		Jobs: []Job{
			{
				Name:     "routed-job",
				Schedule: "* * * * *",
				Prompt:   "Test job for director routing",
			},
		},
	}

	s := New(cfg, "test")

	// Initialize with NextRun in the past to trigger immediately
	s.mu.Lock()
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	s.jobs = []*jobState{{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: time.Now().Add(-time.Minute),
	}}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	// Run the job
	s.checkAndRunJobs(time.Now())

	// Verify director was called
	assert.True(t, directorCalled.Load(), "Director should be called")

	// Verify the request format (queue API doesn't include agent_url)
	assert.Equal(t, "Test job for director routing", directorReceived.Prompt)
	assert.Equal(t, "scheduler", directorReceived.Source, "Source should be 'scheduler'")
	assert.Equal(t, "routed-job", directorReceived.SourceJob, "SourceJob should be job name")

	// Verify job state was updated for queue submission
	s.mu.RLock()
	js := s.jobs[0]
	s.mu.RUnlock()

	js.mu.RLock()
	assert.Equal(t, "queued", js.LastStatus)
	assert.Equal(t, "queue-director-123", js.LastQueueID)
	js.mu.RUnlock()
}

func TestIntegrationSchedulerDirectorFallback(t *testing.T) {
	t.Parallel()

	var agentCalled atomic.Bool

	// Mock director that fails with 500 (not 503, which is treated as queue full)
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer director.Close()

	// Mock agent (should be called as fallback)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentCalled.Store(true)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-fallback-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:        port,
		DirectorURL: director.URL,
		AgentURL:    agent.URL,
		Jobs: []Job{
			{
				Name:     "fallback-job",
				Schedule: "* * * * *",
				Prompt:   "Test job for fallback",
			},
		},
	}

	s := New(cfg, "test")

	// Initialize with NextRun in the past
	s.mu.Lock()
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	s.jobs = []*jobState{{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: time.Now().Add(-time.Minute),
	}}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	// Run the job
	s.checkAndRunJobs(time.Now())

	// Verify agent was called as fallback
	assert.True(t, agentCalled.Load(), "Agent should be called as fallback")

	// Verify job state was updated
	s.mu.RLock()
	js := s.jobs[0]
	s.mu.RUnlock()

	js.mu.RLock()
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-fallback-123", js.LastTaskID)
	js.mu.RUnlock()
}

func TestIntegrationSchedulerStatusIncludesDirectorURL(t *testing.T) {
	t.Parallel()

	port := testutil.AllocateTestPort(t)
	cfg := &Config{
		Port:        port,
		DirectorURL: "http://director:8080",
		AgentURL:    "http://agent:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test",
			},
		},
	}

	s := New(cfg, "test")

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	schedulerURL := fmt.Sprintf("https://localhost:%d", port)
	testutil.WaitForHealthy(t, schedulerURL+"/status", 10*time.Second)

	// Verify status endpoint includes director_url
	resp, err := testutil.HTTPClient(5 * time.Second).Get(schedulerURL + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)

	cfg_, ok := status["config"].(map[string]interface{})
	require.True(t, ok, "Status should include config")
	assert.Equal(t, "http://director:8080", cfg_["director_url"], "Should include director_url")
	assert.Equal(t, "http://agent:9000", cfg_["agent_url"], "Should include agent_url")

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Shutdown(ctx)
}
