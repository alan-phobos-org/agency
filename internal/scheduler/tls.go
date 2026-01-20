package scheduler

import (
	"crypto/tls"
	"os"

	"phobos.org.uk/agency/internal/tlsutil"
)

// ensureTLSCert checks if certificates exist and generates them if needed
func ensureTLSCert(certPath, keyPath string) error {
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)

	if certExists && keyExists {
		return nil
	}

	return tlsutil.GenerateSelfSignedCert(certPath, keyPath, "Agency Scheduler")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getTLSConfig returns a TLS config with reasonable defaults
func getTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}
