package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// SecurityHeaders adds comprehensive security headers for production
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prevent clickjacking attacks
			w.Header().Set("X-Frame-Options", "DENY")

			// Prevent MIME type sniffing
			w.Header().Set("X-Content-Type-Options", "nosniff")

			// Enable XSS protection in older browsers
			w.Header().Set("X-XSS-Protection", "1; mode=block")

			// Control referrer information
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// Permissions Policy (formerly Feature Policy)
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")

			// Content Security Policy - tightened for production security
			// NOTE: Removed unsafe-inline and unsafe-eval to prevent XSS attacks
			// Use nonces or external script files for any dynamic content
			csp := []string{
				"default-src 'self'",
				"script-src 'self'", // Removed unsafe-inline and unsafe-eval for XSS protection
				"style-src 'self'",  // Removed unsafe-inline - use external stylesheets
				"img-src 'self' data: https:",
				"font-src 'self' data:",
				"connect-src 'self'",
				"media-src 'self' blob:",
				"object-src 'none'",
				"frame-ancestors 'none'",
				"base-uri 'self'",
				"form-action 'self'",
				"upgrade-insecure-requests",
			}
			w.Header().Set("Content-Security-Policy", strings.Join(csp, "; "))

			// Strict Transport Security (HSTS) - only for HTTPS
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestID generates and adds a unique request ID to each request
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				b := make([]byte, 16)
				_, err := rand.Read(b)
				if err != nil {
					// SECURITY FIX: Fallback to UUID instead of hanging the request
					// crypto/rand failure is extremely rare but must be handled
					requestID = uuid.New().String()
					log.Printf("WARNING: crypto/rand failed in RequestID generation, falling back to UUID: %v", err)
				} else {
					requestID = base64.RawURLEncoding.EncodeToString(b)
				}
			}

			w.Header().Set("X-Request-ID", requestID)
			next.ServeHTTP(w, r)
		})
	}
}

// SizeLimiter limits the size of request bodies.
// maxBytes is the default limit.
// limitFunc is optional; if provided and returns > 0, that value is used as the limit.
func SizeLimiter(maxBytes int64, limitFunc func(r *http.Request) int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := maxBytes
			if limitFunc != nil {
				if l := limitFunc(r); l > 0 {
					limit = l
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyAuth provides API key authentication as an alternative to JWT
// SECURITY: API keys must be provided via X-API-Key header only (not query parameters)
// to prevent logging in access logs, browser history, and referrer headers
func APIKeyAuth(validateKey func(string) (string, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only accept API keys from headers for security
			apiKey := r.Header.Get("X-API-Key")

			if apiKey == "" {
				http.Error(w, "Missing API key in X-API-Key header", http.StatusUnauthorized)
				return
			}

			userID, err := validateKey(apiKey)
			if err != nil {
				http.Error(w, "Invalid API key", http.StatusUnauthorized)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
