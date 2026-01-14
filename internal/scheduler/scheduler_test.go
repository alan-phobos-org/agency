package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCron(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expr     string
		wantErr  bool
		validate func(*testing.T, *CronExpr)
	}{
		{
			name: "every minute",
			expr: "* * * * *",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Len(t, c.Minutes, 60)
				assert.Len(t, c.Hours, 24)
			},
		},
		{
			name: "specific time",
			expr: "0 1 * * *",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Equal(t, []int{0}, c.Minutes)
				assert.Equal(t, []int{1}, c.Hours)
			},
		},
		{
			name: "step expression",
			expr: "*/15 * * * *",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Equal(t, []int{0, 15, 30, 45}, c.Minutes)
			},
		},
		{
			name: "range",
			expr: "0-5 * * * *",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, c.Minutes)
			},
		},
		{
			name: "comma-separated",
			expr: "0,30 * * * *",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Equal(t, []int{0, 30}, c.Minutes)
			},
		},
		{
			name: "complex",
			expr: "0 2 * * 0",
			validate: func(t *testing.T, c *CronExpr) {
				assert.Equal(t, []int{0}, c.Minutes)
				assert.Equal(t, []int{2}, c.Hours)
				assert.Equal(t, []int{0}, c.DaysOfWeek) // Sunday
			},
		},
		{
			name:    "invalid - wrong field count",
			expr:    "* * *",
			wantErr: true,
		},
		{
			name:    "invalid - out of range",
			expr:    "60 * * * *",
			wantErr: true,
		},
		{
			name:    "invalid - bad step",
			expr:    "*/0 * * * *",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cron, err := ParseCron(tt.expr)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, cron)
			}
		})
	}
}

func TestCronNext(t *testing.T) {
	t.Parallel()

	// Test next run at 1:00 AM
	cron, err := ParseCron("0 1 * * *")
	require.NoError(t, err)

	// From 00:30, next should be 01:00 same day
	from := time.Date(2025, 1, 15, 0, 30, 0, 0, time.UTC)
	next := cron.Next(from)
	assert.Equal(t, 1, next.Hour())
	assert.Equal(t, 0, next.Minute())
	assert.Equal(t, 15, next.Day())

	// From 01:30, next should be 01:00 next day
	from = time.Date(2025, 1, 15, 1, 30, 0, 0, time.UTC)
	next = cron.Next(from)
	assert.Equal(t, 1, next.Hour())
	assert.Equal(t, 0, next.Minute())
	assert.Equal(t, 16, next.Day())
}

func TestCronNextEvery15Min(t *testing.T) {
	t.Parallel()

	cron, err := ParseCron("*/15 * * * *")
	require.NoError(t, err)

	from := time.Date(2025, 1, 15, 10, 7, 0, 0, time.UTC)
	next := cron.Next(from)
	assert.Equal(t, 10, next.Hour())
	assert.Equal(t, 15, next.Minute())
}

func TestCronWeekday(t *testing.T) {
	t.Parallel()

	// Sunday at 2am
	cron, err := ParseCron("0 2 * * 0")
	require.NoError(t, err)

	// Wednesday Jan 15 2025
	from := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	next := cron.Next(from)

	// Should be Sunday Jan 19 2025
	assert.Equal(t, time.Sunday, next.Weekday())
	assert.Equal(t, 2, next.Hour())
	assert.Equal(t, 19, next.Day())
}

func TestConfigParse(t *testing.T) {
	t.Parallel()

	yaml := `
port: 9100
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test prompt"
    model: opus
    timeout: 1h
`
	cfg, err := Parse([]byte(yaml))
	require.NoError(t, err)

	assert.Equal(t, 9100, cfg.Port)
	assert.Equal(t, "http://localhost:9000", cfg.AgentURL)
	assert.Len(t, cfg.Jobs, 1)
	assert.Equal(t, "test-job", cfg.Jobs[0].Name)
	assert.Equal(t, "opus", cfg.Jobs[0].Model)
	assert.Equal(t, time.Hour, cfg.Jobs[0].Timeout)
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "valid",
			yaml: `
jobs:
  - name: test
    schedule: "0 1 * * *"
    prompt: "test"
`,
		},
		{
			name: "no jobs",
			yaml: `
port: 9100
`,
			wantErr: "at least one job is required",
		},
		{
			name: "missing name",
			yaml: `
jobs:
  - schedule: "0 1 * * *"
    prompt: "test"
`,
			wantErr: "name is required",
		},
		{
			name: "duplicate name",
			yaml: `
jobs:
  - name: test
    schedule: "0 1 * * *"
    prompt: "test"
  - name: test
    schedule: "0 2 * * *"
    prompt: "test2"
`,
			wantErr: "duplicate name",
		},
		{
			name: "invalid schedule",
			yaml: `
jobs:
  - name: test
    schedule: "bad"
    prompt: "test"
`,
			wantErr: "invalid schedule",
		},
		{
			name: "invalid model",
			yaml: `
jobs:
  - name: test
    schedule: "0 1 * * *"
    prompt: "test"
    model: invalid
`,
			wantErr: "model must be opus, sonnet, or haiku",
		},
		{
			name: "missing prompt",
			yaml: `
jobs:
  - name: test
    schedule: "0 1 * * *"
`,
			wantErr: "prompt is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	yaml := `
jobs:
  - name: test
    schedule: "0 1 * * *"
    prompt: "test"
`
	cfg, err := Parse([]byte(yaml))
	require.NoError(t, err)

	assert.Equal(t, DefaultPort, cfg.Port)
	assert.Equal(t, DefaultLogLevel, cfg.LogLevel)
	assert.Equal(t, DefaultAgentURL, cfg.AgentURL)

	// Job defaults
	assert.Equal(t, DefaultModel, cfg.GetModel(&cfg.Jobs[0]))
	assert.Equal(t, DefaultTimeout, cfg.GetTimeout(&cfg.Jobs[0]))
	assert.Equal(t, DefaultAgentURL, cfg.GetAgentURL(&cfg.Jobs[0]))
}

func TestConfigJobOverrides(t *testing.T) {
	t.Parallel()

	yaml := `
agent_url: http://default:9000
jobs:
  - name: test
    schedule: "0 1 * * *"
    prompt: "test"
    agent_url: http://custom:9000
    model: opus
    timeout: 2h
`
	cfg, err := Parse([]byte(yaml))
	require.NoError(t, err)

	job := &cfg.Jobs[0]
	assert.Equal(t, "http://custom:9000", cfg.GetAgentURL(job))
	assert.Equal(t, "opus", cfg.GetModel(job))
	assert.Equal(t, 2*time.Hour, cfg.GetTimeout(job))
}

func TestSchedulerStatus(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Port:     0, // Will be overridden
		AgentURL: "http://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test-version")

	// Initialize job states manually for status test
	s.mu.Lock()
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	s.jobs = []*jobState{{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: cron.Next(time.Now()),
	}}
	s.running = true
	s.mu.Unlock()

	// Test status handler
	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "helper", resp["type"])
	assert.Equal(t, "test-version", resp["version"])
	assert.Equal(t, "running", resp["state"])

	jobs, ok := resp["jobs"].([]interface{})
	require.True(t, ok)
	assert.Len(t, jobs, 1)

	job := jobs[0].(map[string]interface{})
	assert.Equal(t, "test-job", job["name"])
	assert.Equal(t, "0 1 * * *", job["schedule"])
}

func TestSchedulerJobSubmission(t *testing.T) {
	t.Parallel()

	// Create mock agent
	submitted := make(chan map[string]interface{}, 1)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			submitted <- req

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	cfg := &Config{
		Port:     0,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
				Model:    "opus",
				Timeout:  time.Hour,
			},
		},
	}

	s := New(cfg, "test")

	// Initialize job state
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	// Run job
	s.runJob(js)

	// Verify submission
	select {
	case req := <-submitted:
		assert.Equal(t, "Test prompt", req["prompt"])
		assert.Equal(t, "opus", req["model"])
		assert.Equal(t, float64(3600), req["timeout_seconds"])
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for job submission")
	}

	// Verify state update
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-123", js.LastTaskID)
	assert.False(t, js.LastRun.IsZero())
}

func TestSchedulerJobAgentBusy(t *testing.T) {
	t.Parallel()

	// Create mock agent that returns 409
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "agent_busy",
		})
	}))
	defer agent.Close()

	cfg := &Config{
		Port:     0,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	assert.Equal(t, "skipped_busy", js.LastStatus)
	assert.Empty(t, js.LastTaskID)
}

func TestSchedulerJobAgentError(t *testing.T) {
	t.Parallel()

	// Create mock agent that returns error
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer agent.Close()

	cfg := &Config{
		Port:     0,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	assert.Equal(t, "skipped_error", js.LastStatus)
}

func TestSchedulerShutdown(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Port:     0,
		AgentURL: "http://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	// Start in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Wait for start
	time.Sleep(100 * time.Millisecond)

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.Shutdown(ctx)
	require.NoError(t, err)

	// Verify stopped
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestSchedulerStartTwice(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Port:     0,
		AgentURL: "http://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	// Start in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	// Wait for start
	time.Sleep(100 * time.Millisecond)

	// Try to start again - should error
	err := s.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Shutdown(ctx)
	<-errCh
}

func TestConfigLoadFileNotFound(t *testing.T) {
	t.Parallel()

	_, err := Load("/nonexistent/path/to/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestSchedulerJobNoDoubleRun(t *testing.T) {
	t.Parallel()

	// Create mock agent with delay to simulate slow response
	var submissions int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			// Simulate slow agent
			time.Sleep(50 * time.Millisecond)
			count := submissions
			submissions++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-" + string(rune('0'+count)),
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	cfg := &Config{
		Port:     0,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "* * * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	// Initialize job state with NextRun in the past
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: time.Now().Add(-time.Minute),
	}
	s.jobs = []*jobState{js}

	// Call checkAndRunJobs twice in quick succession
	// The second call should not trigger another run because isRunning is true
	now := time.Now()
	go s.checkAndRunJobs(now)
	time.Sleep(10 * time.Millisecond) // Let first call start
	s.checkAndRunJobs(now)

	// Wait for job to complete
	time.Sleep(100 * time.Millisecond)

	// Should only have submitted once despite two checkAndRunJobs calls
	assert.Equal(t, int32(1), submissions)
}

func TestSchedulerDirectorRouting(t *testing.T) {
	t.Parallel()

	// Create mock director that accepts tasks
	directorCalled := false
	var receivedReq map[string]interface{}
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/task" && r.Method == "POST" {
			directorCalled = true
			json.NewDecoder(r.Body).Decode(&receivedReq)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-dir-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer director.Close()

	// Create mock agent (should not be called when director succeeds)
	agentCalled := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agentCalled = true
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"task_id": "task-agent-123"})
	}))
	defer agent.Close()

	cfg := &Config{
		Port:        0,
		DirectorURL: director.URL,
		AgentURL:    agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
				Model:    "opus",
				Timeout:  time.Hour,
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	// Should have called director, not agent
	assert.True(t, directorCalled, "Director should have been called")
	assert.False(t, agentCalled, "Agent should not have been called when director succeeds")

	// Verify request format
	assert.Equal(t, agent.URL, receivedReq["agent_url"])
	assert.Equal(t, "Test prompt", receivedReq["prompt"])
	assert.Equal(t, "opus", receivedReq["model"])
	assert.Equal(t, float64(3600), receivedReq["timeout_seconds"])
	assert.Equal(t, "scheduler", receivedReq["source"])
	assert.Equal(t, "test-job", receivedReq["source_job"])

	// Verify state
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-dir-123", js.LastTaskID)
}

func TestSchedulerDirectorFallbackToAgent(t *testing.T) {
	t.Parallel()

	// Create mock director that fails
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer director.Close()

	// Create mock agent that succeeds
	agentCalled := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-fallback-123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	cfg := &Config{
		Port:        0,
		DirectorURL: director.URL,
		AgentURL:    agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	// Should have fallen back to agent
	assert.True(t, agentCalled, "Agent should have been called as fallback")
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-fallback-123", js.LastTaskID)
}

func TestSchedulerDirectorUnavailable(t *testing.T) {
	t.Parallel()

	// Use a URL that won't connect
	cfg := &Config{
		Port:        0,
		DirectorURL: "http://localhost:59999", // Won't connect
		AgentURL:    "http://localhost:59998", // Also won't connect
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	// Should have failed with error status
	assert.Equal(t, "skipped_error", js.LastStatus)
	assert.Empty(t, js.LastTaskID)
}

func TestSchedulerNoDirectorConfigured(t *testing.T) {
	t.Parallel()

	// Create mock agent
	agentCalled := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" && r.Method == "POST" {
			agentCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"task_id": "task-agent-only",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	// No DirectorURL configured
	cfg := &Config{
		Port:     0,
		AgentURL: agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	// Should go directly to agent
	assert.True(t, agentCalled, "Agent should have been called directly")
	assert.Equal(t, "submitted", js.LastStatus)
	assert.Equal(t, "task-agent-only", js.LastTaskID)
}

func TestSchedulerDirectorAgentBusy(t *testing.T) {
	t.Parallel()

	// Create mock director that returns agent busy
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/task" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "agent_busy"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer director.Close()

	// Create mock agent (should be called as fallback)
	agentCalled := false
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/task" {
			agentCalled = true
			// Agent is also busy
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "agent_busy"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	cfg := &Config{
		Port:        0,
		DirectorURL: director.URL,
		AgentURL:    agent.URL,
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test")

	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	js := &jobState{
		Job:  &cfg.Jobs[0],
		Cron: cron,
	}
	s.jobs = []*jobState{js}

	s.runJob(js)

	// Director returned busy, so fallback to agent which is also busy
	assert.True(t, agentCalled, "Agent should have been called as fallback")
	assert.Equal(t, "skipped_busy", js.LastStatus)
}

func TestConfigDirectorURL(t *testing.T) {
	t.Parallel()

	yaml := `
port: 9100
director_url: https://localhost:8443
agent_url: http://localhost:9000
jobs:
  - name: test-job
    schedule: "0 1 * * *"
    prompt: "Test prompt"
`
	cfg, err := Parse([]byte(yaml))
	require.NoError(t, err)

	assert.Equal(t, "https://localhost:8443", cfg.DirectorURL)
	assert.Equal(t, "http://localhost:9000", cfg.AgentURL)
}

func TestSchedulerStatusWithDirectorURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Port:        0,
		DirectorURL: "https://localhost:8443",
		AgentURL:    "http://localhost:9000",
		Jobs: []Job{
			{
				Name:     "test-job",
				Schedule: "0 1 * * *",
				Prompt:   "Test prompt",
			},
		},
	}

	s := New(cfg, "test-version")

	// Initialize job states manually for status test
	s.mu.Lock()
	cron, _ := ParseCron(cfg.Jobs[0].Schedule)
	s.jobs = []*jobState{{
		Job:     &cfg.Jobs[0],
		Cron:    cron,
		NextRun: cron.Next(time.Now()),
	}}
	s.running = true
	s.mu.Unlock()

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	config := resp["config"].(map[string]interface{})
	assert.Equal(t, "http://localhost:9000", config["agent_url"])
	assert.Equal(t, "https://localhost:8443", config["director_url"])
}
