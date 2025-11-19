package torrent

import (
	"context"
	"net"
	"testing"
	"time"
)

// SetupTestDNS configures DNS resolution for test environment
// This helps fix DNS resolution errors when running tests in isolated environments
func SetupTestDNS(t *testing.T) {
	t.Helper()

	// Configure resolver with public DNS servers
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 5 * time.Second,
			}
			// Use Google's public DNS as fallback
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
}

// MockedDHTConfig returns a client config with DHT disabled for unit tests
// This avoids external network calls during testing
func MockedDHTConfig() *ClientConfig {
	return &ClientConfig{
		ListenAddr:        "127.0.0.1:0", // Use localhost with random port
		DisableTCP:        false,
		DisableUTP:        false,
		DisableIPv6:       true, // Disable IPv6 in tests
		NoDHT:             true, // Disable DHT to avoid external DNS
		NoUpload:          true, // No upload in tests
		DataDir:           "/tmp/test-torrents",
		CacheSize:         1024 * 1024, // 1MB for tests
		MaxConnections:    10,
		Seed:              false,
		DisablePEX:        true, // Disable PEX in tests
		DisableWebtorrent: true, // Disable WebTorrent in tests
		DisableTrackers:   true, // Disable trackers in tests
		HandshakeTimeout:  1 * time.Second,
		RequestTimeout:    1 * time.Second,
		Debug:             false,
	}
}
