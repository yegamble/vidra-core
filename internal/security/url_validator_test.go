package security

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestURLValidator_ValidateURL(t *testing.T) {
	validator := NewURLValidator()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
			errMsg:  "URL cannot be empty",
		},
		{
			name:    "invalid URL format",
			url:     "not a url",
			wantErr: true,
			errMsg:  "invalid URL format",
		},
		{
			name:    "file scheme not allowed",
			url:     "file:///etc/passwd",
			wantErr: true,
			errMsg:  "must use http or https scheme",
		},
		{
			name:    "ftp scheme not allowed",
			url:     "ftp://example.com/file.txt",
			wantErr: true,
			errMsg:  "must use http or https scheme",
		},
		{
			name:    "data scheme not allowed",
			url:     "data:text/plain;base64,SGVsbG8=",
			wantErr: true,
			errMsg:  "must use http or https scheme",
		},
		{
			name:    "javascript scheme not allowed",
			url:     "javascript:alert(1)",
			wantErr: true,
			errMsg:  "must use http or https scheme",
		},
		{
			name:    "no host",
			url:     "http://",
			wantErr: true,
			errMsg:  "must have a host",
		},
		{
			name:    "localhost blocked",
			url:     "http://localhost:8080",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "127.0.0.1 blocked",
			url:     "http://127.0.0.1",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "10.0.0.1 blocked (RFC1918)",
			url:     "http://10.0.0.1",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "192.168.1.1 blocked (RFC1918)",
			url:     "http://192.168.1.1",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "172.16.0.1 blocked (RFC1918)",
			url:     "http://172.16.0.1",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "169.254.169.254 blocked (AWS metadata)",
			url:     "http://169.254.169.254/latest/meta-data/",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "0.0.0.0 blocked",
			url:     "http://0.0.0.0",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "broadcast address blocked",
			url:     "http://255.255.255.255",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "multicast blocked",
			url:     "http://224.0.0.1",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "IPv6 loopback blocked",
			url:     "http://[::1]",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "IPv6 link-local blocked",
			url:     "http://[fe80::1]",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "IPv6 unique local blocked",
			url:     "http://[fc00::1]",
			wantErr: true,
			errMsg:  "access to private IP addresses is not allowed",
		},
		{
			name:    "valid public domain - example.com",
			url:     "http://example.com",
			wantErr: false,
		},
		{
			name:    "valid public domain with https",
			url:     "https://example.com",
			wantErr: false,
		},
		{
			name:    "valid public domain with path",
			url:     "https://example.com/path/to/resource",
			wantErr: false,
		},
		{
			name:    "valid public domain with query",
			url:     "https://example.com/path?query=value",
			wantErr: false,
		},
		{
			name:    "valid public domain with port",
			url:     "https://example.com:443/path",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestURLValidator_AllowPrivate(t *testing.T) {
	validator := NewURLValidatorAllowPrivate()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "localhost allowed when private enabled",
			url:     "http://localhost:8080",
			wantErr: false,
		},
		{
			name:    "127.0.0.1 allowed when private enabled",
			url:     "http://127.0.0.1",
			wantErr: false,
		},
		{
			name:    "192.168.1.1 allowed when private enabled",
			url:     "http://192.168.1.1",
			wantErr: false,
		},
		{
			name:    "public domain still allowed",
			url:     "https://example.com",
			wantErr: false,
		},
		{
			name:    "invalid scheme still blocked",
			url:     "file:///etc/passwd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name       string
		ip         string
		wantPrivate bool
	}{
		// Loopback
		{"localhost IPv4", "127.0.0.1", true},
		{"localhost IPv4 other", "127.0.0.100", true},
		{"localhost IPv6", "::1", true},

		// RFC1918 Private Networks
		{"10.0.0.0/8 - start", "10.0.0.0", true},
		{"10.0.0.0/8 - mid", "10.128.0.1", true},
		{"10.0.0.0/8 - end", "10.255.255.255", true},
		{"172.16.0.0/12 - start", "172.16.0.0", true},
		{"172.16.0.0/12 - mid", "172.20.0.1", true},
		{"172.16.0.0/12 - end", "172.31.255.255", true},
		{"192.168.0.0/16 - start", "192.168.0.0", true},
		{"192.168.0.0/16 - mid", "192.168.100.1", true},
		{"192.168.0.0/16 - end", "192.168.255.255", true},

		// Link-local
		{"link-local IPv4 start", "169.254.0.0", true},
		{"link-local IPv4 AWS metadata", "169.254.169.254", true},
		{"link-local IPv4 end", "169.254.255.255", true},
		{"link-local IPv6", "fe80::1", true},
		{"link-local IPv6 end", "fe80::ffff:ffff:ffff:ffff", true},

		// Multicast
		{"multicast IPv4 start", "224.0.0.0", true},
		{"multicast IPv4 mid", "230.0.0.1", true},
		{"multicast IPv4 end", "239.255.255.255", true},
		{"multicast IPv6", "ff00::1", true},

		// Reserved
		{"reserved IPv4 start", "240.0.0.0", true},
		{"reserved IPv4 end", "255.255.255.254", true},
		{"broadcast", "255.255.255.255", true},

		// Current network
		{"current network start", "0.0.0.0", true},
		{"current network end", "0.255.255.255", true},

		// Carrier-grade NAT
		{"CGNAT start", "100.64.0.0", true},
		{"CGNAT mid", "100.100.0.1", true},
		{"CGNAT end", "100.127.255.255", true},

		// Test networks
		{"TEST-NET-1", "192.0.2.1", true},
		{"TEST-NET-2", "198.51.100.1", true},
		{"TEST-NET-3", "203.0.113.1", true},
		{"benchmarking", "198.18.0.1", true},

		// IPv6 Unique Local
		{"IPv6 unique local start", "fc00::1", true},
		{"IPv6 unique local mid", "fd00::1", true},
		{"IPv6 unique local end", "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true},

		// IPv6 Documentation
		{"IPv6 documentation", "2001:db8::1", true},

		// Public IPs (should NOT be private)
		{"public IPv4 - Google DNS", "8.8.8.8", false},
		{"public IPv4 - Cloudflare DNS", "1.1.1.1", false},
		{"public IPv4 - example.com", "93.184.216.34", false},
		{"public IPv6 - Google DNS", "2001:4860:4860::8888", false},
		{"public IPv6 - Cloudflare DNS", "2606:4700:4700::1111", false},

		// Edge cases
		{"just outside 10.0.0.0/8", "11.0.0.0", false},
		{"just outside 172.16.0.0/12", "172.32.0.0", false},
		{"just outside 192.168.0.0/16", "192.169.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip, "Failed to parse IP: %s", tt.ip)

			got := isPrivateIP(ip)
			assert.Equal(t, tt.wantPrivate, got,
				"isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.wantPrivate)
		})
	}
}

func TestURLValidator_SSRFBypassAttempts(t *testing.T) {
	validator := NewURLValidator()

	// Common SSRF bypass techniques that should be blocked
	bypassAttempts := []struct {
		name string
		url  string
	}{
		{"decimal notation", "http://2130706433"}, // 127.0.0.1 in decimal
		{"octal notation", "http://0177.0.0.1"},   // 127.0.0.1 in octal
		{"hex notation", "http://0x7f.0.0.1"},     // 127.0.0.1 in hex
		{"IPv6 localhost", "http://[::1]"},
		{"IPv6 loopback full", "http://[0:0:0:0:0:0:0:1]"},
		{"IPv6 compressed", "http://[::ffff:127.0.0.1]"},
		{"AWS metadata", "http://169.254.169.254"},
		{"AWS metadata with path", "http://169.254.169.254/latest/meta-data/"},
	}

	for _, tt := range bypassAttempts {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			assert.Error(t, err, "SSRF bypass attempt should be blocked: %s", tt.url)
		})
	}
}

func TestURLValidator_ValidateAndResolveURL(t *testing.T) {
	validator := NewURLValidator()

	t.Run("valid public URL resolution", func(t *testing.T) {
		ips, err := validator.ValidateAndResolveURL("http://example.com")
		assert.NoError(t, err)
		assert.NotEmpty(t, ips, "Should resolve to at least one IP")
	})

	t.Run("invalid URL returns error", func(t *testing.T) {
		ips, err := validator.ValidateAndResolveURL("http://127.0.0.1")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})

	t.Run("non-existent domain returns error", func(t *testing.T) {
		ips, err := validator.ValidateAndResolveURL("http://this-domain-definitely-does-not-exist-12345.com")
		assert.Error(t, err)
		assert.Nil(t, ips)
	})
}

func TestURLValidator_RealWorldScenarios(t *testing.T) {
	validator := NewURLValidator()

	tests := []struct {
		name        string
		url         string
		description string
		wantErr     bool
	}{
		{
			name:        "github repository",
			url:         "https://github.com/user/repo",
			description: "Public GitHub repo should be allowed",
			wantErr:     false,
		},
		{
			name:        "youtube video",
			url:         "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			description: "Public YouTube video should be allowed",
			wantErr:     false,
		},
		{
			name:        "twitter post",
			url:         "https://twitter.com/user/status/12345",
			description: "Public Twitter post should be allowed",
			wantErr:     false,
		},
		{
			name:        "internal network scanner attempt",
			url:         "http://192.168.1.1/admin",
			description: "Internal router admin should be blocked",
			wantErr:     true,
		},
		{
			name:        "kubernetes API attempt",
			url:         "http://10.0.0.1:8080/api",
			description: "Internal Kubernetes API should be blocked",
			wantErr:     true,
		},
		{
			name:        "docker socket attempt",
			url:         "http://127.0.0.1:2375/containers/json",
			description: "Local Docker socket should be blocked",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestIsPrivateIP_IPv4MappedIPv6(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{
			name: "IPv4-mapped IPv6 localhost",
			ip:   "::ffff:127.0.0.1",
			want: true,
		},
		{
			name: "IPv4-mapped IPv6 private",
			ip:   "::ffff:192.168.1.1",
			want: true,
		},
		{
			name: "IPv4-mapped IPv6 public",
			ip:   "::ffff:8.8.8.8",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip)
			got := isPrivateIP(ip)
			assert.Equal(t, tt.want, got)
		})
	}
}
