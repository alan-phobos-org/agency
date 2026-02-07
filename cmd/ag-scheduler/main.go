package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"phobos.org.uk/agency/internal/scheduler"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "Path to config file (required)")
	port := flag.Int("port", 0, "Port to listen on (overrides config)")
	bind := flag.String("bind", "", "Address to bind to (overrides config)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if *configPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -config flag is required\n")
		fmt.Fprintf(os.Stderr, "Usage: ag-scheduler -config <path>\n")
		os.Exit(1)
	}

	// Load config
	cfg, err := scheduler.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override port if specified
	if *port > 0 {
		cfg.Port = *port
	}
	// Override bind if specified
	if *bind != "" {
		cfg.Bind = *bind
	}
	if cfg.Bind != "127.0.0.1" && cfg.Bind != "localhost" && cfg.Bind != "::1" {
		fmt.Fprintf(os.Stderr, "Warning: scheduler bind=%q exposes unauthenticated endpoints. Prefer 127.0.0.1.\n", cfg.Bind)
	}

	// Parse config reload interval from environment (default: 60s, min: 1s)
	configReloadInterval := 60 * time.Second
	if intervalStr := os.Getenv("AG_SCHEDULER_CONFIG_RELOAD_INTERVAL"); intervalStr != "" {
		if parsed, err := time.ParseDuration(intervalStr); err == nil {
			if parsed < time.Second {
				fmt.Fprintf(os.Stderr, "Warning: AG_SCHEDULER_CONFIG_RELOAD_INTERVAL=%s is too small, using minimum 1s\n", intervalStr)
				configReloadInterval = time.Second
			} else {
				configReloadInterval = parsed
			}
		} else {
			fmt.Fprintf(os.Stderr, "Warning: Invalid AG_SCHEDULER_CONFIG_RELOAD_INTERVAL=%s, using default 60s: %v\n", intervalStr, err)
		}
	}

	// Create and start scheduler
	s := scheduler.New(cfg, *configPath, configReloadInterval, version)

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		os.Exit(0)
	}()

	if err := s.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
