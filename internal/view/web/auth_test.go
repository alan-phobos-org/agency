package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRateLimiterBasic(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	ip := "192.168.1.1"

	// Initially not blocked
	require.False(t, rl.IsBlocked(ip))

	// Record failures up to threshold
	for i := 0; i < maxFailedAttempts-1; i++ {
		blocked := rl.RecordFailure(ip)
		require.False(t, blocked, "Should not be blocked after %d failures", i+1)
		require.False(t, rl.IsBlocked(ip))
	}

	// 10th failure should trigger block
	blocked := rl.RecordFailure(ip)
	require.True(t, blocked, "Should be blocked after %d failures", maxFailedAttempts)
	require.True(t, rl.IsBlocked(ip))
}

func TestRateLimiterSuccessResetsCount(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	ip := "192.168.1.2"

	// Record some failures
	for i := 0; i < 5; i++ {
		rl.RecordFailure(ip)
	}

	// Success should clear the count
	rl.RecordSuccess(ip)

	// Should be able to fail again without being blocked
	for i := 0; i < maxFailedAttempts-1; i++ {
		blocked := rl.RecordFailure(ip)
		require.False(t, blocked)
	}
}

func TestRateLimiterMultipleIPs(t *testing.T) {
	t.Parallel()

	rl := NewRateLimiter()
	ip1 := "10.0.0.1"
	ip2 := "10.0.0.2"

	// Block ip1
	for i := 0; i < maxFailedAttempts; i++ {
		rl.RecordFailure(ip1)
	}

	require.True(t, rl.IsBlocked(ip1))
	require.False(t, rl.IsBlocked(ip2), "Different IP should not be blocked")
}

func TestAccessLoggerWritesEntries(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "access.log")
	logger, err := NewAccessLogger(logPath)
	require.NoError(t, err)
	defer logger.Close()

	// Log some entries
	logger.Log("192.168.1.1", "GET", "/api/test", 200, true)
	logger.Log("192.168.1.2", "POST", "/api/task", 401, false)

	// Close and read the file
	logger.Close()

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	content := string(data)
	require.Contains(t, content, "192.168.1.1")
	require.Contains(t, content, "GET")
	require.Contains(t, content, "/api/test")
	require.Contains(t, content, "auth_ok")
	require.Contains(t, content, "192.168.1.2")
	require.Contains(t, content, "auth_fail")
}

func TestAccessLoggerInvalidPath(t *testing.T) {
	t.Parallel()

	_, err := NewAccessLogger("/nonexistent/directory/access.log")
	require.Error(t, err)
}

func TestSessionMiddlewareNoPassword(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "")
	require.NoError(t, err)

	middleware := SessionMiddleware(store, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Even without password configured, requests require auth (redirect to login)
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestSessionMiddlewareNoCookie(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password123")
	require.NoError(t, err)

	middleware := SessionMiddleware(store, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No cookie = redirect to login
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestSessionMiddlewareInvalidCookie(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password123")
	require.NoError(t, err)

	middleware := SessionMiddleware(store, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Invalid session cookie = redirect to login
	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "invalid-session-id"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))

	// Should also clear the invalid cookie
	cookies := rec.Result().Cookies()
	var foundClearCookie bool
	for _, c := range cookies {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			foundClearCookie = true
			break
		}
	}
	require.True(t, foundClearCookie, "Should clear invalid cookie")
}

func TestSessionMiddlewareValidSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password123")
	require.NoError(t, err)

	// Create a valid session
	session, err := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")
	require.NoError(t, err)

	middleware := SessionMiddleware(store, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify session is in context
		ctxSession := GetSessionFromContext(r.Context())
		require.NotNil(t, ctxSession)
		require.Equal(t, session.ID, ctxSession.ID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "OK", rec.Body.String())
}

func TestSessionMiddlewareExpiredSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password123")
	require.NoError(t, err)

	// Create a session
	session, err := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")
	require.NoError(t, err)

	// Manually expire the session
	store.mu.Lock()
	session.ExpiresAt = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	middleware := SessionMiddleware(store, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Expired session = redirect to login
	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestSessionMiddlewareWithAccessLogging(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password123")
	require.NoError(t, err)

	logPath := filepath.Join(dir, "access.log")
	logger, err := NewAccessLogger(logPath)
	require.NoError(t, err)
	defer logger.Close()

	// Create a valid session
	session, err := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")
	require.NoError(t, err)

	middleware := SessionMiddleware(store, logger)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Successful request
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	req.RemoteAddr = "10.0.0.1:5000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Failed request (no cookie) - API paths return 401
	req2 := httptest.NewRequest("POST", "/api/task", nil)
	req2.RemoteAddr = "10.0.0.2:5001"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusUnauthorized, rec2.Code)

	// Give logger time to write
	time.Sleep(10 * time.Millisecond)
	logger.Close()

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "auth_ok")
	require.Contains(t, lines[1], "auth_fail")
}

func TestSetSessionCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "test-session-id", true)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)

	cookie := cookies[0]
	require.Equal(t, SessionCookieName, cookie.Name)
	require.Equal(t, "test-session-id", cookie.Value)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/", cookie.Path)
}

func TestSetDeviceSessionCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	SetDeviceSessionCookie(rec, "device-session-id", true)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)

	cookie := cookies[0]
	require.Equal(t, SessionCookieName, cookie.Name)
	require.Equal(t, "device-session-id", cookie.Value)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, 365*24*60*60, cookie.MaxAge) // 1 year
}
