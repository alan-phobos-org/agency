package web

import (
	"fmt"
	"os"

	"phobos.org.uk/agency/internal/tlsutil"
)

// TLSConfig holds TLS certificate configuration
type TLSConfig struct {
	CertFile     string
	KeyFile      string
	AutoGenerate bool
}

// EnsureTLSCert checks if certificates exist and generates them if AutoGenerate is true
func EnsureTLSCert(cfg TLSConfig) error {
	certExists := fileExists(cfg.CertFile)
	keyExists := fileExists(cfg.KeyFile)

	if certExists && keyExists {
		return nil
	}

	if !cfg.AutoGenerate {
		if !certExists {
			return fmt.Errorf("certificate file not found: %s", cfg.CertFile)
		}
		if !keyExists {
			return fmt.Errorf("key file not found: %s", cfg.KeyFile)
		}
	}

	return tlsutil.GenerateSelfSignedCert(cfg.CertFile, cfg.KeyFile, "Agency Web Director")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
