package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// URLValidator provides secure URL validation with SSRF protection
type URLValidator struct {
	allowPrivate bool // For testing purposes only
}

// NewURLValidator creates a new URL validator
func NewURLValidator() *URLValidator {
	return &URLValidator{
		allowPrivate: false,
	}
}

// NewURLValidatorAllowPrivate creates a validator that allows private IPs (for testing only)
func NewURLValidatorAllowPrivate() *URLValidator {
	return &URLValidator{
		allowPrivate: true,
	}
}

// ValidateURL validates a URL and checks for SSRF vulnerabilities
func (v *URLValidator) ValidateURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("URL cannot be empty")
	}

	// Parse URL
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Only allow http/https
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use http or https scheme, got: %s", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("URL must have a host")
	}

	// Extract hostname (remove port if present)
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		// No port present, use the whole host
		host = u.Host
	}

	// Resolve hostname to IP addresses
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	// Check each resolved IP
	if !v.allowPrivate {
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return fmt.Errorf("access to private IP addresses is not allowed: %s resolves to %s", host, ip)
			}
		}
	}

	return nil
}

// isPrivateIP checks if an IP address is in a private range
func isPrivateIP(ip net.IP) bool {
	// IPv4 private/restricted ranges
	private4 := []string{
		"10.0.0.0/8",         // RFC1918 private network
		"172.16.0.0/12",      // RFC1918 private network
		"192.168.0.0/16",     // RFC1918 private network
		"127.0.0.0/8",        // Loopback
		"169.254.0.0/16",     // Link-local (AWS metadata)
		"0.0.0.0/8",          // Current network
		"224.0.0.0/4",        // Multicast
		"240.0.0.0/4",        // Reserved
		"100.64.0.0/10",      // Carrier-grade NAT
		"192.0.0.0/24",       // IETF Protocol Assignments
		"192.0.2.0/24",       // TEST-NET-1
		"198.18.0.0/15",      // Benchmarking
		"198.51.100.0/24",    // TEST-NET-2
		"203.0.113.0/24",     // TEST-NET-3
		"255.255.255.255/32", // Broadcast
	}

	// IPv6 private/restricted ranges
	private6 := []string{
		"::1/128",       // Loopback
		"fc00::/7",      // Unique local
		"fe80::/10",     // Link-local
		"ff00::/8",      // Multicast
		"::/128",        // Unspecified
		"::ffff:0:0/96", // IPv4-mapped IPv6
		"2001:db8::/32", // Documentation
	}

	// Check IPv4 ranges
	for _, cidr := range private4 {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	// Check IPv6 ranges
	for _, cidr := range private6 {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	// Additional checks for IPv4-mapped IPv6 addresses
	if ip.To4() != nil {
		// Convert to IPv4 and check again
		return isPrivateIP(ip.To4())
	}

	// Check for IPv4-compatible IPv6 addresses (deprecated)
	if strings.HasPrefix(ip.String(), "::ffff:") {
		// Extract IPv4 part
		parts := strings.Split(ip.String(), ":")
		if len(parts) > 0 {
			ipv4Str := parts[len(parts)-1]
			if ipv4 := net.ParseIP(ipv4Str); ipv4 != nil {
				return isPrivateIP(ipv4)
			}
		}
	}

	return false
}

// ValidateAndResolveURL validates a URL and returns the resolved IPs (for testing)
func (v *URLValidator) ValidateAndResolveURL(rawURL string) ([]net.IP, error) {
	if err := v.ValidateURL(rawURL); err != nil {
		return nil, err
	}

	u, _ := url.Parse(rawURL)
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
	}

	return net.LookupIP(host)
}
