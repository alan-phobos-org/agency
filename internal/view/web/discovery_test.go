package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryAgentClassification(t *testing.T) {
	t.Parallel()

	// Create a mock agent server
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":           "agent",
				"interfaces":     []string{"statusable", "taskable"},
				"version":        "test-1.0",
				"state":          "idle",
				"uptime_seconds": 100,
			})
		}
	}))
	defer agent.Close()

	// Extract port from URL
	port := extractPort(t, agent.URL)

	d := NewDiscovery(DiscoveryConfig{
		PortStart:       port,
		PortEnd:         port,
		RefreshInterval: 100 * time.Millisecond,
		MaxFailures:     3,
	})

	// Do a single scan
	d.scan()

	agents := d.Agents()
	require.Len(t, agents, 1)
	require.Equal(t, "agent", agents[0].Type)
	require.Equal(t, "idle", agents[0].State)
	require.Equal(t, "test-1.0", agents[0].Version)

	directors := d.Directors()
	require.Len(t, directors, 0)
}

func TestDiscoveryDirectorClassification(t *testing.T) {
	t.Parallel()

	// Create a mock director server
	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":           "director",
				"interfaces":     []string{"statusable", "observable", "taskable"},
				"version":        "test-1.0",
				"state":          "running",
				"uptime_seconds": 50,
			})
		}
	}))
	defer director.Close()

	port := extractPort(t, director.URL)

	d := NewDiscovery(DiscoveryConfig{
		PortStart: port,
		PortEnd:   port,
	})

	d.scan()

	directors := d.Directors()
	require.Len(t, directors, 1)
	require.Equal(t, "director", directors[0].Type)

	agents := d.Agents()
	require.Len(t, agents, 0)
}

func TestDiscoveryFailureRemoval(t *testing.T) {
	t.Parallel()

	// Create a server that will be shut down
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":    "agent",
				"version": "test",
				"state":   "idle",
			})
		} else {
			// Simulate failure
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	port := extractPort(t, server.URL)

	d := NewDiscovery(DiscoveryConfig{
		PortStart:   port,
		PortEnd:     port,
		MaxFailures: 2,
	})

	// First scan - should find agent
	d.scan()
	require.Len(t, d.Agents(), 1)

	// Second scan - first failure
	d.scan()
	require.Len(t, d.Agents(), 1) // Still there, 1 failure

	// Third scan - second failure (threshold reached)
	d.scan()
	require.Len(t, d.Agents(), 0) // Removed

	server.Close()
}

func TestDiscoveryStartStop(t *testing.T) {
	t.Parallel()

	d := NewDiscovery(DiscoveryConfig{
		PortStart:       50000,
		PortEnd:         50000,
		RefreshInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	go func() {
		close(started)
		d.Start(ctx)
	}()

	<-started
	time.Sleep(100 * time.Millisecond)

	cancel()
	d.Stop()
	// Should not hang
}

func TestDiscoveryExcludesSelf(t *testing.T) {
	t.Parallel()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":    "agent",
			"version": "test",
			"state":   "idle",
		})
	}))
	defer server.Close()

	port := extractPort(t, server.URL)

	// Discovery with SelfPort matching server port - should exclude
	d := NewDiscovery(DiscoveryConfig{
		PortStart: port,
		PortEnd:   port,
		SelfPort:  port,
	})

	d.scan()
	require.Len(t, d.Agents(), 0, "Should exclude self port")

	// Discovery without SelfPort - should find
	d2 := NewDiscovery(DiscoveryConfig{
		PortStart: port,
		PortEnd:   port,
	})

	d2.scan()
	require.Len(t, d2.Agents(), 1, "Should find agent when not self")
}

func TestDiscoveryMultipleComponents(t *testing.T) {
	t.Parallel()

	// Create multiple servers
	agent1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "agent", "state": "idle",
		})
	}))
	defer agent1.Close()

	agent2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "agent", "state": "working",
		})
	}))
	defer agent2.Close()

	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type": "director", "state": "running",
		})
	}))
	defer director.Close()

	port1 := extractPort(t, agent1.URL)
	port2 := extractPort(t, agent2.URL)
	port3 := extractPort(t, director.URL)

	// Instead of scanning a port range (which may include other processes),
	// directly add the components to the discovery cache
	d := NewDiscovery(DiscoveryConfig{
		PortStart: 50000,
		PortEnd:   50000,
	})

	// Check each specific port
	d.checkPort(port1)
	d.checkPort(port2)
	d.checkPort(port3)

	all := d.AllComponents()
	require.Len(t, all, 3)

	agents := d.Agents()
	require.Len(t, agents, 2)

	directors := d.Directors()
	require.Len(t, directors, 1)
}

func extractPort(t *testing.T, url string) int {
	t.Helper()
	// URL format: http://127.0.0.1:PORT
	parts := strings.Split(url, ":")
	require.Len(t, parts, 3)

	// Remove any path suffix
	portStr := strings.Split(parts[2], "/")[0]

	var port int
	for _, c := range portStr {
		if c >= '0' && c <= '9' {
			port = port*10 + int(c-'0')
		}
	}
	return port
}

func TestDiscoveryHelperWithJobs(t *testing.T) {
	t.Parallel()

	// Track job status that can be updated
	var mu sync.Mutex
	jobLastStatus := ""
	jobLastTaskID := ""

	// Create a mock helper server that returns jobs
	helper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			mu.Lock()
			status := jobLastStatus
			taskID := jobLastTaskID
			mu.Unlock()

			response := map[string]interface{}{
				"type":           "helper",
				"interfaces":     []string{"statusable", "observable"},
				"version":        "test-scheduler-1.0",
				"state":          "running",
				"uptime_seconds": 100,
				"jobs": []map[string]interface{}{
					{
						"name":         "test-job",
						"schedule":     "0 * * * *",
						"next_run":     time.Now().Add(time.Hour).Format(time.RFC3339),
						"last_status":  status,
						"last_task_id": taskID,
					},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer helper.Close()

	port := extractPort(t, helper.URL)

	d := NewDiscovery(DiscoveryConfig{
		PortStart:       port,
		PortEnd:         port,
		RefreshInterval: 100 * time.Millisecond,
		MaxFailures:     3,
	})

	// Initial scan - should find helper with jobs
	d.scan()

	helpers := d.Helpers()
	require.Len(t, helpers, 1)
	require.Equal(t, "helper", helpers[0].Type)
	require.Equal(t, "running", helpers[0].State)
	require.Len(t, helpers[0].Jobs, 1)
	require.Equal(t, "test-job", helpers[0].Jobs[0].Name)
	require.Equal(t, "0 * * * *", helpers[0].Jobs[0].Schedule)
	require.Empty(t, helpers[0].Jobs[0].LastStatus)

	// Update job status (simulating job execution)
	mu.Lock()
	jobLastStatus = "submitted"
	jobLastTaskID = "task-123"
	mu.Unlock()

	// Scan again - should reflect updated status
	d.scan()

	helpers = d.Helpers()
	require.Len(t, helpers, 1)
	require.Len(t, helpers[0].Jobs, 1)
	require.Equal(t, "submitted", helpers[0].Jobs[0].LastStatus)
	require.Equal(t, "task-123", helpers[0].Jobs[0].LastTaskID)
}

func TestDiscoveryHelperJobStatusUpdates(t *testing.T) {
	t.Parallel()

	// This test verifies that job status updates trigger data changes
	// which would cause the ETag to change in the dashboard API

	var mu sync.Mutex
	jobStatus := "pending"
	nextRunTime := time.Now().Add(5 * time.Minute)

	helper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			mu.Lock()
			status := jobStatus
			nextRun := nextRunTime
			mu.Unlock()

			response := map[string]interface{}{
				"type":       "helper",
				"interfaces": []string{"statusable"},
				"version":    "v1",
				"state":      "running",
				"jobs": []map[string]interface{}{
					{
						"name":        "cron-job",
						"schedule":    "*/5 * * * *",
						"next_run":    nextRun.Format(time.RFC3339),
						"last_status": status,
					},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer helper.Close()

	port := extractPort(t, helper.URL)

	d := NewDiscovery(DiscoveryConfig{
		PortStart: port,
		PortEnd:   port,
	})

	// First scan
	d.scan()
	helpers := d.Helpers()
	require.Len(t, helpers, 1)
	initialJob := helpers[0].Jobs[0]
	require.Equal(t, "pending", initialJob.LastStatus)

	// Update the job status
	mu.Lock()
	jobStatus = "queued"
	nextRunTime = time.Now().Add(10 * time.Minute)
	mu.Unlock()

	// Rescan
	d.scan()
	helpers = d.Helpers()
	require.Len(t, helpers, 1)
	updatedJob := helpers[0].Jobs[0]

	// Verify status changed
	require.Equal(t, "queued", updatedJob.LastStatus)

	// Verify next_run changed
	require.True(t, updatedJob.NextRun.After(initialJob.NextRun),
		"NextRun should have been updated: initial=%v, updated=%v",
		initialJob.NextRun, updatedJob.NextRun)
}
