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

func TestAuthMiddlewareNoToken(t *testing.T) {
	t.Parallel()

	// Empty token = no auth required
	middleware := AuthMiddleware("")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "OK", rec.Body.String())
}

func TestAuthMiddlewareBearerHeader(t *testing.T) {
	t.Parallel()

	token := "secret-token-123"
	middleware := AuthMiddleware(token)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Valid token
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Invalid token
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestAuthMiddlewareQueryParam(t *testing.T) {
	t.Parallel()

	token := "query-token-456"
	middleware := AuthMiddleware(token)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Valid token in query
	req := httptest.NewRequest("GET", "/test?token="+token, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Invalid token in query
	req2 := httptest.NewRequest("GET", "/test?token=wrong", nil)
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestAuthMiddlewareMissingToken(t *testing.T) {
	t.Parallel()

	token := "required-token"
	middleware := AuthMiddleware(token)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No token provided
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "unauthorized")
}

func TestAuthMiddlewareBearerPriority(t *testing.T) {
	t.Parallel()

	token := "correct-token"
	middleware := AuthMiddleware(token)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Both header and query, header is checked first
	req := httptest.NewRequest("GET", "/test?token=wrong-query", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "Should succeed with correct header token")
}

func TestAuthMiddlewareFallbackToQuery(t *testing.T) {
	t.Parallel()

	token := "correct-token"
	middleware := AuthMiddleware(token)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Wrong header, correct query - should fall back to query
	req := httptest.NewRequest("GET", "/test?token="+token, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "Should succeed with correct query token when header fails")
}

func TestSecureCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"", "", true},
		{"a", "", false},
	}

	for _, tt := range tests {
		got := secureCompare(tt.a, tt.b)
		require.Equal(t, tt.want, got, "secureCompare(%q, %q)", tt.a, tt.b)
	}
}

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

func TestAuthMiddlewareWithRateLimiting(t *testing.T) {
	t.Parallel()

	token := "secret-token"
	rl := NewRateLimiter()

	middleware := AuthMiddlewareWithLogging(token, rl, nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust rate limit with wrong tokens
	for i := 0; i < maxFailedAttempts; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if i < maxFailedAttempts-1 {
			require.Equal(t, http.StatusUnauthorized, rec.Code)
		}
	}

	// Next request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token) // Even with correct token
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "rate_limited")
}

func TestAuthMiddlewareWithAccessLogging(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "access.log")
	logger, err := NewAccessLogger(logPath)
	require.NoError(t, err)
	defer logger.Close()

	token := "secret-token"
	middleware := AuthMiddlewareWithLogging(token, nil, logger)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Successful request
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.0.0.1:5000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Failed request
	req2 := httptest.NewRequest("POST", "/api/task", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
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
