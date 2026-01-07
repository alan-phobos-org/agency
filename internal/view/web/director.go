package web

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Config holds web director configuration
type Config struct {
	Port            int
	Bind            string // Address to bind to (default: 0.0.0.0)
	Token           string // Auth token (empty = no auth)
	PortStart       int    // Discovery port range start
	PortEnd         int    // Discovery port range end
	RefreshInterval time.Duration
	TLS             TLSConfig
}

// Director is the web director server
type Director struct {
	config    *Config
	version   string
	discovery *Discovery
	handlers  *Handlers
	server    *http.Server
}

// New creates a new web director
func New(cfg *Config, version string) (*Director, error) {
	// Set defaults
	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0"
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = time.Second
	}
	if cfg.PortStart == 0 {
		cfg.PortStart = 9000
	}
	if cfg.PortEnd == 0 {
		cfg.PortEnd = 9199
	}

	discovery := NewDiscovery(DiscoveryConfig{
		PortStart:       cfg.PortStart,
		PortEnd:         cfg.PortEnd,
		RefreshInterval: cfg.RefreshInterval,
		MaxFailures:     3,
		SelfPort:        cfg.Port,
	})

	handlers, err := NewHandlers(discovery, version)
	if err != nil {
		return nil, err
	}

	return &Director{
		config:    cfg,
		version:   version,
		discovery: discovery,
		handlers:  handlers,
	}, nil
}

// Router returns the HTTP router
func (d *Director) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	// Universal endpoint (no auth needed for /status - used by discovery)
	r.Get("/status", d.handlers.HandleStatus)

	// Protected routes
	protected := r.Group(nil)
	if d.config.Token != "" {
		protected.Use(AuthMiddleware(d.config.Token))
	}

	// Dashboard
	protected.Get("/", d.handlers.HandleDashboard)

	// API endpoints
	protected.Route("/api", func(r chi.Router) {
		r.Get("/status", d.handlers.HandleStatus)
		r.Get("/agents", d.handlers.HandleAgents)
		r.Get("/directors", d.handlers.HandleDirectors)
		r.Post("/task", d.handlers.HandleTaskSubmit)
		r.Get("/task/{id}", func(w http.ResponseWriter, r *http.Request) {
			taskID := chi.URLParam(r, "id")
			d.handlers.HandleTaskStatus(w, r, taskID)
		})
	})

	return r
}

// Start starts the web director server
func (d *Director) Start() error {
	addr := fmt.Sprintf("%s:%d", d.config.Bind, d.config.Port)

	d.server = &http.Server{
		Addr:    addr,
		Handler: d.Router(),
	}

	// Start discovery in background
	go d.discovery.Start(context.Background())

	// Setup TLS
	if err := EnsureTLSCert(d.config.TLS); err != nil {
		return fmt.Errorf("setting up TLS: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Web director starting on https://%s\n", addr)
	fmt.Fprintf(os.Stderr, "Discovery scanning ports %d-%d\n", d.config.PortStart, d.config.PortEnd)

	// Configure TLS
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	d.server.TLSConfig = tlsCfg

	return d.server.ListenAndServeTLS(d.config.TLS.CertFile, d.config.TLS.KeyFile)
}

// Shutdown gracefully shuts down the director
func (d *Director) Shutdown(ctx context.Context) error {
	d.discovery.Stop()
	if d.server != nil {
		return d.server.Shutdown(ctx)
	}
	return nil
}
