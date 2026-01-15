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
	"phobos.org.uk/agency/internal/api"
)

// Config holds web director configuration
type Config struct {
	Port            int
	InternalPort    int    // Internal HTTP port for unauthenticated localhost API (optional)
	Bind            string // Address to bind to (default: 0.0.0.0)
	AuthStore       *AuthStore
	PortStart       int // Discovery port range start
	PortEnd         int // Discovery port range end
	RefreshInterval time.Duration
	TLS             TLSConfig
	AccessLogPath   string // Path for access log file (empty = no logging)
	ContextsPath    string // Path to contexts YAML file (optional)
}

// Director is the web director server
type Director struct {
	config         *Config
	version        string
	discovery      *Discovery
	handlers       *Handlers
	server         *http.Server
	internalServer *http.Server // Internal HTTP server (no auth)
	rateLimiter    *RateLimiter
	accessLogger   *AccessLogger
	authStore      *AuthStore
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
		cfg.PortEnd = 9009
	}

	discovery := NewDiscovery(DiscoveryConfig{
		PortStart:       cfg.PortStart,
		PortEnd:         cfg.PortEnd,
		RefreshInterval: cfg.RefreshInterval,
		MaxFailures:     3,
		SelfPort:        cfg.Port,
	})

	// Load contexts if path specified
	var contexts *ContextsConfig
	if cfg.ContextsPath != "" {
		var err error
		contexts, err = LoadContexts(cfg.ContextsPath)
		if err != nil {
			return nil, fmt.Errorf("loading contexts: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Loaded %d contexts from %s\n", len(contexts.Contexts), cfg.ContextsPath)
	}

	// Create rate limiter for auth protection
	rateLimiter := NewRateLimiter()

	// Create access logger if path configured
	var accessLogger *AccessLogger
	if cfg.AccessLogPath != "" {
		var err error
		accessLogger, err = NewAccessLogger(cfg.AccessLogPath)
		if err != nil {
			return nil, fmt.Errorf("creating access logger: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Access logging enabled: %s\n", cfg.AccessLogPath)
	}

	// Determine if we should use secure cookies (HTTPS)
	secureCookie := true // Always use secure cookies since we use HTTPS

	handlers, err := NewHandlers(discovery, version, contexts, cfg.AuthStore, rateLimiter, secureCookie)
	if err != nil {
		return nil, err
	}

	return &Director{
		config:       cfg,
		version:      version,
		discovery:    discovery,
		handlers:     handlers,
		rateLimiter:  rateLimiter,
		accessLogger: accessLogger,
		authStore:    cfg.AuthStore,
	}, nil
}

// Router returns the HTTP router
func (d *Director) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)

	// Public endpoints (no auth needed)
	r.Get("/status", d.handlers.HandleStatus) // Used by discovery
	r.Get("/login", d.handlers.HandleLoginPage)
	r.Post("/login", d.handlers.HandleLogin)
	r.Get("/pair", d.handlers.HandlePairPage)
	r.Post("/pair", d.handlers.HandlePair)

	// Protected routes with session middleware and rate limiting
	protected := r.Group(nil)
	protected.Use(SessionMiddlewareWithRateLimiter(d.authStore, d.accessLogger, d.rateLimiter))

	// Dashboard
	protected.Get("/", d.handlers.HandleDashboard)
	protected.Post("/logout", d.handlers.HandleLogout)

	// API endpoints
	protected.Route("/api", func(r chi.Router) {
		r.Get("/status", d.handlers.HandleStatus)
		r.Get("/dashboard", d.handlers.HandleDashboardData) // Consolidated endpoint with ETag
		r.Get("/agents", d.handlers.HandleAgents)
		r.Get("/directors", d.handlers.HandleDirectors)
		r.Get("/contexts", d.handlers.HandleContexts) // Available task contexts
		r.Post("/task", d.handlers.HandleTaskSubmit)
		r.Get("/task/{id}", func(w http.ResponseWriter, r *http.Request) {
			taskID := chi.URLParam(r, "id")
			d.handlers.HandleTaskStatus(w, r, taskID)
		})
		r.Get("/history/{id}", func(w http.ResponseWriter, r *http.Request) {
			taskID := chi.URLParam(r, "id")
			d.handlers.HandleTaskHistory(w, r, taskID)
		})
		// Session endpoints for global session tracking (task sessions)
		r.Get("/sessions", d.handlers.HandleSessions)
		r.Post("/sessions", d.handlers.HandleAddSessionTask)
		r.Put("/sessions/{sessionId}/tasks/{taskId}", func(w http.ResponseWriter, r *http.Request) {
			sessionID := chi.URLParam(r, "sessionId")
			taskID := chi.URLParam(r, "taskId")
			d.handlers.HandleUpdateSessionTask(w, r, sessionID, taskID)
		})
		r.Post("/sessions/{sessionId}/archive", func(w http.ResponseWriter, r *http.Request) {
			sessionID := chi.URLParam(r, "sessionId")
			d.handlers.HandleArchiveSession(w, r, sessionID)
		})
		// Device pairing and management
		r.Post("/pair/code", d.handlers.HandleGeneratePairingCode)
		r.Get("/devices", d.handlers.HandleListDevices)
		r.Delete("/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
			deviceID := chi.URLParam(r, "id")
			d.handlers.HandleRevokeDevice(w, r, deviceID)
		})
		// Scheduler job trigger (proxies to scheduler component)
		r.Post("/scheduler/trigger", func(w http.ResponseWriter, req *http.Request) {
			schedulerURL := req.URL.Query().Get("scheduler_url")
			jobName := req.URL.Query().Get("job")
			if schedulerURL == "" || jobName == "" {
				api.WriteError(w, http.StatusBadRequest, "validation_error", "scheduler_url and job query parameters are required")
				return
			}
			d.handlers.HandleTriggerJob(w, req, schedulerURL, jobName)
		})
	})

	return r
}

// InternalRouter returns the internal HTTP router (no authentication).
// This is used for service-to-service communication on localhost.
func (d *Director) InternalRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// Internal API endpoints (no auth required)
	r.Route("/api", func(r chi.Router) {
		r.Get("/status", d.handlers.HandleStatus)
		r.Post("/task", d.handlers.HandleTaskSubmit)
		r.Get("/task/{id}", func(w http.ResponseWriter, req *http.Request) {
			taskID := chi.URLParam(req, "id")
			d.handlers.HandleTaskStatus(w, req, taskID)
		})
		r.Get("/history/{id}", func(w http.ResponseWriter, req *http.Request) {
			taskID := chi.URLParam(req, "id")
			d.handlers.HandleTaskHistory(w, req, taskID)
		})
		r.Get("/sessions", d.handlers.HandleSessions)
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

	// Start internal HTTP server if port configured (localhost only, no auth)
	if d.config.InternalPort > 0 {
		internalAddr := fmt.Sprintf("127.0.0.1:%d", d.config.InternalPort)
		d.internalServer = &http.Server{
			Addr:    internalAddr,
			Handler: d.InternalRouter(),
		}
		go func() {
			fmt.Fprintf(os.Stderr, "Internal API starting on http://%s (localhost only, no auth)\n", internalAddr)
			if err := d.internalServer.ListenAndServe(); err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "Internal server error: %v\n", err)
			}
		}()
	}

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
	if d.accessLogger != nil {
		d.accessLogger.Close()
	}
	// Shutdown internal server first
	if d.internalServer != nil {
		d.internalServer.Shutdown(ctx)
	}
	if d.server != nil {
		return d.server.Shutdown(ctx)
	}
	return nil
}
