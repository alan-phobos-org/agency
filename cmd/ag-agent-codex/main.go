package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"phobos.org.uk/agency/internal/agent"
	"phobos.org.uk/agency/internal/config"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "Path to config file")
	port := flag.Int("port", 0, "Port to listen on (overrides config)")
	bind := flag.String("bind", "", "Address to bind to (overrides config)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Load config
	var cfg *config.Config
	var err error

	if *configPath != "" {
		cfg, err = config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg = config.Default()
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
		fmt.Fprintf(os.Stderr, "Warning: agent bind=%q exposes unauthenticated endpoints. Prefer 127.0.0.1.\n", cfg.Bind)
	}

	// Create and start agent
	a := agent.NewWithRunner(cfg, version, agent.NewCodexRunner())

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a.Shutdown(ctx)
		os.Exit(0)
	}()

	if err := a.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
