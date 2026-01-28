package middleware

import (
	"net/http"
	"strings"
)

// CORS returns a middleware that sets Cross-Origin Resource Sharing headers
// based on the provided allowedOrigins (comma-separated).
// It properly handles Access-Control-Allow-Credentials by reflecting the Origin
// when allowed, avoiding the invalid "*" + "true" combination.
func CORS(allowedOrigins string) func(http.Handler) http.Handler {
	// Parse allowed origins into a map for fast lookup
	allowed := make(map[string]bool)
	allowAll := false

	for _, o := range strings.Split(allowedOrigins, ",") {
		trimmed := strings.TrimSpace(o)
		if trimmed == "*" {
			allowAll = true
		}
		allowed[trimmed] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Only set CORS headers if Origin is present (CORS request)
			// and matches the allowed list (or wildcard is enabled)
			if origin != "" && (allowAll || allowed[origin]) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Requested-With, Idempotency-Key")
				w.Header().Set("Access-Control-Expose-Headers", "Link")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "300")
				w.Header().Add("Vary", "Origin")
			}

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
