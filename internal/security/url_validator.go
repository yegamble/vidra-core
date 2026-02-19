package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
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
// Warning: This function checks the URL at the time of calling, but does not prevent
// DNS rebinding attacks if the URL is used later. For complete protection, use
// NewSafeHTTPClient() to make requests.
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

	// Extract hostname and port
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		// No port present, use the whole host
		host = u.Host
	} else {
		// Validate port if present
		if port != "" {
			portNum, err := strconv.Atoi(port)
			if err != nil {
				return fmt.Errorf("invalid port: %s", port)
			}
			if portNum < 1 || portNum > 65535 {
				return fmt.Errorf("port must be between 1 and 65535, got: %d", portNum)
			}
		}
	}

	// Remove brackets from IPv6 addresses (e.g., [::1] -> ::1)
	host = strings.Trim(host, "[]")

	// Check for obfuscated IP addresses (octal, hex, integer representations)
	// These are SSRF bypass techniques that need to be blocked
	if ip := parseObfuscatedIP(host); ip != nil {
		if !v.allowPrivate && isPrivateIP(ip) {
			return fmt.Errorf("access to private IP addresses is not allowed: %s", host)
		}
		return nil
	}

	// Check if host is already an IP address
	if ip := net.ParseIP(host); ip != nil {
		// Host is a direct IP address, validate it directly
		if !v.allowPrivate && isPrivateIP(ip) {
			return fmt.Errorf("access to private IP addresses is not allowed: %s", host)
		}
		return nil
	}

	// Not a direct IP, resolve hostname to IP addresses
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
	// Normalize IPv4-mapped IPv6 to IPv4 (::ffff:192.0.2.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	// Check for IPv6-compatible IPv4 addresses (::192.0.2.1 - deprecated but still possible)
	// These are IPv6 addresses with the first 96 bits as 0 and last 32 bits as IPv4
	if len(ip) == 16 {
		// Check if first 12 bytes are zero (IPv6-compatible IPv4)
		isCompat := true
		for i := 0; i < 12; i++ {
			if ip[i] != 0 {
				isCompat = false
				break
			}
		}
		if isCompat {
			// Extract the IPv4 portion from last 4 bytes
			v4 := net.IPv4(ip[12], ip[13], ip[14], ip[15])
			// Recursively check the extracted IPv4 address
			return isPrivateIP(v4)
		}
	}

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
		"2001:db8::/32", // Documentation
	}

	if ip.To4() != nil {
		for _, cidr := range private4 {
			_, network, err := net.ParseCIDR(cidr)
			if err != nil {
				continue
			}
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}

	for _, cidr := range private6 {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// parseObfuscatedIP detects and parses obfuscated IP representations
// This handles SSRF bypass techniques like:
// - Octal: 0177.0.0.1 (127.0.0.1)
// - Hex: 0x7f.0.0.1 (127.0.0.1)
// - Integer: 2130706433 (127.0.0.1)
// - Mixed: combinations of the above
func parseObfuscatedIP(host string) net.IP {
	// Try parsing as integer IP (e.g., 2130706433 = 127.0.0.1)
	if intIP, err := strconv.ParseUint(host, 10, 32); err == nil {
		return net.IPv4(
			byte(intIP>>24),
			byte(intIP>>16),
			byte(intIP>>8),
			byte(intIP),
		)
	}

	// Try parsing as dotted notation with octal/hex components
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		var octets [4]byte
		hasObfuscation := false
		valid := true

		for i, part := range parts {
			var val uint64
			var err error

			if strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X") {
				// Hex: 0x7f
				val, err = strconv.ParseUint(part[2:], 16, 8)
				hasObfuscation = true
			} else if len(part) > 1 && part[0] == '0' && !strings.Contains(part, "x") {
				// Octal: 0177
				val, err = strconv.ParseUint(part, 8, 8)
				hasObfuscation = true
			} else {
				// Decimal
				val, err = strconv.ParseUint(part, 10, 8)
			}

			if err != nil || val > 255 {
				valid = false
				break
			}
			octets[i] = byte(val)
		}

		// Only return if we detected obfuscation and it's valid
		if valid && hasObfuscation {
			return net.IPv4(octets[0], octets[1], octets[2], octets[3])
		}
	}

	return nil
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

// SafeDialer returns a dialer function that validates IP addresses before connection
// This prevents DNS rebinding attacks by ensuring the IP used for connection is validated
func (v *URLValidator) SafeDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			// address here is "ip:port" provided by the resolver
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}

			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("invalid IP address in control: %s", host)
			}

			if !v.allowPrivate && isPrivateIP(ip) {
				return fmt.Errorf("access to private IP addresses is not allowed: %s", host)
			}
			return nil
		},
	}

	return d.DialContext(ctx, network, addr)
}

// NewSafeHTTPClient creates an HTTP client with SSRF protection
func (v *URLValidator) NewSafeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           v.SafeDialer,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: timeout,
	}
}
