package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
