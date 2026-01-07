package web

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthMiddleware returns HTTP middleware that validates bearer token authentication.
// Token can be provided via:
// - Authorization header: "Bearer <token>"
// - Query parameter: "?token=<token>"
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if no token configured
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Try Authorization header first
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				if strings.HasPrefix(authHeader, "Bearer ") {
					providedToken := strings.TrimPrefix(authHeader, "Bearer ")
					if secureCompare(providedToken, token) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}

			// Try query parameter
			queryToken := r.URL.Query().Get("token")
			if queryToken != "" && secureCompare(queryToken, token) {
				next.ServeHTTP(w, r)
				return
			}

			// Unauthorized
			http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
		})
	}
}

// secureCompare performs constant-time comparison to prevent timing attacks
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
