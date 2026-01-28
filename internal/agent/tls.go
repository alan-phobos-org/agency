package agent

import (
	"crypto/tls"

	"phobos.org.uk/agency/internal/tlsutil"
)

// ensureTLSCert checks if certificates exist and generates them if needed
func ensureTLSCert(certPath, keyPath string) error {
	return tlsutil.EnsureTLSCert(certPath, keyPath, "Agency Agent")
}

// getTLSConfig returns a TLS config with reasonable defaults
func getTLSConfig() *tls.Config {
	return tlsutil.DefaultTLSConfig()
}
