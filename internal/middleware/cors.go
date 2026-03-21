package middleware

import (
	"net/http"
	"strings"
)

// CORS returns a middleware that sets Cross-Origin Resource Sharing headers
// based on the provided comma-separated allowedOrigins. When "*" is configured,
// the request Origin is NOT reflected and Access-Control-Allow-Credentials is NOT set.
// To use credentials, explicit origins must be configured.
func CORS(allowedOrigins, allowedMethods, allowedHeaders string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool)
	allowAll := false
	for _, o := range strings.Split(allowedOrigins, ",") {
		trimmed := strings.TrimSpace(o)
		if trimmed == "*" {
			allowAll = true
		}
		if trimmed != "" {
			allowed[trimmed] = true
		}
	}

	methodsValue := joinCSV(allowedMethods, "GET,POST,PUT,DELETE,OPTIONS,PATCH")
	headersValue := joinCSV(
		allowedHeaders,
		"Accept,Authorization,Content-Type,X-CSRF-Token,X-Requested-With,Idempotency-Key,X-Chunk-Index,X-Chunk-Checksum",
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" && (allowAll || allowed[origin]) {
				if allowed[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}

				w.Header().Set("Access-Control-Allow-Methods", methodsValue)
				w.Header().Set("Access-Control-Allow-Headers", headersValue)
				w.Header().Set("Access-Control-Expose-Headers", "Link")
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

func joinCSV(value, fallback string) string {
	source := value
	if strings.TrimSpace(source) == "" {
		source = fallback
	}

	parts := strings.Split(source, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return strings.Join(out, ", ")
}
