package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phobos.org.uk/agency/internal/view/web"
)

var version = "dev"

func main() {
	port := flag.Int("port", 8443, "Port to listen on")
	internalPort := flag.Int("internal-port", 0, "Internal HTTP port for unauthenticated localhost API (0=disabled)")
	bind := flag.String("bind", "0.0.0.0", "Address to bind to")
	portStart := flag.Int("port-start", 9000, "Discovery port range start")
	portEnd := flag.Int("port-end", 9010, "Discovery port range end")
	envFile := flag.String("env", "", "Path to .env file for token (default: .env in current dir)")
	certFile := flag.String("cert", "", "Path to TLS certificate")
	keyFile := flag.String("key", "", "Path to TLS private key")
	accessLog := flag.String("access-log", "", "Path to access log file (logs all connection attempts)")
	contextsFile := flag.String("contexts", "", "Path to contexts YAML file for predefined task settings")
	regenCert := flag.Bool("regen-cert", false, "Regenerate self-signed certificate")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Determine cert paths
	agencyRoot := os.Getenv("AGENCY_ROOT")
	if agencyRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		agencyRoot = filepath.Join(home, ".agency")
	}

	certPath := *certFile
	keyPath := *keyFile
	if certPath == "" {
		certPath = filepath.Join(agencyRoot, "web-director", "cert.pem")
	}
	if keyPath == "" {
		keyPath = filepath.Join(agencyRoot, "web-director", "key.pem")
	}

	// Handle cert regeneration
	if *regenCert {
		os.Remove(certPath)
		os.Remove(keyPath)
		fmt.Println("Certificates will be regenerated on startup")
	}

	// Load password from env file
	password := os.Getenv("AG_WEB_PASSWORD")
	if password == "" {
		envPath := *envFile
		if envPath == "" {
			envPath = ".env"
		}
		password = loadEnvPassword(envPath)
	}

	if password == "" {
		fmt.Fprintf(os.Stderr, "Error: AG_WEB_PASSWORD is required.\n")
		fmt.Fprintf(os.Stderr, "Set AG_WEB_PASSWORD in environment or .env file.\n")
		os.Exit(1)
	}

	// Create auth store
	authStorePath := filepath.Join(agencyRoot, "auth-sessions.json")
	authStore, err := web.NewAuthStore(authStorePath, password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating auth store: %v\n", err)
		os.Exit(1)
	}

	cfg := &web.Config{
		Port:            *port,
		InternalPort:    *internalPort,
		Bind:            *bind,
		AuthStore:       authStore,
		PortStart:       *portStart,
		PortEnd:         *portEnd,
		RefreshInterval: time.Second,
		AccessLogPath:   *accessLog,
		ContextsPath:    *contextsFile,
		TLS: web.TLSConfig{
			CertFile:     certPath,
			KeyFile:      keyPath,
			AutoGenerate: true,
		},
	}

	d, err := web.New(cfg, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating director: %v\n", err)
		os.Exit(1)
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		d.Shutdown(ctx)
		os.Exit(0)
	}()

	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// loadEnvPassword reads AG_WEB_PASSWORD from a .env file
func loadEnvPassword(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "AG_WEB_PASSWORD=") {
			return strings.TrimPrefix(line, "AG_WEB_PASSWORD=")
		}
	}
	return ""
}
