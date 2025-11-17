package security

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
