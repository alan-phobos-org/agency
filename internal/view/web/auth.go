package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"phobos.org.uk/agency/internal/api"
)

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

// SessionMiddleware validates authentication and protects routes.
// Supports multiple auth methods:
// - Session cookie (for web UI)
// - Bearer token in Authorization header (for API)
// - Token query parameter (for API)
// API paths (/api/*) return 401 on auth failure; others redirect to /login.
func SessionMiddleware(store *AuthStore, accessLogger *AccessLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
				ip = realIP
			}

			isAPIPath := strings.HasPrefix(r.URL.Path, "/api/")

			// Helper to handle auth failure
			authFailed := func() {
				if isAPIPath {
					if accessLogger != nil {
						accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusUnauthorized, false)
					}
					http.Error(w, `{"error":"`+api.ErrorUnauthorized+`"}`, http.StatusUnauthorized)
				} else {
					if accessLogger != nil {
						accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusFound, false)
					}
					http.Redirect(w, r, "/login", http.StatusFound)
				}
			}

			// If no store configured, deny access
			if store == nil {
				authFailed()
				return
			}

			// Try bearer token auth (for API access)
			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				token := strings.TrimPrefix(authHeader, "Bearer ")
				if store.ValidatePassword(token) {
					if accessLogger != nil {
						accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
					}
					next.ServeHTTP(w, r)
					return
				}
			}

			// Try query param token (for API access)
			if token := r.URL.Query().Get("token"); token != "" {
				if store.ValidatePassword(token) {
					if accessLogger != nil {
						accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
					}
					next.ServeHTTP(w, r)
					return
				}
			}

			// Try session cookie (for web UI)
			cookie, err := r.Cookie(SessionCookieName)
			if err == nil && cookie.Value != "" {
				session := store.GetSession(cookie.Value)
				if session != nil {
					// Refresh session (updates last_seen and extends auth session expiry)
					store.RefreshSession(session.ID)

					// Add session to context for handlers
					ctx := context.WithValue(r.Context(), sessionContextKey, session)

					if accessLogger != nil {
						accessLogger.Log(ip, r.Method, r.URL.Path, http.StatusOK, true)
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Invalid session - clear cookie
				clearSessionCookie(w)
			}

			authFailed()
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
