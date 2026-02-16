package setup

import (
	"errors"
	"strings"
)

var (
	ErrInvalidURL     = errors.New("invalid URL")
	ErrShellMetachars = errors.New("input contains shell metacharacters")
	ErrWeakSecret     = errors.New("JWT secret is too weak or too short")
	ErrInvalidDomain  = errors.New("invalid domain")
	ErrInvalidPort    = errors.New("invalid port")
)

func ValidateDatabaseURL(url string) error {
	if url == "" {
		return ErrInvalidURL
	}

	if !strings.HasPrefix(url, "postgres://") && !strings.HasPrefix(url, "postgresql://") {
		return ErrInvalidURL
	}

	if containsShellMetachars(url) {
		return ErrShellMetachars
	}

	return nil
}

func ValidateRedisURL(url string) error {
	if url == "" {
		return ErrInvalidURL
	}

	if !strings.HasPrefix(url, "redis://") {
		return ErrInvalidURL
	}

	if containsShellMetachars(url) {
		return ErrShellMetachars
	}

	return nil
}

func ValidateJWTSecret(secret string) error {
	if len(secret) < 32 {
		return ErrWeakSecret
	}

	weakValues := []string{"secret", "password", "12345", "admin", "test"}
	lowerSecret := strings.ToLower(secret)
	for _, weak := range weakValues {
		if strings.Contains(lowerSecret, weak) {
			return ErrWeakSecret
		}
	}

	return nil
}

func ValidateDomain(domain string) error {
	if domain == "" {
		return ErrInvalidDomain
	}

	if containsShellMetachars(domain) {
		return ErrShellMetachars
	}

	return nil
}

func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return ErrInvalidPort
	}

	if port < 1024 && port != 80 && port != 443 {
		return ErrInvalidPort
	}

	return nil
}

func containsShellMetachars(s string) bool {
	metachars := []string{";", "|", "&", "$", "`", "\\"}
	for _, mc := range metachars {
		if strings.Contains(s, mc) {
			return true
		}
	}
	return false
}
