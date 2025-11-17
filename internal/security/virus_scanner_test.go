package security

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVirusScanner_Initialize tests scanner initialization
func TestVirusScanner_Initialize(t *testing.T) {
	tests := []struct {
		name    string
		config  VirusScannerConfig
		wantErr bool
	}{
		{
			name: "successful initialization with defaults",
			config: VirusScannerConfig{
				Address: "localhost:3310",
				Timeout: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "initialization with custom timeout",
			config: VirusScannerConfig{
				Address: "localhost:3310",
				Timeout: 10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "initialization with invalid address",
			config: VirusScannerConfig{
				Address: "",
				Timeout: 30 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner, err := NewVirusScanner(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, scanner)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, scanner)
			}
		})
	}
}

// TestVirusScanner_ConnectionFallback tests graceful degradation when ClamAV unavailable
func TestVirusScanner_ConnectionFallback(t *testing.T) {
	config := VirusScannerConfig{
		Address:          "localhost:9999", // Non-existent port
		Timeout:          5 * time.Second,
		FallbackMode:     FallbackModeWarn, // Warn but allow files
		MaxRetries:       3,
		RetryDelay:       1 * time.Second,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)
	require.NotNil(t, scanner)

	// Should allow file but log warning when ClamAV unavailable
	ctx := context.Background()
	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/clean_file.txt")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.FallbackUsed)
	assert.Equal(t, ScanStatusWarning, result.Status)
}

// TestVirusScanner_ConfigFromEnv tests configuration from environment variables
func TestVirusScanner_ConfigFromEnv(t *testing.T) {
	// Set environment variables
	os.Setenv("CLAMAV_ADDRESS", "localhost:3310")
	os.Setenv("CLAMAV_TIMEOUT", "60")
	os.Setenv("CLAMAV_FALLBACK_MODE", "strict")
	defer func() {
		os.Unsetenv("CLAMAV_ADDRESS")
		os.Unsetenv("CLAMAV_TIMEOUT")
		os.Unsetenv("CLAMAV_FALLBACK_MODE")
	}()

	config, err := LoadVirusScannerConfigFromEnv()
	require.NoError(t, err)

	assert.Equal(t, "localhost:3310", config.Address)
	assert.Equal(t, 60*time.Second, config.Timeout)
	assert.Equal(t, FallbackModeStrict, config.FallbackMode)
}

// TestVirusScanner_ScanCleanFile tests scanning a clean file
func TestVirusScanner_ScanCleanFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/clean_file.txt")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ScanStatusClean, result.Status)
	assert.Empty(t, result.VirusName)
	assert.False(t, result.FallbackUsed)
	assert.Greater(t, result.ScanDuration, time.Duration(0))
}

// TestVirusScanner_DetectEICAR tests EICAR test virus detection
func TestVirusScanner_DetectEICAR(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/eicar.txt")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ScanStatusInfected, result.Status)
	assert.Contains(t, result.VirusName, "EICAR")
	assert.False(t, result.FallbackUsed)
}

// TestVirusScanner_DetectEICARStream tests EICAR detection from stream
func TestVirusScanner_DetectEICARStream(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// EICAR test string
	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	reader := bytes.NewReader([]byte(eicarString))

	result, err := scanner.ScanStream(ctx, reader)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ScanStatusInfected, result.Status)
	assert.Contains(t, result.VirusName, "EICAR")
}

// ====================================================================================
// CRITICAL P1 SECURITY TESTS FOR RETRY LOGIC VULNERABILITY
// ====================================================================================
// These tests verify that the fix for the exhausted reader vulnerability is working.
// An infected file must NEVER be marked as clean due to retry logic issues.
// ====================================================================================

// exhaustedReader simulates a non-seekable reader that becomes exhausted after first read
type exhaustedReader struct {
	data       []byte
	readCount  int
	exhausted  bool
	shouldFail bool
}

func newExhaustedReader(data []byte, shouldFail bool) *exhaustedReader {
	return &exhaustedReader{
		data:       data,
		readCount:  0,
		shouldFail: shouldFail,
	}
}

func (r *exhaustedReader) Read(p []byte) (n int, err error) {
	r.readCount++

	// Simulate network failure on first attempt if configured
	if r.shouldFail && r.readCount == 1 {
		// Read some data, then fail mid-stream
		if len(r.data) > 0 {
			n = copy(p, r.data[:len(r.data)/2])
			r.data = r.data[n:]
			return n, fmt.Errorf("simulated network error mid-read")
		}
		return 0, fmt.Errorf("simulated network error")
	}

	// If already exhausted (second+ read), return EOF
	if r.exhausted {
		return 0, io.EOF
	}

	// First successful read - consume all data
	if len(r.data) == 0 {
		r.exhausted = true
		return 0, io.EOF
	}

	n = copy(p, r.data)
	r.data = r.data[n:]

	if len(r.data) == 0 {
		r.exhausted = true
		err = io.EOF
	}

	return n, err
}

// TestVirusScanner_ExhaustedReaderVulnerability tests the critical vulnerability where
// retry logic could allow infected files to bypass scanning by reading an exhausted stream
func TestVirusScanner_ExhaustedReaderVulnerability(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// CRITICAL: EICAR test file that should ALWAYS be detected as infected
	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

	// Simulate a non-seekable reader (like HTTP request body) that will fail on retry
	reader := newExhaustedReader([]byte(eicarString), true)

	result, err := scanner.ScanStream(ctx, reader)

	// CRITICAL SECURITY ASSERTION: An infected file must NEVER be marked as clean
	// If the retry logic reuses an exhausted reader, ClamAV receives 0 bytes and returns clean
	// This would be a critical security vulnerability
	if result != nil && result.Status == ScanStatusClean {
		t.Fatalf("CRITICAL SECURITY VULNERABILITY: Infected EICAR file marked as CLEAN! "+
			"This indicates exhausted reader was scanned as empty file. "+
			"Status=%v, VirusName=%q, FallbackUsed=%v, Error=%v",
			result.Status, result.VirusName, result.FallbackUsed, err)
	}

	// The fix should either:
	// 1. Detect the virus correctly (ScanStatusInfected)
	// 2. Return an error indicating inability to retry non-seekable reader
	// 3. Buffer the stream and successfully detect the virus
	// But it must NEVER return ScanStatusClean for infected content

	if err != nil {
		// If error, it should be a scan error, not a false clean result
		assert.Equal(t, ScanStatusError, result.Status,
			"If scan fails, status must be Error, not Clean")
		t.Logf("Expected behavior: scan failed with error (safe): %v", err)
	} else {
		// If no error, virus must be detected
		assert.Equal(t, ScanStatusInfected, result.Status,
			"EICAR must be detected as infected")
		assert.Contains(t, result.VirusName, "EICAR",
			"Virus name must indicate EICAR detection")
		t.Logf("Expected behavior: virus detected correctly")
	}
}

// TestVirusScanner_NonSeekableReaderRetry tests retry behavior with non-seekable readers
func TestVirusScanner_NonSeekableReaderRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create scanner with retry enabled
	config := VirusScannerConfig{
		Address:      "localhost:3310",
		Timeout:      30 * time.Second,
		MaxRetries:   3,
		RetryDelay:   100 * time.Millisecond,
		FallbackMode: FallbackModeStrict, // Must fail on error, not allow
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name            string
		data            []byte
		expectInfected  bool
		allowError      bool
		description     string
	}{
		{
			name:           "infected_eicar_non_seekable",
			data:           []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`),
			expectInfected: true,
			allowError:     true, // May error on non-seekable, but must not return clean
			description:    "EICAR in non-seekable reader must never be marked clean",
		},
		{
			name:           "clean_data_non_seekable",
			data:           []byte("This is clean text data for testing purposes"),
			expectInfected: false,
			allowError:     true,
			description:    "Clean data in non-seekable reader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use exhausted reader that fails on first attempt
			reader := newExhaustedReader(tt.data, true)

			result, err := scanner.ScanStream(ctx, reader)

			t.Logf("Test: %s - Result: status=%v, virus=%q, err=%v",
				tt.description, result.Status, result.VirusName, err)

			if tt.expectInfected {
				// CRITICAL: Infected content must NEVER be marked clean
				if result != nil {
					assert.NotEqual(t, ScanStatusClean, result.Status,
						"CRITICAL: Infected content must never be marked clean")
				}

				// Either detect virus or fail with error (both are safe)
				if err == nil {
					assert.Equal(t, ScanStatusInfected, result.Status,
						"If no error, infected content must be detected")
					assert.Contains(t, result.VirusName, "EICAR")
				} else if tt.allowError {
					// Error is acceptable for non-seekable readers
					if result != nil {
						assert.Equal(t, ScanStatusError, result.Status,
							"On error, status must be Error")
					}
				} else {
					t.Errorf("Unexpected error: %v", err)
				}
			} else {
				// Clean content: can be clean, error, or warning
				// But if result is clean, we need to ensure it was actually scanned
				if result != nil && result.Status == ScanStatusClean {
					t.Logf("Clean file correctly identified")
				}
			}
		})
	}
}

// TestVirusScanner_SeekableReaderRetrySuccess tests that seekable readers (files) can retry
func TestVirusScanner_SeekableReaderRetrySuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Create a temporary file (seekable)
	tmpFile, err := os.CreateTemp("", "virus_test_*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write EICAR to file
	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	_, err = tmpFile.WriteString(eicarString)
	require.NoError(t, err)

	// Reset to beginning
	_, err = tmpFile.Seek(0, 0)
	require.NoError(t, err)

	// ScanFile should handle retries correctly because files are seekable
	result, err := scanner.ScanFile(ctx, tmpFile.Name())
	require.NoError(t, err)
	require.NotNil(t, result)

	// Seekable files should always detect virus correctly
	assert.Equal(t, ScanStatusInfected, result.Status,
		"Seekable file (os.File) should detect virus on retry")
	assert.Contains(t, result.VirusName, "EICAR")
}

// TestVirusScanner_ZeroByteStream tests scanning zero-byte streams
func TestVirusScanner_ZeroByteStream(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Zero-byte reader
	reader := bytes.NewReader([]byte{})

	result, err := scanner.ScanStream(ctx, reader)

	// Zero-byte stream should be clean or error, never infected
	if err == nil {
		require.NotNil(t, result)
		assert.NotEqual(t, ScanStatusInfected, result.Status,
			"Zero-byte stream cannot be infected")
	}
}

// TestVirusScanner_StreamFailsMidRead tests stream that fails during read
func TestVirusScanner_StreamFailsMidRead(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Reader that fails mid-stream
	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	reader := &failingReader{
		data:      []byte(eicarString),
		failAfter: 10, // Fail after 10 bytes
	}

	result, err := scanner.ScanStream(ctx, reader)

	// Should error (fail-safe behavior)
	if result != nil {
		// CRITICAL: Must never mark as clean if read failed
		assert.NotEqual(t, ScanStatusClean, result.Status,
			"Partial read of infected content must not be marked clean")
	}

	// Error is expected and safe
	t.Logf("Mid-stream failure handled safely: err=%v, status=%v",
		err, result.Status)
}

// failingReader fails after reading a certain number of bytes
type failingReader struct {
	data      []byte
	failAfter int
	bytesRead int
}

func (r *failingReader) Read(p []byte) (n int, err error) {
	if r.bytesRead >= r.failAfter {
		return 0, fmt.Errorf("simulated read failure")
	}

	remaining := r.failAfter - r.bytesRead
	toRead := len(p)
	if toRead > remaining {
		toRead = remaining
	}
	if toRead > len(r.data) {
		toRead = len(r.data)
	}

	n = copy(p, r.data[:toRead])
	r.data = r.data[n:]
	r.bytesRead += n

	if r.bytesRead >= r.failAfter {
		return n, fmt.Errorf("simulated read failure")
	}

	if len(r.data) == 0 {
		return n, io.EOF
	}

	return n, nil
}

// TestVirusScanner_ConcurrentStreamScans tests concurrent stream scanning
func TestVirusScanner_ConcurrentStreamScans(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	cleanData := []byte("This is clean test data")

	concurrency := 10
	var wg sync.WaitGroup
	results := make(chan *ScanResult, concurrency)
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			var reader io.Reader
			expectInfected := (id % 2 == 0)

			if expectInfected {
				reader = bytes.NewReader([]byte(eicarString))
			} else {
				reader = bytes.NewReader(cleanData)
			}

			result, err := scanner.ScanStream(ctx, reader)
			if err != nil {
				errors <- err
				return
			}

			// CRITICAL: Check infected files are not marked clean
			if expectInfected && result.Status == ScanStatusClean {
				errors <- fmt.Errorf("concurrent scan %d: CRITICAL - infected marked as clean", id)
				return
			}

			results <- result
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	// Check for critical errors
	for err := range errors {
		if strings.Contains(err.Error(), "CRITICAL") {
			t.Fatalf("Critical security violation in concurrent scan: %v", err)
		}
		t.Logf("Concurrent scan error (may be acceptable): %v", err)
	}

	// Verify results
	for result := range results {
		assert.NotNil(t, result)
		// Results should be either infected or clean, not error for simple streams
		t.Logf("Concurrent scan result: status=%v, virus=%q",
			result.Status, result.VirusName)
	}
}

// TestVirusScanner_LargeStreamMemoryUsage tests memory usage for large streams
func TestVirusScanner_LargeStreamMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Create large clean data (10MB)
	largeData := make([]byte, 10*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	reader := bytes.NewReader(largeData)
	result, err := scanner.ScanStream(ctx, reader)

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	if err == nil {
		require.NotNil(t, result)
	}

	memIncrease := m2.Alloc - m1.Alloc
	t.Logf("Memory increase for 10MB stream: %d bytes (%.2f MB)",
		memIncrease, float64(memIncrease)/(1024*1024))

	// If buffering is used, memory should be reasonable
	// Allow up to 50MB for a 10MB file (5x overhead for buffering, processing)
	assert.Less(t, memIncrease, uint64(50*1024*1024),
		"Memory usage should be reasonable even with buffering")
}

// TestVirusScanner_NetworkErrorRetry tests retry behavior on network errors
func TestVirusScanner_NetworkErrorRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Scanner with short timeout to simulate network issues
	config := VirusScannerConfig{
		Address:      "localhost:3310",
		Timeout:      30 * time.Second,
		MaxRetries:   2,
		RetryDelay:   100 * time.Millisecond,
		FallbackMode: FallbackModeStrict,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Use network error simulator
	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	reader := &networkErrorReader{
		data:         []byte(eicarString),
		failAttempts: 1, // Fail once, then succeed
	}

	result, err := scanner.ScanStream(ctx, reader)

	// After retry, should either detect virus or fail safely
	if err == nil {
		require.NotNil(t, result)
		// If scan succeeded after retry, infected file must be detected
		if reader.attemptCount > 1 {
			assert.Equal(t, ScanStatusInfected, result.Status,
				"After successful retry, virus must be detected")
		}
	} else {
		// Error is safe - fails closed
		t.Logf("Network error caused safe failure: %v", err)
	}
}

// networkErrorReader simulates transient network errors
type networkErrorReader struct {
	data         []byte
	failAttempts int
	attemptCount int
	mu           sync.Mutex
}

func (r *networkErrorReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	r.attemptCount++
	currentAttempt := r.attemptCount
	r.mu.Unlock()

	// Fail on early attempts
	if currentAttempt <= r.failAttempts {
		return 0, fmt.Errorf("simulated network timeout")
	}

	// Succeed on later attempts
	if len(r.data) == 0 {
		return 0, io.EOF
	}

	n = copy(p, r.data)
	r.data = r.data[n:]

	if len(r.data) == 0 {
		return n, io.EOF
	}

	return n, nil
}

// TestVirusScanner_DifferentErrorTypes tests handling of different error types
func TestVirusScanner_DifferentErrorTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	eicarString := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`

	tests := []struct {
		name        string
		reader      io.Reader
		description string
	}{
		{
			name:        "timeout_error",
			reader:      &timeoutReader{data: []byte(eicarString)},
			description: "Reader that times out",
		},
		{
			name:        "eof_error",
			reader:      &eofReader{},
			description: "Reader that immediately returns EOF",
		},
		{
			name:        "permission_error",
			reader:      &permissionErrorReader{data: []byte(eicarString)},
			description: "Reader that simulates permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scanner.ScanStream(ctx, tt.reader)

			t.Logf("Test %s: err=%v, status=%v", tt.description, err, result.Status)

			// CRITICAL: Never mark as clean on error
			if result != nil && result.Status == ScanStatusClean {
				// Only allow clean if there was no error
				assert.NoError(t, err, "Clean status requires no error")
			}

			// Errors should result in Error status
			if err != nil && result != nil {
				assert.Equal(t, ScanStatusError, result.Status,
					"Errors should result in Error status")
			}
		})
	}
}

// Test helper readers for different error scenarios

type timeoutReader struct {
	data []byte
}

func (r *timeoutReader) Read(p []byte) (n int, err error) {
	time.Sleep(100 * time.Millisecond)
	return 0, context.DeadlineExceeded
}

type eofReader struct{}

func (r *eofReader) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

type permissionErrorReader struct {
	data []byte
}

func (r *permissionErrorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("permission denied")
}

// ====================================================================================
// BUSINESS LOGIC VALIDATION TESTS
// ====================================================================================
// These tests ensure the fix maintains correct business logic
// ====================================================================================

// TestVirusScanner_BusinessLogic_CleanFilesPassthrough ensures clean files still pass
func TestVirusScanner_BusinessLogic_CleanFilesPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	cleanData := []byte("This is completely safe, clean text data for testing")

	tests := []struct {
		name   string
		reader io.Reader
	}{
		{
			name:   "bytes_reader",
			reader: bytes.NewReader(cleanData),
		},
		{
			name:   "strings_reader",
			reader: strings.NewReader(string(cleanData)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scanner.ScanStream(ctx, tt.reader)

			// Clean files should pass (or error safely, but not be marked infected)
			if err == nil {
				require.NotNil(t, result)
				assert.NotEqual(t, ScanStatusInfected, result.Status,
					"Clean data must not be marked as infected")
			}
		})
	}
}

// TestVirusScanner_BusinessLogic_InfectedFilesBlocked ensures infected files are blocked
func TestVirusScanner_BusinessLogic_InfectedFilesBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Multiple EICAR variants
	eicarVariants := []string{
		`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`,
		// Add more variants if needed
	}

	for i, eicar := range eicarVariants {
		t.Run(fmt.Sprintf("eicar_variant_%d", i), func(t *testing.T) {
			reader := bytes.NewReader([]byte(eicar))
			result, err := scanner.ScanStream(ctx, reader)

			// Infected files must be detected or fail (never pass as clean)
			if result != nil {
				assert.NotEqual(t, ScanStatusClean, result.Status,
					"CRITICAL: EICAR must never be marked clean")
			}

			if err == nil {
				require.NotNil(t, result)
				assert.Equal(t, ScanStatusInfected, result.Status,
					"EICAR must be detected as infected")
				assert.Contains(t, strings.ToUpper(result.VirusName), "EICAR",
					"Virus name must indicate EICAR")
			}
		})
	}
}

// TestVirusScanner_BusinessLogic_ErrorHandlingConsistency ensures errors are handled consistently
func TestVirusScanner_BusinessLogic_ErrorHandlingConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := VirusScannerConfig{
		Address:      "localhost:3310",
		Timeout:      30 * time.Second,
		MaxRetries:   3,
		RetryDelay:   100 * time.Millisecond,
		FallbackMode: FallbackModeStrict, // Strict mode for security
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Test that errors in strict mode prevent processing
	reader := &alwaysFailReader{}
	result, err := scanner.ScanStream(ctx, reader)

	// In strict mode, should error
	if config.FallbackMode == FallbackModeStrict {
		// Either err != nil or status == Error
		if err == nil {
			require.NotNil(t, result)
			assert.Equal(t, ScanStatusError, result.Status,
				"Strict mode should return error status on scan failure")
		}

		// CRITICAL: Must never return clean on failure
		if result != nil {
			assert.NotEqual(t, ScanStatusClean, result.Status,
				"Strict mode must never mark as clean on error")
		}
	}
}

type alwaysFailReader struct{}

func (r *alwaysFailReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("intentional failure for testing")
}

// TestVirusScanner_ScanLargeFile tests scanning large files with streaming
func TestVirusScanner_ScanLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Scan 100MB clean file
	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/large_clean.bin")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ScanStatusClean, result.Status)
	assert.Greater(t, result.BytesScanned, int64(100*1024*1024))
}

// TestVirusScanner_ConcurrentScans tests multiple concurrent scans
func TestVirusScanner_ConcurrentScans(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	concurrency := 10
	var wg sync.WaitGroup
	results := make(chan *ScanResult, concurrency)
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			filePath := "../../testdata/virus_scanner/clean_file.txt"
			if id%2 == 0 {
				filePath = "../../testdata/virus_scanner/eicar.txt"
			}

			result, err := scanner.ScanFile(ctx, filePath)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	// Check no errors occurred
	for err := range errors {
		t.Errorf("Concurrent scan error: %v", err)
	}

	// Verify all results
	cleanCount := 0
	infectedCount := 0
	for result := range results {
		if result.Status == ScanStatusClean {
			cleanCount++
		} else if result.Status == ScanStatusInfected {
			infectedCount++
		}
	}

	assert.Equal(t, 5, cleanCount)
	assert.Equal(t, 5, infectedCount)
}

// TestVirusScanner_ScanTimeout tests scan timeout handling
func TestVirusScanner_ScanTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := VirusScannerConfig{
		Address: "localhost:3310",
		Timeout: 100 * time.Millisecond, // Very short timeout
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Try to scan large file with short timeout
	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/large_clean.bin")

	// Should timeout
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")

	if result != nil {
		assert.Equal(t, ScanStatusError, result.Status)
	}
}

// TestVirusScanner_ContextCancellation tests context cancellation
func TestVirusScanner_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/clean_file.txt")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")

	if result != nil {
		assert.Equal(t, ScanStatusError, result.Status)
	}
}

// TestVirusScanner_QuarantineInfected tests quarantine of infected files
func TestVirusScanner_QuarantineInfected(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	quarantineDir := t.TempDir()

	config := VirusScannerConfig{
		Address:        "localhost:3310",
		Timeout:        30 * time.Second,
		QuarantineDir:  quarantineDir,
		AutoQuarantine: true,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Copy EICAR to temp location (so we can move it)
	tempFile := filepath.Join(t.TempDir(), "test_eicar.txt")
	copyTestFile(t, "../../testdata/virus_scanner/eicar.txt", tempFile)

	result, err := scanner.ScanAndQuarantine(ctx, tempFile)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, ScanStatusInfected, result.Status)
	assert.True(t, result.Quarantined)
	assert.NotEmpty(t, result.QuarantinePath)

	// Original file should be moved
	_, err = os.Stat(tempFile)
	assert.True(t, os.IsNotExist(err))

	// File should exist in quarantine
	_, err = os.Stat(result.QuarantinePath)
	assert.NoError(t, err)
}

// TestVirusScanner_QuarantinePermissions tests quarantine directory permissions
func TestVirusScanner_QuarantinePermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	quarantineDir := t.TempDir()

	config := VirusScannerConfig{
		Address:        "localhost:3310",
		Timeout:        30 * time.Second,
		QuarantineDir:  quarantineDir,
		AutoQuarantine: true,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)
	require.NotNil(t, scanner)

	// Check quarantine directory has restricted permissions (0700)
	info, err := os.Stat(quarantineDir)
	require.NoError(t, err)

	mode := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0700), mode, "Quarantine directory should have 0700 permissions")
}

// TestVirusScanner_QuarantineAuditLog tests quarantine audit trail
func TestVirusScanner_QuarantineAuditLog(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	quarantineDir := t.TempDir()
	auditLogPath := filepath.Join(quarantineDir, "audit.log")

	config := VirusScannerConfig{
		Address:        "localhost:3310",
		Timeout:        30 * time.Second,
		QuarantineDir:  quarantineDir,
		AutoQuarantine: true,
		AuditLogPath:   auditLogPath,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	ctx := context.Background()

	// Scan infected file
	tempFile := filepath.Join(t.TempDir(), "test_eicar.txt")
	copyTestFile(t, "../../testdata/virus_scanner/eicar.txt", tempFile)

	result, err := scanner.ScanAndQuarantine(ctx, tempFile)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify audit log exists and contains entry
	_, err = os.Stat(auditLogPath)
	assert.NoError(t, err)

	content, err := os.ReadFile(auditLogPath)
	require.NoError(t, err)

	auditEntry := string(content)
	assert.Contains(t, auditEntry, "EICAR")
	assert.Contains(t, auditEntry, "INFECTED")
	assert.Contains(t, auditEntry, tempFile)
}

// TestVirusScanner_QuarantineCleanup tests cleanup of old quarantined files
func TestVirusScanner_QuarantineCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	quarantineDir := t.TempDir()

	config := VirusScannerConfig{
		Address:             "localhost:3310",
		Timeout:             30 * time.Second,
		QuarantineDir:       quarantineDir,
		QuarantineRetention: 24 * time.Hour,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)

	// Create old quarantined file (simulate)
	oldFile := filepath.Join(quarantineDir, "old_virus.txt")
	err = os.WriteFile(oldFile, []byte("old virus"), 0600)
	require.NoError(t, err)

	// Set modification time to 2 days ago
	oldTime := time.Now().Add(-48 * time.Hour)
	err = os.Chtimes(oldFile, oldTime, oldTime)
	require.NoError(t, err)

	// Create recent quarantined file
	recentFile := filepath.Join(quarantineDir, "recent_virus.txt")
	err = os.WriteFile(recentFile, []byte("recent virus"), 0600)
	require.NoError(t, err)

	// Run cleanup
	deleted, err := scanner.CleanupQuarantine(context.Background())
	require.NoError(t, err)

	// Old file should be deleted
	_, err = os.Stat(oldFile)
	assert.True(t, os.IsNotExist(err))

	// Recent file should still exist
	_, err = os.Stat(recentFile)
	assert.NoError(t, err)

	assert.Equal(t, 1, deleted)
}

// TestVirusScanner_IntegrationWithUpload tests upload workflow integration
func TestVirusScanner_IntegrationWithUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	tests := []struct {
		name           string
		filePath       string
		expectApproval bool
		expectVirus    bool
	}{
		{
			name:           "clean video file approved",
			filePath:       "../../testdata/virus_scanner/clean_video.mp4",
			expectApproval: true,
			expectVirus:    false,
		},
		{
			name:           "infected file rejected",
			filePath:       "../../testdata/virus_scanner/eicar.txt",
			expectApproval: false,
			expectVirus:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scanner.ScanFile(ctx, tt.filePath)
			require.NoError(t, err)

			if tt.expectVirus {
				assert.Equal(t, ScanStatusInfected, result.Status)
			} else {
				assert.Equal(t, ScanStatusClean, result.Status)
			}

			approved := result.Status == ScanStatusClean
			assert.Equal(t, tt.expectApproval, approved)
		})
	}
}

// TestVirusScanner_BeforeFFmpegProcessing tests scan before FFmpeg
func TestVirusScanner_BeforeFFmpegProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Simulate upload workflow: scan -> FFmpeg
	filePath := "../../testdata/virus_scanner/clean_video.mp4"

	// Step 1: Scan
	result, err := scanner.ScanFile(ctx, filePath)
	require.NoError(t, err)
	require.Equal(t, ScanStatusClean, result.Status)

	// Step 2: Only proceed to FFmpeg if clean
	if result.Status == ScanStatusClean {
		// FFmpeg processing would happen here
		assert.True(t, true, "Should proceed to FFmpeg")
	} else {
		t.Fatal("Should not reach FFmpeg with infected file")
	}
}

// TestVirusScanner_BeforeIPFSPinning tests scan before IPFS
func TestVirusScanner_BeforeIPFSPinning(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Simulate: scan -> IPFS pin
	filePath := "../../testdata/virus_scanner/clean_file.txt"

	// Step 1: Scan
	result, err := scanner.ScanFile(ctx, filePath)
	require.NoError(t, err)

	// Step 2: Only pin to IPFS if clean
	if result.Status == ScanStatusClean {
		// IPFS pinning would happen here
		assert.True(t, true, "Should proceed to IPFS")
	} else {
		t.Fatal("Should not pin infected file to IPFS")
	}
}

// TestVirusScanner_UserNotification tests malware detection notification
func TestVirusScanner_UserNotification(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	result, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/eicar.txt")
	require.NoError(t, err)
	require.Equal(t, ScanStatusInfected, result.Status)

	// Build notification
	notification := buildMalwareNotification(result)

	assert.NotNil(t, notification)
	assert.Contains(t, notification.Message, "malware")
	assert.Contains(t, notification.Message, result.VirusName)
	assert.Equal(t, "security_alert", notification.Type)
}

// Benchmark tests

// BenchmarkVirusScanner_ScanSmallFile benchmarks small file scanning
func BenchmarkVirusScanner_ScanSmallFile(b *testing.B) {
	scanner := setupScannerForBenchmark(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/clean_file.txt")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVirusScanner_ScanLargeFile benchmarks large file scanning
func BenchmarkVirusScanner_ScanLargeFile(b *testing.B) {
	scanner := setupScannerForBenchmark(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/large_clean.bin")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVirusScanner_ConcurrentScans benchmarks concurrent scanning throughput
func BenchmarkVirusScanner_ConcurrentScans(b *testing.B) {
	scanner := setupScannerForBenchmark(b)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/clean_file.txt")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestVirusScanner_MemoryUsage tests memory usage during large file scans
func TestVirusScanner_MemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	scanner := setupScanner(t)
	ctx := context.Background()

	// Get initial memory stats
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Scan large file
	_, err := scanner.ScanFile(ctx, "../../testdata/virus_scanner/large_clean.bin")
	require.NoError(t, err)

	// Get final memory stats
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// Memory increase should be reasonable (< 50MB for 100MB file due to streaming)
	memIncrease := m2.Alloc - m1.Alloc
	assert.Less(t, memIncrease, uint64(50*1024*1024),
		"Memory usage should be < 50MB for 100MB file scan (streaming)")
}

// Helper functions

func setupScanner(t *testing.T) *VirusScanner {
	t.Helper()

	config := VirusScannerConfig{
		Address:        "localhost:3310",
		Timeout:        30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
		FallbackMode:   FallbackModeStrict,
	}

	scanner, err := NewVirusScanner(config)
	require.NoError(t, err)
	require.NotNil(t, scanner)

	return scanner
}

func setupScannerForBenchmark(b *testing.B) *VirusScanner {
	b.Helper()

	config := VirusScannerConfig{
		Address: "localhost:3310",
		Timeout: 30 * time.Second,
	}

	scanner, err := NewVirusScanner(config)
	if err != nil {
		b.Fatal(err)
	}

	return scanner
}

func copyTestFile(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	require.NoError(t, err)

	err = os.WriteFile(dst, data, 0644)
	require.NoError(t, err)
}

// Test helper types

type Notification struct {
	Type    string
	Message string
}

func buildMalwareNotification(result *ScanResult) *Notification {
	if result.Status != ScanStatusInfected {
		return nil
	}

	return &Notification{
		Type:    "security_alert",
		Message: fmt.Sprintf("Malware detected: %s", result.VirusName),
	}
}
