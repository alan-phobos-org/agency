package tlsutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateSelfSignedCert generates a self-signed certificate for localhost.
func GenerateSelfSignedCert(certPath, keyPath, organization string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generating private key: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{organization},
			CommonName:   hostname,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", hostname},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("creating certificate: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return fmt.Errorf("creating cert directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("opening cert file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return fmt.Errorf("encoding certificate: %w", err)
	}

	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("opening key file: %w", err)
	}
	defer keyFile.Close()

	keyDER := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}); err != nil {
		return fmt.Errorf("encoding private key: %w", err)
	}

	return nil
}

// FileExists returns true if a file exists at the given path.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnsureTLSCert checks if certificates exist and generates them if needed.
func EnsureTLSCert(certPath, keyPath, organization string) error {
	if FileExists(certPath) && FileExists(keyPath) {
		return nil
	}
	return GenerateSelfSignedCert(certPath, keyPath, organization)
}

// DefaultTLSConfig returns a TLS config with reasonable defaults.
func DefaultTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type loopbackTLSBypassTransport struct {
	secure        http.RoundTripper
	insecure      http.RoundTripper
	insecureAll   bool
	insecureHosts map[string]struct{} // hostnames or IPs (as in URL.Hostname())
}

func (t *loopbackTLSBypassTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.insecureAll {
		return t.insecure.RoundTrip(req)
	}
	if req == nil || req.URL == nil {
		return t.secure.RoundTrip(req)
	}

	if req.URL.Scheme == "https" {
		host := req.URL.Hostname()
		if _, ok := t.insecureHosts[host]; ok || isLoopbackHost(host) {
			return t.insecure.RoundTrip(req)
		}
	}
	return t.secure.RoundTrip(req)
}

func cloneDefaultTransport() *http.Transport {
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		return dt.Clone()
	}
	// Extremely defensive fallback.
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
}

// NewHTTPClient creates an HTTP client that:
// - Uses normal TLS verification by default
// - Allows self-signed TLS for loopback HTTPS targets (localhost/127.0.0.1/::1)
//
// To force-disable TLS verification for all HTTPS (not recommended), set
// AGENCY_TLS_INSECURE=1.
// To whitelist additional hosts for self-signed TLS (not recommended), set
// AGENCY_TLS_INSECURE_HOSTS to a comma-separated list of hostnames/IPs.
func NewHTTPClient(timeout time.Duration, _ ...string) *http.Client {
	secure := cloneDefaultTransport()
	secure.TLSClientConfig = DefaultTLSConfig()

	insecure := cloneDefaultTransport()
	insecureTLS := DefaultTLSConfig()
	insecureTLS.InsecureSkipVerify = true
	insecure.TLSClientConfig = insecureTLS

	insecureHosts := map[string]struct{}{}
	if raw := os.Getenv("AGENCY_TLS_INSECURE_HOSTS"); raw != "" {
		for _, host := range strings.Split(raw, ",") {
			host = strings.TrimSpace(host)
			if host == "" {
				continue
			}
			insecureHosts[host] = struct{}{}
		}
	}

	insecureAll := os.Getenv("AGENCY_TLS_INSECURE") == "1"
	client := &http.Client{
		Timeout: timeout,
		Transport: &loopbackTLSBypassTransport{
			secure:        secure,
			insecure:      insecure,
			insecureAll:   insecureAll,
			insecureHosts: insecureHosts,
		},
	}

	return client
}
