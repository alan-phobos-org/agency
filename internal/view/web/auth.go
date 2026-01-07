package web

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// RateLimiter tracks failed auth attempts per IP
type RateLimiter struct {
	mu       sync.RWMutex
	attempts map[string]*ipAttempts
}

type ipAttempts struct {
	count     int
	blockedAt time.Time
}

const (
	maxFailedAttempts = 10
	blockDuration     = time.Hour
)

// NewRateLimiter creates a new rate limiter
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		attempts: make(map[string]*ipAttempts),
	}
}

// IsBlocked checks if an IP is currently blocked
func (rl *RateLimiter) IsBlocked(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	att, ok := rl.attempts[ip]
	if !ok {
		return false
	}

	// Check if block has expired
	if !att.blockedAt.IsZero() && time.Since(att.blockedAt) < blockDuration {
		return true
	}

	return false
}

// RecordFailure records a failed auth attempt and returns true if IP is now blocked
func (rl *RateLimiter) RecordFailure(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	att, ok := rl.attempts[ip]
	if !ok {
		att = &ipAttempts{}
		rl.attempts[ip] = att
	}

	// If previously blocked and block expired, reset
	if !att.blockedAt.IsZero() && time.Since(att.blockedAt) >= blockDuration {
		att.count = 0
		att.blockedAt = time.Time{}
	}

	att.count++

	// Block if exceeded max attempts
	if att.count >= maxFailedAttempts {
		att.blockedAt = time.Now()
		att.count = 0 // Reset count for next period
		return true
	}

	return false
}

// RecordSuccess clears failed attempts for an IP
func (rl *RateLimiter) RecordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// AccessLogger logs access attempts to a file
type AccessLogger struct {
	mu   sync.Mutex
	file *os.File
}

// NewAccessLogger creates a new access logger writing to the specified file
func NewAccessLogger(path string) (*AccessLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening access log: %w", err)
	}
	return &AccessLogger{file: f}, nil
}

// Log writes an access log entry
func (al *AccessLogger) Log(ip, method, path string, status int, authSuccess bool) {
	al.mu.Lock()
	defer al.mu.Unlock()

	authStatus := "auth_ok"
	if !authSuccess {
		authStatus = "auth_fail"
	}

	entry := fmt.Sprintf("%s %s %s %s %d %s\n",
		time.Now().Format(time.RFC3339),
		ip,
		method,
		path,
		status,
		authStatus,
	)
	al.file.WriteString(entry)
}

// Close closes the access log file
func (al *AccessLogger) Close() error {
	return al.file.Close()
}

// AuthMiddleware returns HTTP middleware that validates bearer token authentication.
// Token can be provided via:
// - Authorization header: "Bearer <token>"
// - Query parameter: "?token=<token>"
// Includes rate limiting for failed attempts and optional access logging.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	return AuthMiddlewareWithLogging(token, nil, nil)
}

// AuthMiddlewareWithLogging returns auth middleware with rate limiting and access logging
func AuthMiddlewareWithLogging(token string, rateLimiter *RateLimiter, accessLogger *AccessLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			// Use X-Real-IP if available (set by RealIP middleware)
			if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
				ip = realIP
			}

			// Check if IP is blocked
			if rateLimiter != nil && rateLimiter.IsBlocked(ip) {
				if accessLogger != nil {
					accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusTooManyRequests, false)
				}
				http.Error(w, `{"error":"rate_limited","message":"Too many failed attempts. Try again later."}`, http.StatusTooManyRequests)
				return
			}

			// Skip auth if no token configured
			if token == "" {
				if accessLogger != nil {
					accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
				}
				next.ServeHTTP(w, r)
				return
			}

			// Try Authorization header first
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				if strings.HasPrefix(authHeader, "Bearer ") {
					providedToken := strings.TrimPrefix(authHeader, "Bearer ")
					if secureCompare(providedToken, token) {
						if rateLimiter != nil {
							rateLimiter.RecordSuccess(ip)
						}
						if accessLogger != nil {
							accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
						}
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Try query parameter
			queryToken := r.URL.Query().Get("token")
			if queryToken != "" && secureCompare(queryToken, token) {
				if rateLimiter != nil {
					rateLimiter.RecordSuccess(ip)
				}
				if accessLogger != nil {
					accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
				}
				next.ServeHTTP(w, r)
				return
			}

			// Unauthorized - record failure
			blocked := false
			if rateLimiter != nil {
				blocked = rateLimiter.RecordFailure(ip)
			}
			if accessLogger != nil {
				accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusUnauthorized, false)
			}
			if blocked {
				fmt.Fprintf(os.Stderr, "WARNING: IP %s blocked after %d failed auth attempts\n", ip, maxFailedAttempts)
			}
			http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
		})
	}
}

// secureCompare performs constant-time comparison to prevent timing attacks
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
