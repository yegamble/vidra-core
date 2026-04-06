package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type RequestSizeOverride struct {
	PathPrefix string
	PathSuffix string
	MaxBytes   int64
}

type SecurityConfig struct {
	CSPEnabled    bool
	CSPReportOnly bool
	CSPReportURI  string
	CDNDomains    []string
}

func SecurityHeaders(cfg SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")

			if cfg.CSPEnabled {
				csp := buildCSP(cfg.CDNDomains, cfg.CSPReportURI)
				headerName := "Content-Security-Policy"
				if cfg.CSPReportOnly {
					headerName = "Content-Security-Policy-Report-Only"
				}
				w.Header().Set(headerName, csp)
			}

			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}

			next.ServeHTTP(w, r)
		})
	}
}

func buildCSP(cdnDomains []string, reportURI string) string {
	directives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"font-src 'self' data:",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"upgrade-insecure-requests",
	}

	imgSrc := "'self' data:"
	connectSrc := "'self'"
	mediaSrc := "'self' blob:"
	for _, domain := range cdnDomains {
		if domain != "" {
			host := extractHostFromURL(domain)
			imgSrc += " " + host
			connectSrc += " " + host
			mediaSrc += " " + host
		}
	}
	directives = append(directives, "img-src "+imgSrc)
	directives = append(directives, "connect-src "+connectSrc)
	directives = append(directives, "media-src "+mediaSrc)

	if reportURI != "" {
		directives = append(directives, "report-uri "+reportURI)
	}

	return strings.Join(directives, "; ")
}

func extractHostFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host
	}
	return rawURL
}

func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				b := make([]byte, 16)
				_, err := rand.Read(b)
				if err != nil {
					requestID = uuid.New().String()
					slog.Info(fmt.Sprintf("WARNING: crypto/rand failed in RequestID generation, falling back to UUID: %v", err))
				} else {
					requestID = base64.RawURLEncoding.EncodeToString(b)
				}
			}

			w.Header().Set("X-Request-ID", requestID)
			next.ServeHTTP(w, r)
		})
	}
}

func SizeLimiter(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func SizeLimiterWithOverrides(defaultMaxBytes int64, overrides []RequestSizeOverride) func(http.Handler) http.Handler {
	if defaultMaxBytes <= 0 {
		defaultMaxBytes = 10 * 1024 * 1024
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

func APIKeyAuth(validateKey func(string) (string, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
