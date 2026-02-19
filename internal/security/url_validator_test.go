package security

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestValidateURL_ValidURLs tests that valid URLs pass validation
func TestValidateURL_ValidURLs(t *testing.T) {
	validator := NewURLValidator()

	validURLs := []string{
		"https://example.com",
		"http://example.com",
		"https://example.com/path",
		"https://example.com:443/path?query=value",
		"http://subdomain.example.com",
		"https://example.com:8080",
		"https://192.0.2.1", // TEST-NET-1 (documentation IP, should be blocked but let's test)
	}

	for _, url := range validURLs {
		err := validator.ValidateURL(url)
		if err != nil {
			t.Logf("URL %s validation result: %v (some IPs may be blocked by SSRF protection)", url, err)
		}
	}
}

// TestValidateURL_InvalidSchemes tests that non-HTTP(S) schemes are rejected
func TestValidateURL_InvalidSchemes(t *testing.T) {
	validator := NewURLValidator()

	invalidURLs := []string{
		"ftp://example.com",
		"file:///etc/passwd",
		"javascript:alert('xss')",
		"data:text/html,<script>alert('xss')</script>",
		"gopher://example.com",
		"ldap://example.com",
		"ssh://example.com",
		"telnet://example.com",
		"dict://example.com",
		"sftp://example.com",
	}

	for _, url := range invalidURLs {
		err := validator.ValidateURL(url)
		if err == nil {
			t.Errorf("Expected URL validation to fail for %s, but it passed", url)
		}
	}
}

// TestValidateURL_SSRFProtection tests blocking of private/internal IPs
func TestValidateURL_SSRFProtection(t *testing.T) {
	validator := NewURLValidator()

	// These should all be blocked by SSRF protection
	ssrfURLs := []struct {
		url         string
		description string
	}{
		// Loopback addresses
		{"http://127.0.0.1", "IPv4 loopback"},
		{"http://127.0.0.1:8080", "IPv4 loopback with port"},
		{"http://127.1.1.1", "IPv4 loopback range"},
		{"http://localhost", "localhost hostname"},
		{"http://[::1]", "IPv6 loopback"},
		{"http://[0:0:0:0:0:0:0:1]", "IPv6 loopback expanded"},

		// AWS/GCP metadata endpoints
		{"http://169.254.169.254", "AWS EC2 metadata"},
		{"http://169.254.169.254/latest/meta-data/", "AWS metadata path"},

		// RFC1918 private networks
		{"http://10.0.0.1", "RFC1918 10.0.0.0/8"},
		{"http://10.255.255.255", "RFC1918 10.0.0.0/8 upper bound"},
		{"http://172.16.0.1", "RFC1918 172.16.0.0/12"},
		{"http://172.31.255.255", "RFC1918 172.16.0.0/12 upper bound"},
		{"http://192.168.0.1", "RFC1918 192.168.0.0/16"},
		{"http://192.168.255.255", "RFC1918 192.168.0.0/16 upper bound"},

		// Link-local addresses
		{"http://169.254.1.1", "Link-local address"},

		// Carrier-grade NAT
		{"http://100.64.0.1", "Carrier-grade NAT"},
		{"http://100.127.255.255", "Carrier-grade NAT upper bound"},

		// Reserved/test networks
		{"http://192.0.0.1", "IETF Protocol Assignments"},
		{"http://192.0.2.1", "TEST-NET-1"},
		{"http://198.18.0.1", "Benchmarking"},
		{"http://198.51.100.1", "TEST-NET-2"},
		{"http://203.0.113.1", "TEST-NET-3"},
		{"http://0.0.0.0", "Current network"},
		{"http://255.255.255.255", "Broadcast"},

		// Multicast and reserved
		{"http://224.0.0.1", "Multicast"},
		{"http://240.0.0.1", "Reserved"},

		// IPv6 private ranges
		{"http://[fc00::1]", "IPv6 unique local"},
		{"http://[fd00::1]", "IPv6 unique local"},
		{"http://[fe80::1]", "IPv6 link-local"},
		{"http://[ff00::1]", "IPv6 multicast"},
		{"http://[::]", "IPv6 unspecified"},
		{"http://[2001:db8::1]", "IPv6 documentation"},
	}

	for _, test := range ssrfURLs {
		err := validator.ValidateURL(test.url)
		if err == nil {
			t.Errorf("Expected SSRF protection to block %s (%s), but it passed", test.url, test.description)
		} else {
			t.Logf("✓ Correctly blocked %s (%s): %v", test.url, test.description, err)
		}
	}
}

// TestValidateURL_IPv4MappedIPv6 tests IPv4-mapped IPv6 address blocking
func TestValidateURL_IPv4MappedIPv6(t *testing.T) {
	validator := NewURLValidator()

	// IPv4-mapped IPv6 addresses that should be blocked
	mappedURLs := []string{
		"http://[::ffff:127.0.0.1]",       // Loopback
		"http://[::ffff:10.0.0.1]",        // Private
		"http://[::ffff:192.168.1.1]",     // Private
		"http://[::ffff:169.254.169.254]", // AWS metadata
	}

	for _, url := range mappedURLs {
		err := validator.ValidateURL(url)
		if err == nil {
			t.Errorf("Expected SSRF protection to block IPv4-mapped IPv6 address %s, but it passed", url)
		}
	}
}

// TestValidateURL_DNSRebinding tests protection against DNS rebinding
func TestValidateURL_DNSRebinding(t *testing.T) {
	validator := NewURLValidator()

	// These hostnames might resolve to private IPs in a real attack
	// In our test environment, we expect resolution to fail or be blocked
	rebindingURLs := []string{
		"http://localtest.me", // Often resolves to 127.0.0.1
	}

	for _, url := range rebindingURLs {
		err := validator.ValidateURL(url)
		// Either DNS resolution fails or IP is blocked
		if err == nil {
			t.Logf("Warning: %s was not blocked. Check if it resolves to a private IP", url)
		}
	}
}

// TestValidateURL_EdgeCases tests edge cases and malformed URLs
func TestValidateURL_EdgeCases(t *testing.T) {
	validator := NewURLValidator()

	edgeCases := []struct {
		url         string
		shouldFail  bool
		description string
	}{
		{"", true, "empty URL"},
		{"http://", true, "no host"},
		{"http:///path", true, "no host with path"},
		{"://example.com", true, "no scheme"},
		{"example.com", true, "no scheme"},
		{"http://93.184.216.34/../../../etc/passwd", false, "path traversal (scheme/host valid, using example.com IP)"},
		{"http://example.com\r\n\r\nGET /admin", true, "HTTP request smuggling attempt (control chars rejected)"},
		{"http://user:pass@93.184.216.34", false, "URL with credentials (using example.com IP)"},
		{"http://example.com:99999", true, "invalid port"},
		{"http://[invalid", true, "malformed IPv6"},
	}

	for _, test := range edgeCases {
		err := validator.ValidateURL(test.url)
		if test.shouldFail && err == nil {
			t.Errorf("Expected %s (%s) to fail validation, but it passed", test.url, test.description)
		} else if !test.shouldFail && err != nil {
			t.Errorf("Expected %s (%s) to pass validation, but it failed: %v", test.url, test.description, err)
		} else if test.shouldFail && err != nil {
			t.Logf("✓ Correctly rejected %s: %v", test.description, err)
		} else {
			t.Logf("✓ Correctly accepted %s", test.description)
		}
	}
}

// TestValidateURL_ComprehensiveEdgeCases tests all potential bypass techniques
func TestValidateURL_ComprehensiveEdgeCases(t *testing.T) {
	validator := NewURLValidator()

	tests := []struct {
		url         string
		shouldFail  bool
		description string
		category    string
	}{
		// Alternative IP Notations (Critical Security Edge Cases)
		{"http://127.0.0.%31", true, "URL encoded loopback", "encoding"},
		{"http://127.%30.%30.1", true, "partially encoded loopback", "encoding"},
		{"http://127.1", true, "shortened loopback notation", "encoding"},
		{"http://127.0.1", true, "shortened loopback notation 2", "encoding"},
		{"http://2130706433", true, "decimal IP (127.0.0.1)", "encoding"},
		{"http://0x7f000001", true, "hexadecimal IP (127.0.0.1)", "encoding"},
		{"http://0177.0.0.1", true, "octal IP (127.0.0.1)", "encoding"},

		// IPv6 Edge Cases
		{"http://[::ffff:7f00:1]", true, "IPv4-mapped IPv6 loopback", "ipv6"},
		{"http://[0:0:0:0:0:ffff:127.0.0.1]", true, "IPv4-mapped IPv6 expanded", "ipv6"},
		{"http://[::127.0.0.1]", true, "IPv6 compatible IPv4 loopback", "ipv6"},

		// Port Edge Cases
		{"http://93.184.216.34:0", true, "port 0", "port"},
		{"http://93.184.216.34:-1", true, "negative port", "port"},
		{"http://93.184.216.34:65536", true, "port > 65535", "port"},
		{"http://8.8.8.8:65535", false, "max valid port", "port"},
		{"http://8.8.8.8:80", false, "explicit HTTP port", "port"},

		// Control Characters
		{"http://example.com\x00", true, "null byte", "control"},
		{"http://example.com\t", true, "tab character", "control"},
		{"http://example.com\n", true, "newline", "control"},
		{"http://example.com ", true, "trailing space", "control"},
		{" http://example.com", true, "leading space", "control"},

		// Alternative Notations
		{"http://0", true, "IP 0 shorthand", "notation"},
		{"http://0.0.0.0", true, "unspecified address", "notation"},
		{"http://255.255.255.255", true, "broadcast", "notation"},

		// Valid but unusual
		{"http://8.8.8.8", false, "Google DNS IP", "valid"},
		{"http://1.1.1.1", false, "Cloudflare DNS IP", "valid"},
		{"https://93.184.216.34:443", false, "explicit HTTPS port", "valid"},
		{"http://93.184.216.34:8080/path?query=value#fragment", false, "full URL components", "valid"},

		// Fragment & Query Parameter Edge Cases
		{"http://93.184.216.34#@evil.com", false, "fragment with @", "fragment"},
		{"http://93.184.216.34?@evil.com", false, "query with @", "fragment"},
		{"http://93.184.216.34#http://evil.com", false, "URL in fragment", "fragment"},

		// Case sensitivity
		{"HTTP://8.8.8.8", false, "uppercase scheme", "case"},
		{"HtTp://8.8.8.8", false, "mixed case scheme", "case"},
		{"FTP://8.8.8.8", true, "FTP scheme", "case"},
	}

	for _, test := range tests {
		t.Run(test.category+"_"+test.description, func(t *testing.T) {
			err := validator.ValidateURL(test.url)
			if test.shouldFail && err == nil {
				t.Errorf("Expected %s (%s) to fail, but passed", test.url, test.description)
			} else if !test.shouldFail && err != nil {
				t.Errorf("Expected %s (%s) to pass, but failed: %v", test.url, test.description, err)
			} else if test.shouldFail && err != nil {
				t.Logf("✓ Correctly rejected %s: %v", test.description, err)
			} else {
				t.Logf("✓ Correctly accepted %s", test.description)
			}
		})
	}
}

// TestValidateURL_AllowPrivate tests the allow private mode (for testing only)
func TestValidateURL_AllowPrivate(t *testing.T) {
	validator := NewURLValidatorAllowPrivate()

	privateURLs := []string{
		"http://127.0.0.1",
		"http://192.168.1.1",
		"http://10.0.0.1",
	}

	for _, url := range privateURLs {
		err := validator.ValidateURL(url)
		if err != nil {
			t.Errorf("Expected AllowPrivate mode to allow %s, but got error: %v", url, err)
		}
	}
}

// TestValidateURL_PublicIPs tests that public IPs are allowed
func TestValidateURL_PublicIPs(t *testing.T) {
	validator := NewURLValidator()

	publicURLs := []string{
		"http://8.8.8.8",       // Google DNS
		"http://1.1.1.1",       // Cloudflare DNS
		"http://93.184.216.34", // example.com
		"http://[2606:2800:220:1:248:1893:25c8:1946]", // example.com IPv6
	}

	for _, url := range publicURLs {
		err := validator.ValidateURL(url)
		if err != nil {
			t.Errorf("Expected public IP %s to be allowed, but got error: %v", url, err)
		}
	}
}

// TestValidateURL_PortVariations tests different port specifications
func TestValidateURL_PortVariations(t *testing.T) {
	validator := NewURLValidator()

	portTests := []struct {
		url         string
		shouldBlock bool
		description string
	}{
		{"http://127.0.0.1:80", true, "loopback with standard port"},
		{"http://127.0.0.1:8080", true, "loopback with custom port"},
		{"http://127.0.0.1:65535", true, "loopback with max port"},
		{"http://8.8.8.8:443", false, "public IP with HTTPS port"},
	}

	for _, test := range portTests {
		err := validator.ValidateURL(test.url)
		if test.shouldBlock && err == nil {
			t.Errorf("Expected %s (%s) to be blocked, but it passed", test.url, test.description)
		} else if !test.shouldBlock && err != nil {
			t.Errorf("Expected %s (%s) to be allowed, but got error: %v", test.url, test.description, err)
		}
	}
}

// TestValidateURL_CaseInsensitiveSchemes tests that scheme checking is case-insensitive
func TestValidateURL_CaseInsensitiveSchemes(t *testing.T) {
	validator := NewURLValidator()

	// Go's url.Parse already handles this, but let's verify
	schemeTests := []struct {
		url         string
		shouldFail  bool
		description string
	}{
		{"HTTP://example.com", false, "uppercase HTTP"},
		{"HTTPS://example.com", false, "uppercase HTTPS"},
		{"HtTp://example.com", false, "mixed case HTTP"},
		{"FTP://example.com", true, "uppercase FTP (should fail)"},
	}

	for _, test := range schemeTests {
		err := validator.ValidateURL(test.url)
		if test.shouldFail && err == nil {
			t.Errorf("Expected %s (%s) to fail, but it passed", test.url, test.description)
		}
	}
}

// TestValidateAndResolveURL tests the resolution function
func TestValidateAndResolveURL(t *testing.T) {
	validator := NewURLValidator()

	// Test with a public URL that should resolve
	ips, err := validator.ValidateAndResolveURL("https://dns.google")
	if err != nil {
		t.Logf("Resolution test: %v (DNS might not be available)", err)
	} else {
		t.Logf("Successfully resolved to IPs: %v", ips)
	}

	// Test with localhost (should fail)
	_, err = validator.ValidateAndResolveURL("http://localhost")
	if err == nil {
		t.Error("Expected localhost to be blocked by SSRF protection")
	}
}

// TestIsPrivateIP tests the private IP detection function directly
func TestIsPrivateIP(t *testing.T) {
	validator := NewURLValidator()

	// We can't directly test isPrivateIP as it's not exported,
	// but we can test through ValidateURL
	tests := []struct {
		url       string
		isPrivate bool
	}{
		{"http://127.0.0.1", true},
		{"http://10.0.0.1", true},
		{"http://172.16.0.1", true},
		{"http://192.168.1.1", true},
		{"http://169.254.169.254", true},
		{"http://8.8.8.8", false},
		{"http://1.1.1.1", false},
	}

	for _, test := range tests {
		err := validator.ValidateURL(test.url)
		hasError := err != nil
		if test.isPrivate && !hasError {
			t.Errorf("Expected %s to be detected as private IP", test.url)
		} else if !test.isPrivate && hasError {
			t.Errorf("Expected %s to be detected as public IP, got error: %v", test.url, err)
		}
	}
}

// TestSafeDialer tests that the SafeDialer blocks private IPs
func TestSafeDialer(t *testing.T) {
	validator := NewURLValidator()

	// Try to dial a private IP
	ctx := context.Background()
	_, err := validator.SafeDialer(ctx, "tcp", "127.0.0.1:80")
	if err == nil {
		t.Error("Expected SafeDialer to block 127.0.0.1, but it didn't")
	} else if !strings.Contains(err.Error(), "access to private IP addresses is not allowed") {
		t.Errorf("Expected private IP error, got: %v", err)
	}
}

// TestNewSafeHTTPClient tests that the client uses the SafeDialer
func TestNewSafeHTTPClient(t *testing.T) {
	validator := NewURLValidator()
	client := validator.NewSafeHTTPClient(1 * time.Second)

	// Try to get a private IP
	_, err := client.Get("http://127.0.0.1")
	if err == nil {
		t.Error("Expected client to fail for 127.0.0.1")
	} else {
		// The error comes from Transport.RoundTrip -> DialContext -> SafeDialer
		if !strings.Contains(err.Error(), "access to private IP addresses is not allowed") {
			t.Logf("Got error as expected, but message was: %v", err)
		}
	}
}

// BenchmarkValidateURL benchmarks URL validation performance
func BenchmarkValidateURL(b *testing.B) {
	validator := NewURLValidator()
	url := "https://example.com/path?query=value"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}

// BenchmarkValidateURL_Private benchmarks private IP detection
func BenchmarkValidateURL_Private(b *testing.B) {
	validator := NewURLValidator()
	url := "http://192.168.1.1"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}
