package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
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

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "agency_session"

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const sessionContextKey contextKey = "session"

// GetSessionFromContext retrieves the AuthSession from the request context.
func GetSessionFromContext(ctx context.Context) *AuthSession {
	session, _ := ctx.Value(sessionContextKey).(*AuthSession)
	return session
}

// SessionMiddleware validates session cookies and protects routes.
// Requests without valid session cookies are redirected to /login.
func SessionMiddleware(store *AuthStore, accessLogger *AccessLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
				ip = realIP
			}

			// Get session cookie
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				if accessLogger != nil {
					accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusFound, false)
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			// Validate session
			session := store.GetSession(cookie.Value)
			if session == nil {
				// Invalid or expired session - clear cookie and redirect
				clearSessionCookie(w)
				if accessLogger != nil {
					accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusFound, false)
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			// Refresh session (updates last_seen and extends auth session expiry)
			store.RefreshSession(session.ID)

			// Add session to context for handlers
			ctx := context.WithValue(r.Context(), sessionContextKey, session)

			if accessLogger != nil {
				accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SetSessionCookie sets the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, sessionID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   0, // Session cookie (expires when browser closes)
	})
}

// SetDeviceSessionCookie sets a long-lived cookie for device sessions.
func SetDeviceSessionCookie(w http.ResponseWriter, sessionID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   365 * 24 * 60 * 60, // 1 year
	})
}

// clearSessionCookie removes the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
