package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"athena/internal/domain"
	"athena/internal/security"
	"athena/internal/usecase/redundancy"
)

// TestSSRFProtection_VideoImport tests SSRF protection for video import functionality
func TestSSRFProtection_VideoImport(t *testing.T) {
	// Test cases for malicious URLs that should be blocked
	maliciousURLs := []struct {
		name string
		url  string
	}{
		{"AWS Metadata", "http://169.254.169.254/latest/meta-data/iam/security-credentials/"},
		{"Localhost", "http://localhost:6379/"},
		{"Loopback", "http://127.0.0.1:8080/admin"},
		{"Private Network 10.x", "http://10.0.0.1/"},
		{"Private Network 192.168.x", "http://192.168.1.1/"},
		{"Private Network 172.16.x", "http://172.16.0.1/"},
		{"Link Local", "http://169.254.1.1/"},
		{"IPv6 Loopback", "http://[::1]:6379/"},
		{"IPv6 Link Local", "http://[fe80::1]/"},
		{"IPv4 Mapped IPv6 Loopback", "http://[::ffff:127.0.0.1]/"},
		{"IPv4 Mapped IPv6 Private", "http://[::ffff:192.168.1.1]/"},
	}

	for _, tc := range maliciousURLs {
		t.Run(tc.name, func(t *testing.T) {
			// Test with domain.ValidateURLWithSSRFCheck
			err := domain.ValidateURLWithSSRFCheck(tc.url)
			if err == nil {
				t.Errorf("Expected SSRF protection to block %s (%s), but it was allowed", tc.name, tc.url)
			} else {
				t.Logf("✓ Correctly blocked %s: %v", tc.name, err)
			}
		})
	}
}

// TestSSRFProtection_InstanceDiscovery tests SSRF protection for instance discovery
func TestSSRFProtection_InstanceDiscovery(t *testing.T) {
	discovery := redundancy.NewInstanceDiscovery()
	ctx := context.Background()

	maliciousURLs := []struct {
		name string
		url  string
	}{
		{"Internal Docker Network", "http://172.17.0.1/"},
		{"Kubernetes API", "http://10.96.0.1:443/"},
		{"AWS Metadata", "http://169.254.169.254/"},
		{"Localhost", "http://localhost:8080/"},
	}

	for _, tc := range maliciousURLs {
		t.Run(tc.name, func(t *testing.T) {
			_, err := discovery.DiscoverInstance(ctx, tc.url)
			if err == nil {
				t.Errorf("Expected instance discovery to block %s (%s), but it was allowed", tc.name, tc.url)
			} else {
				t.Logf("✓ Instance discovery correctly blocked %s: %v", tc.name, err)
			}
		})
	}
}

// TestSSRFProtection_URLValidator tests the URLValidator directly
func TestSSRFProtection_URLValidator(t *testing.T) {
	validator := security.NewURLValidator()

	testCases := []struct {
		name        string
		url         string
		shouldBlock bool
		description string
	}{
		// Should block
		{"AWS Metadata", "http://169.254.169.254/", true, "AWS EC2 metadata endpoint"},
		{"GCP Metadata", "http://metadata.google.internal/", true, "GCP metadata endpoint"},
		{"Localhost HTTP", "http://localhost/", true, "Localhost"},
		{"Loopback 127.0.0.1", "http://127.0.0.1/", true, "Loopback address"},
		{"Private 10.x", "http://10.0.0.1/", true, "RFC1918 private network"},
		{"Private 172.16.x", "http://172.16.0.1/", true, "RFC1918 private network"},
		{"Private 192.168.x", "http://192.168.1.1/", true, "RFC1918 private network"},
		{"Link Local", "http://169.254.1.1/", true, "Link-local address"},
		{"Carrier Grade NAT", "http://100.64.0.1/", true, "Carrier-grade NAT"},
		{"Multicast", "http://224.0.0.1/", true, "Multicast address"},
		{"IPv6 Loopback", "http://[::1]/", true, "IPv6 loopback"},
		{"IPv6 Link Local", "http://[fe80::1]/", true, "IPv6 link-local"},
		{"IPv6 Unique Local", "http://[fc00::1]/", true, "IPv6 unique local"},

		// Should allow
		{"Google DNS", "http://8.8.8.8/", false, "Public IP"},
		{"Cloudflare DNS", "http://1.1.1.1/", false, "Public IP"},
		{"Public HTTPS", "https://example.com/", false, "Public domain"},
		{"Public HTTP", "http://example.com/", false, "Public domain"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validator.ValidateURL(tc.url)
			if tc.shouldBlock && err == nil {
				t.Errorf("Expected to block %s (%s), but it was allowed", tc.url, tc.description)
			} else if !tc.shouldBlock && err != nil {
				t.Errorf("Expected to allow %s (%s), but it was blocked: %v", tc.url, tc.description, err)
			} else if tc.shouldBlock && err != nil {
				t.Logf("✓ Correctly blocked %s: %v", tc.description, err)
			} else {
				t.Logf("✓ Correctly allowed %s", tc.description)
			}
		})
	}
}

// TestSSRFProtection_InvalidSchemes tests that non-HTTP(S) schemes are blocked
func TestSSRFProtection_InvalidSchemes(t *testing.T) {
	validator := security.NewURLValidator()

	invalidSchemes := []struct {
		name   string
		url    string
		scheme string
	}{
		{"File Protocol", "file:///etc/passwd", "file"},
		{"FTP", "ftp://example.com/", "ftp"},
		{"JavaScript", "javascript:alert(1)", "javascript"},
		{"Data URI", "data:text/html,<script>alert(1)</script>", "data"},
		{"Gopher", "gopher://example.com/", "gopher"},
		{"LDAP", "ldap://example.com/", "ldap"},
		{"Dict", "dict://example.com/", "dict"},
		{"SFTP", "sftp://example.com/", "sftp"},
	}

	for _, tc := range invalidSchemes {
		t.Run(tc.name, func(t *testing.T) {
			err := validator.ValidateURL(tc.url)
			if err == nil {
				t.Errorf("Expected to block %s scheme (%s), but it was allowed", tc.scheme, tc.url)
			} else {
				t.Logf("✓ Correctly blocked %s scheme: %v", tc.scheme, err)
			}
		})
	}
}

// TestSSRFProtection_EdgeCases tests edge cases and attack vectors
func TestSSRFProtection_EdgeCases(t *testing.T) {
	validator := security.NewURLValidator()

	edgeCases := []struct {
		name        string
		url         string
		shouldBlock bool
	}{
		{"Empty URL", "", true},
		{"No Host", "http://", true},
		{"No Scheme", "example.com", true},
		{"Integer Overflow IP", "http://2130706433/", true},              // 127.0.0.1 as integer
		{"Octal IP Localhost", "http://0177.0.0.1/", true},               // Octal representation
		{"Hex IP Localhost", "http://0x7f.0.0.1/", true},                 // Hex representation
		{"Mixed Encoding", "http://127.0.0.1.nip.io/", true},             // DNS rebinding service
		{"URL with Credentials", "http://user:pass@example.com/", false}, // Should validate host only
		{"URL with Fragment", "https://example.com/#test", false},
		{"URL with Query", "https://example.com/?q=test", false},
	}

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validator.ValidateURL(tc.url)
			if tc.shouldBlock && err == nil {
				t.Errorf("Expected to block %s (%s), but it was allowed", tc.name, tc.url)
			} else if !tc.shouldBlock && err != nil {
				t.Logf("URL %s was blocked: %v (may be expected for some edge cases)", tc.name, err)
			}
		})
	}
}

// TestSSRFProtection_RedirectFollowing tests protection against redirect-based attacks
func TestSSRFProtection_RedirectFollowing(t *testing.T) {
	// Create a test server that redirects to localhost
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:6379/", http.StatusFound)
	}))
	defer redirectServer.Close()

	validator := security.NewURLValidator()

	// The initial URL should pass validation (it's a public address)
	// But actual HTTP clients should have redirect limits configured
	err := validator.ValidateURL(redirectServer.URL)
	if err != nil {
		t.Logf("Initial redirect URL blocked: %v", err)
	}

	t.Log("Note: Redirect following protection should be implemented at HTTP client level")
	t.Log("Recommendation: Configure http.Client with custom CheckRedirect function")
}

// TestSSRFProtection_DNSRebinding tests protection against DNS rebinding
func TestSSRFProtection_DNSRebinding(t *testing.T) {
	validator := security.NewURLValidator()

	// These domains might resolve to different IPs on repeated lookups
	// The validator should check at validation time
	rebindingTests := []string{
		"http://localtest.me/",     // Often resolves to 127.0.0.1
		"http://127.0.0.1.nip.io/", // DNS wildcard service
		"http://10.0.0.1.nip.io/",  // Should resolve to private IP
	}

	for _, url := range rebindingTests {
		err := validator.ValidateURL(url)
		t.Logf("DNS rebinding test for %s: %v", url, err)
		if err == nil {
			t.Logf("Warning: %s was allowed - verify it doesn't resolve to private IP", url)
		}
	}
}

// TestSSRFProtection_PortScanning tests that port specification doesn't bypass protection
func TestSSRFProtection_PortScanning(t *testing.T) {
	validator := security.NewURLValidator()

	// Common internal service ports
	ports := []int{22, 23, 25, 80, 443, 3306, 5432, 6379, 8080, 9200, 27017}

	for _, port := range ports {
		url := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
		err := validator.ValidateURL(url)
		if err == nil {
			t.Errorf("Port scanning protection failed: localhost:%d was allowed", port)
		}
	}
}

// Benchmark tests for performance
func BenchmarkSSRFValidation(b *testing.B) {
	validator := security.NewURLValidator()
	url := "https://example.com/path"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}

func BenchmarkSSRFValidation_PrivateIP(b *testing.B) {
	validator := security.NewURLValidator()
	url := "http://192.168.1.1/"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validator.ValidateURL(url)
	}
}
