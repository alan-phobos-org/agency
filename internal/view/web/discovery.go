package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"phobos.org.uk/agency/internal/api"
)

// ComponentStatus represents the status of a discovered component
type ComponentStatus struct {
	URL           string           `json:"url"`
	Type          string           `json:"type"`                 // agent, director, helper, view
	Interfaces    []string         `json:"interfaces,omitempty"` // statusable, taskable, observable, configurable
	Version       string           `json:"version"`
	State         string           `json:"state"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	CurrentTask   *api.CurrentTask `json:"current_task,omitempty"`
	Config        interface{}      `json:"config,omitempty"`
	LastSeen      time.Time        `json:"last_seen"`
	FailCount     int              `json:"-"` // Internal: consecutive failures
}

// Discovery handles service discovery via port scanning
type Discovery struct {
	portStart       int
	portEnd         int
	refreshInterval time.Duration
	maxFailures     int

	mu         sync.RWMutex
	components map[string]*ComponentStatus // keyed by URL

	client   *http.Client
	cancel   context.CancelFunc
	doneCh   chan struct{}
	selfPort int // Port of this web director (to exclude from discovery)
}

// DiscoveryConfig holds discovery configuration
type DiscoveryConfig struct {
	PortStart       int
	PortEnd         int
	RefreshInterval time.Duration
	MaxFailures     int
	SelfPort        int
}

// NewDiscovery creates a new discovery service
func NewDiscovery(cfg DiscoveryConfig) *Discovery {
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = time.Second
	}
	if cfg.MaxFailures == 0 {
		cfg.MaxFailures = 3
	}
	return &Discovery{
		portStart:       cfg.PortStart,
		portEnd:         cfg.PortEnd,
		refreshInterval: cfg.RefreshInterval,
		maxFailures:     cfg.MaxFailures,
		selfPort:        cfg.SelfPort,
		components:      make(map[string]*ComponentStatus),
		client: &http.Client{
			Timeout: 500 * time.Millisecond,
		},
		doneCh: make(chan struct{}),
	}
}

// Start begins the discovery polling loop
func (d *Discovery) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()

	// Do initial scan immediately
	d.scan()

	ticker := time.NewTicker(d.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(d.doneCh)
			return
		case <-ticker.C:
			d.scan()
		}
	}
}

// Stop stops the discovery service
func (d *Discovery) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()

	if cancel != nil {
		cancel()
		<-d.doneCh
	}
}

// scan checks all ports in the range for components
func (d *Discovery) scan() {
	var wg sync.WaitGroup

	for port := d.portStart; port <= d.portEnd; port++ {
		// Skip self
		if port == d.selfPort {
			continue
		}

		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			d.checkPort(p)
		}(port)
	}

	wg.Wait()
}

// checkPort queries a single port for /status
func (d *Discovery) checkPort(port int) {
	url := fmt.Sprintf("http://localhost:%d", port)
	statusURL := url + "/status"

	resp, err := d.client.Get(statusURL)
	if err != nil {
		d.markFailed(url)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		d.markFailed(url)
		return
	}

	var status ComponentStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		d.markFailed(url)
		return
	}

	status.URL = url
	status.LastSeen = time.Now()
	status.FailCount = 0

	d.mu.Lock()
	d.components[url] = &status
	d.mu.Unlock()
}

// markFailed increments failure count and removes if threshold exceeded
func (d *Discovery) markFailed(url string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if comp, ok := d.components[url]; ok {
		comp.FailCount++
		if comp.FailCount >= d.maxFailures {
			delete(d.components, url)
		}
	}
}

// Agents returns all discovered agents
func (d *Discovery) Agents() []*ComponentStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var agents []*ComponentStatus
	for _, comp := range d.components {
		if comp.Type == "agent" {
			agents = append(agents, comp)
		}
	}
	return agents
}

// Directors returns all discovered directors
func (d *Discovery) Directors() []*ComponentStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var directors []*ComponentStatus
	for _, comp := range d.components {
		if comp.Type == "director" {
			directors = append(directors, comp)
		}
	}
	return directors
}

// AllComponents returns all discovered components
func (d *Discovery) AllComponents() []*ComponentStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var all []*ComponentStatus
	for _, comp := range d.components {
		all = append(all, comp)
	}
	return all
}

// GetComponent returns a specific component by URL
func (d *Discovery) GetComponent(url string) (*ComponentStatus, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	comp, ok := d.components[url]
	return comp, ok
}

func hasInterface(interfaces []string, target string) bool {
	for _, i := range interfaces {
		if i == target {
			return true
		}
	}
	return false
}

// Taskables returns all discovered components with the taskable interface
func (d *Discovery) Taskables() []*ComponentStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*ComponentStatus
	for _, comp := range d.components {
		if hasInterface(comp.Interfaces, "taskable") {
			result = append(result, comp)
		}
	}
	return result
}

// Observables returns all discovered components with the observable interface
func (d *Discovery) Observables() []*ComponentStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*ComponentStatus
	for _, comp := range d.components {
		if hasInterface(comp.Interfaces, "observable") {
			result = append(result, comp)
		}
	}
	return result
}
