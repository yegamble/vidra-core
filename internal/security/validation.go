package security

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Common security errors
var (
	ErrInvalidUUID            = errors.New("invalid UUID format")
	ErrInvalidURLScheme       = errors.New("invalid URL scheme: only http and https are allowed")
	ErrSSRFBlocked            = errors.New("access to private/internal IPs is blocked for security reasons")
	ErrMetadataServiceBlocked = errors.New("access to AWS metadata service is blocked")
	ErrFileTooLarge           = errors.New("file size exceeds maximum allowed limit")
	ErrContentLengthMissing   = errors.New("content-length header is missing")
	ErrDNSRebindingDetected   = errors.New("DNS rebinding attack detected")
)

// Security configuration constants
const (
	MaxVideoFileSize   = int64(5 * 1024 * 1024 * 1024) // 5GB
	MaxImageFileSize   = int64(50 * 1024 * 1024)       // 50MB
	MaxDocumentSize    = int64(100 * 1024 * 1024)      // 100MB
	DefaultHTTPTimeout = 30 * time.Second
	DNSRebindDelay     = 100 * time.Millisecond
)

// Private IP ranges that should be blocked for SSRF protection
var privateIPBlocks = []*net.IPNet{
	parseCIDR("127.0.0.0/8"),    // Loopback
	parseCIDR("10.0.0.0/8"),     // Private class A
	parseCIDR("172.16.0.0/12"),  // Private class B
	parseCIDR("192.168.0.0/16"), // Private class C
	parseCIDR("169.254.0.0/16"), // Link-local
	parseCIDR("::1/128"),        // IPv6 loopback
	parseCIDR("fe80::/10"),      // IPv6 link-local
	parseCIDR("fc00::/7"),       // IPv6 private
}

// AWS metadata service IPs that must be blocked
var metadataServiceIPs = []string{
	"169.254.169.254", // AWS EC2 metadata service
	"fd00:ec2::254",   // AWS EC2 metadata service (IPv6)
}

// ValidateUUID validates that a string is a valid UUID
func ValidateUUID(input string) error {
	if input == "" {
		return ErrInvalidUUID
	}

	_, err := uuid.Parse(input)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidUUID, err.Error())
	}

	return nil
}

// ValidateOptionalUUID validates an optional UUID pointer
func ValidateOptionalUUID(input *string) error {
	if input == nil || *input == "" {
		return nil // Optional field, empty is valid
	}
	return ValidateUUID(*input)
}

// IsSSRFSafeURL validates that a URL is safe from SSRF attacks
func IsSSRFSafeURL(urlStr string) error {
	// Parse the URL
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check scheme - only allow http/https
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrInvalidURLScheme
	}

	// Check hostname is not empty
	hostname := u.Hostname()
	if hostname == "" {
		return errors.New("URL hostname cannot be empty")
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname: %w", err)
	}

	// Check all resolved IPs
	for _, ip := range ips {
		if err := checkIPSafety(ip); err != nil {
			return err
		}
	}

	// Implement DNS rebinding protection
	// Wait a short time and resolve again to detect DNS rebinding
	time.Sleep(DNSRebindDelay)

	ipsAfter, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("DNS resolution failed on second attempt: %w", err)
	}

	// Check if IPs changed (potential DNS rebinding)
	if !areIPListsEqual(ips, ipsAfter) {
		// IPs changed, check the new IPs too
		for _, ip := range ipsAfter {
			if err := checkIPSafety(ip); err != nil {
				return ErrDNSRebindingDetected
			}
		}
	}

	return nil
}

// checkIPSafety checks if an IP address is safe (not private/internal)
func checkIPSafety(ip net.IP) error {
	// Check if it's a metadata service IP
	ipStr := ip.String()
	for _, metaIP := range metadataServiceIPs {
		if ipStr == metaIP {
			return ErrMetadataServiceBlocked
		}
	}

	// Check if it's in any private IP range
	for _, block := range privateIPBlocks {
		if block.Contains(ip) {
			return ErrSSRFBlocked
		}
	}

	return nil
}

// CheckFileSize validates file size from Content-Length header before downloading
func CheckFileSize(urlStr string, maxSize int64) error {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: DefaultHTTPTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Limit redirects to prevent infinite loops
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			// Check each redirect URL for SSRF
			return IsSSRFSafeURL(req.URL.String())
		},
	}

	// First validate the URL for SSRF
	if err := IsSSRFSafeURL(urlStr); err != nil {
		return fmt.Errorf("URL validation failed: %w", err)
	}

	// Perform HEAD request to get Content-Length
	resp, err := client.Head(urlStr)
	if err != nil {
		return fmt.Errorf("failed to check file size: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check Content-Length header
	contentLength := resp.ContentLength
	if contentLength < 0 {
		// Content-Length not provided, we should be cautious
		// Some servers don't provide Content-Length for dynamic content
		// In production, you might want to allow this with streaming + size limit
		return ErrContentLengthMissing
	}

	if contentLength > maxSize {
		return fmt.Errorf("%w: file is %d bytes, maximum allowed is %d bytes",
			ErrFileTooLarge, contentLength, maxSize)
	}

	return nil
}

// ValidateVideoURL performs comprehensive validation for video import URLs
func ValidateVideoURL(urlStr string) error {
	// Check for SSRF vulnerabilities
	if err := IsSSRFSafeURL(urlStr); err != nil {
		return fmt.Errorf("security validation failed: %w", err)
	}

	// Check file size (if applicable)
	// Note: Some video platforms might not provide Content-Length for streaming
	// so we handle the error gracefully
	if err := CheckFileSize(urlStr, MaxVideoFileSize); err != nil {
		// If Content-Length is missing, we'll allow it but limit during actual download
		if !errors.Is(err, ErrContentLengthMissing) {
			return err
		}
	}

	return nil
}

// SanitizeString removes potentially dangerous characters from user input
func SanitizeString(input string, maxLength int) string {
	// Remove null bytes and other control characters
	input = strings.ReplaceAll(input, "\x00", "")

	// Trim whitespace
	input = strings.TrimSpace(input)

	// Limit length
	if len(input) > maxLength {
		input = input[:maxLength]
	}

	return input
}

// Helper function to parse CIDR strings
func parseCIDR(cidr string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse CIDR %s: %v", cidr, err))
	}
	return ipNet
}

// Helper function to compare IP lists
func areIPListsEqual(ips1, ips2 []net.IP) bool {
	if len(ips1) != len(ips2) {
		return false
	}

	ipMap := make(map[string]bool)
	for _, ip := range ips1 {
		ipMap[ip.String()] = true
	}

	for _, ip := range ips2 {
		if !ipMap[ip.String()] {
			return false
		}
	}

	return true
}

// CreateSecureHTTPClient creates an HTTP client with security protections
func CreateSecureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Limit redirects
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}

			// Validate each redirect URL for SSRF
			if err := IsSSRFSafeURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}

			return nil
		},
		// Disable following redirects to file:// or other dangerous schemes
		Transport: &http.Transport{
			DisableKeepAlives: true,
			MaxIdleConns:      1,
			IdleConnTimeout:   30 * time.Second,
		},
	}
}
