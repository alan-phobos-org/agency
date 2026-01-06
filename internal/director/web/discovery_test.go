package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
				"roles":          []string{"agent"},
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
	require.Contains(t, agents[0].Roles, "agent")
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
				"roles":          []string{"director"},
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
	require.Contains(t, directors[0].Roles, "director")

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
				"roles":   []string{"agent"},
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
			"roles":   []string{"agent"},
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
			"roles": []string{"agent"}, "state": "idle",
		})
	}))
	defer agent1.Close()

	agent2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"roles": []string{"agent"}, "state": "working",
		})
	}))
	defer agent2.Close()

	director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"roles": []string{"director"}, "state": "running",
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

func TestHasRole(t *testing.T) {
	tests := []struct {
		roles  []string
		target string
		want   bool
	}{
		{[]string{"agent"}, "agent", true},
		{[]string{"director"}, "agent", false},
		{[]string{"agent", "director"}, "director", true},
		{nil, "agent", false},
		{[]string{}, "agent", false},
	}

	for _, tt := range tests {
		got := hasRole(tt.roles, tt.target)
		require.Equal(t, tt.want, got, "hasRole(%v, %s)", tt.roles, tt.target)
	}
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
