package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// RequestSizeOverride describes a request body size override rule.
// When both prefix and suffix are set, both must match.
type RequestSizeOverride struct {
	PathPrefix string
	PathSuffix string
	MaxBytes   int64
}

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

// SizeLimiter limits the size of request bodies
func SizeLimiter(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// SizeLimiterWithOverrides applies a default request body limit with optional path-based overrides.
func SizeLimiterWithOverrides(defaultMaxBytes int64, overrides []RequestSizeOverride) func(http.Handler) http.Handler {
	if defaultMaxBytes <= 0 {
		defaultMaxBytes = 10 * 1024 * 1024 // 10MB safety default
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			maxBytes := defaultMaxBytes
			path := r.URL.Path

			for _, override := range overrides {
				if override.MaxBytes <= 0 {
					continue
				}
				if override.PathPrefix != "" && !strings.HasPrefix(path, override.PathPrefix) {
					continue
				}
				if override.PathSuffix != "" && !strings.HasSuffix(path, override.PathSuffix) {
					continue
				}
				maxBytes = override.MaxBytes
				break
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// ParseByteSize converts values like "10MB", "5MiB", or "1048576" into bytes.
func ParseByteSize(value string) (int64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return 0, fmt.Errorf("byte size is empty")
	}

	type unit struct {
		suffix     string
		multiplier int64
	}

	units := []unit{
		{suffix: "TIB", multiplier: 1024 * 1024 * 1024 * 1024},
		{suffix: "GIB", multiplier: 1024 * 1024 * 1024},
		{suffix: "MIB", multiplier: 1024 * 1024},
		{suffix: "KIB", multiplier: 1024},
		{suffix: "TB", multiplier: 1000 * 1000 * 1000 * 1000},
		{suffix: "GB", multiplier: 1000 * 1000 * 1000},
		{suffix: "MB", multiplier: 1000 * 1000},
		{suffix: "KB", multiplier: 1000},
		{suffix: "B", multiplier: 1},
	}

	numPart := normalized
	multiplier := int64(1)
	for _, u := range units {
		if strings.HasSuffix(normalized, u.suffix) {
			numPart = strings.TrimSpace(strings.TrimSuffix(normalized, u.suffix))
			multiplier = u.multiplier
			break
		}
	}

	if numPart == "" {
		return 0, fmt.Errorf("byte size %q is missing a numeric value", value)
	}

	base, err := strconv.ParseInt(numPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", value, err)
	}
	if base <= 0 {
		return 0, fmt.Errorf("byte size %q must be greater than zero", value)
	}
	if base > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("byte size %q is too large", value)
	}

	return base * multiplier, nil
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
