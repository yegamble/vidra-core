package torrent

import (
	"os"
	"testing"
)

// TestMain runs before all tests in this package
func TestMain(m *testing.M) {
	// Setup test environment
	setup()

	// Run tests
	code := m.Run()

	// Cleanup
	cleanup()

	// Exit with test result code
	os.Exit(code)
}

func setup() {
	// Set environment variables for testing to avoid external network calls
	os.Setenv("TORRENT_DISABLE_DHT", "true")
	os.Setenv("TORRENT_DISABLE_PEX", "true")
	os.Setenv("TORRENT_DISABLE_TRACKERS", "true")
}

func cleanup() {
	// Clean up test directories
	os.RemoveAll("/tmp/test-torrents")
}
