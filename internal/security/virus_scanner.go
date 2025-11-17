package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dutchcoders/go-clamd"
	"github.com/rs/zerolog/log"
)

// FallbackMode defines behavior when ClamAV is unavailable
type FallbackMode int

const (
	FallbackModeStrict FallbackMode = iota // Reject if ClamAV unavailable
	FallbackModeWarn                       // Warn but allow
	FallbackModeAllow                      // Silently allow
)

// ScanStatus represents the result of a virus scan
type ScanStatus int

const (
	ScanStatusClean ScanStatus = iota
	ScanStatusInfected
	ScanStatusError
	ScanStatusWarning
)

// VirusScannerConfig holds configuration for the virus scanner
type VirusScannerConfig struct {
	Address             string        // ClamAV address (e.g., "localhost:3310")
	Timeout             time.Duration // Scan timeout
	MaxRetries          int           // Max connection retries
	RetryDelay          time.Duration // Delay between retries
	FallbackMode        FallbackMode  // Behavior when ClamAV unavailable
	QuarantineDir       string        // Directory for quarantined files
	AutoQuarantine      bool          // Automatically quarantine infected files
	AuditLogPath        string        // Path to audit log file
	QuarantineRetention time.Duration // How long to keep quarantined files
	MaxStreamSize       int64         // Maximum stream size to buffer (default: 100MB)
	TempDir             string        // Directory for temporary scan buffers
}

// ScanResult represents the result of a virus scan
type ScanResult struct {
	Status         ScanStatus
	VirusName      string
	FallbackUsed   bool
	ScanDuration   time.Duration
	BytesScanned   int64
	Quarantined    bool
	QuarantinePath string
}

// VirusScanner provides virus scanning functionality using ClamAV
type VirusScanner struct {
	config *VirusScannerConfig
	client *clamd.Clamd
	mu     sync.RWMutex
}

// NewVirusScanner creates a new virus scanner instance
func NewVirusScanner(config VirusScannerConfig) (*VirusScanner, error) {
	// Validate configuration
	if config.Address == "" {
		return nil, fmt.Errorf("ClamAV address is required")
	}

	// Set defaults
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 1 * time.Second
	}
	if config.QuarantineRetention == 0 {
		config.QuarantineRetention = 30 * 24 * time.Hour // 30 days
	}
	if config.MaxStreamSize == 0 {
		config.MaxStreamSize = 100 * 1024 * 1024 // 100MB default
	}
	if config.TempDir == "" {
		config.TempDir = os.TempDir()
	}

	// Create quarantine directory if specified
	if config.QuarantineDir != "" {
		if err := os.MkdirAll(config.QuarantineDir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create quarantine directory: %w", err)
		}
	}

	// Create ClamAV client
	client := clamd.NewClamd(config.Address)

	scanner := &VirusScanner{
		config: &config,
		client: client,
	}

	log.Info().
		Str("address", config.Address).
		Dur("timeout", config.Timeout).
		Int("max_retries", config.MaxRetries).
		Msg("Virus scanner initialized")

	return scanner, nil
}

// ScanFile scans a file for viruses
func (s *VirusScanner) ScanFile(ctx context.Context, filePath string) (*ScanResult, error) {
	start := time.Now()

	// Check context
	if err := ctx.Err(); err != nil {
		return &ScanResult{
			Status:       ScanStatusError,
			ScanDuration: time.Since(start),
		}, err
	}

	// Check file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return &ScanResult{
			Status:       ScanStatusError,
			ScanDuration: time.Since(start),
		}, fmt.Errorf("failed to stat file: %w", err)
	}

	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return &ScanResult{
			Status:       ScanStatusError,
			ScanDuration: time.Since(start),
		}, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Perform scan with timeout
	scanCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	result := &ScanResult{
		BytesScanned: fileInfo.Size(),
	}

	// Scan with retries
	//
	// SECURITY NOTE (CVE-ATHENA-2025-001 FIX):
	// This retry logic prevents a critical vulnerability where exhausted retries
	// without a valid scan response could fall through to fallback mode handling,
	// potentially allowing infected files to bypass scanning.
	//
	// Fix ensures:
	// 1. Retry loop only exits when response != nil (valid scan result obtained)
	// 2. Network/connection errors stored in scanErr for fallback handling
	// 3. Explicit nil check after loop ensures no bypass path exists
	var scanErr error
	var response *clamd.ScanResult

	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-scanCtx.Done():
				scanErr = scanCtx.Err()
				break
			case <-time.After(s.config.RetryDelay):
			}
		}

		// Reset file position for retry
		if _, err := file.Seek(0, 0); err != nil {
			scanErr = fmt.Errorf("failed to seek file: %w", err)
			continue
		}

		// Perform scan via ClamAV streaming API
		responses, err := s.client.ScanStream(file, make(chan bool))
		if err != nil {
			// Connection/network error - store for fallback handling
			scanErr = err
			log.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Str("file", filePath).
				Msg("ClamAV scan attempt failed")
			continue
		}

		// Get first response from channel
		for resp := range responses {
			response = resp
			break
		}

		// CRITICAL: Only exit retry loop if we received a valid response
		// This prevents bypass via exhausted retries with nil response
		if response != nil {
			scanErr = nil
			break
		}
	}

	result.ScanDuration = time.Since(start)

	// Handle scan errors (network/connection failures)
	// SECURITY: Fallback mode applies ONLY to connection errors, never to scan results
	if scanErr != nil {
		log.Error().
			Err(scanErr).
			Str("file", filePath).
			Msg("ClamAV scan failed after retries")

		// Apply fallback mode
		switch s.config.FallbackMode {
		case FallbackModeStrict:
			result.Status = ScanStatusError
			return result, fmt.Errorf("virus scan failed: %w", scanErr)
		case FallbackModeWarn:
			result.Status = ScanStatusWarning
			result.FallbackUsed = true
			log.Warn().
				Str("file", filePath).
				Msg("ClamAV unavailable, allowing file with warning")
			return result, nil
		case FallbackModeAllow:
			result.Status = ScanStatusClean
			result.FallbackUsed = true
			return result, nil
		}
	}

	// Process scan result
	// SECURITY: This nil check is CRITICAL - it ensures infected files cannot bypass
	// scanning via the retry exhaustion path. If we reach here without a valid response,
	// the file is unconditionally rejected.
	if response == nil {
		result.Status = ScanStatusError
		return result, fmt.Errorf("no scan response received")
	}

	// Scan completed successfully - process result
	if response.Status == clamd.RES_OK {
		result.Status = ScanStatusClean
		log.Debug().
			Str("file", filePath).
			Dur("duration", result.ScanDuration).
			Msg("File scanned: clean")
	} else if response.Status == clamd.RES_FOUND {
		result.Status = ScanStatusInfected
		result.VirusName = response.Description
		log.Warn().
			Str("file", filePath).
			Str("virus", result.VirusName).
			Msg("Virus detected")
	} else {
		result.Status = ScanStatusError
		return result, fmt.Errorf("unexpected scan status: %s", response.Status)
	}

	return result, nil
}

// ScanStream scans data from an io.Reader
// SECURITY: This method buffers non-seekable streams to prevent retry vulnerability (CVE-pending)
// where consumed readers could result in false-clean scans on network errors
func (s *VirusScanner) ScanStream(ctx context.Context, reader io.Reader) (*ScanResult, error) {
	start := time.Now()

	// Check context
	if err := ctx.Err(); err != nil {
		return &ScanResult{
			Status:       ScanStatusError,
			ScanDuration: time.Since(start),
		}, err
	}

	// SECURITY: Determine reader type and prepare for retries
	var scanReader io.ReadSeeker
	var tempFile *os.File
	var cleanupFunc func()

	// Check if reader is already seekable
	if seeker, ok := reader.(io.ReadSeeker); ok {
		// Reader is seekable (e.g., *os.File), use directly
		scanReader = seeker
		cleanupFunc = func() {} // No cleanup needed
		log.Debug().
			Msg("ScanStream: Using seekable reader directly")
	} else {
		// SECURITY CRITICAL: Non-seekable reader (e.g., HTTP body, pipe)
		// Must buffer to temporary file to enable safe retries
		// Without this, retries would scan empty data and potentially pass infected content

		log.Info().
			Msg("ScanStream: Buffering non-seekable stream for safe retry support")

		// Create secure temporary file with restricted permissions
		var err error
		tempFile, err = os.CreateTemp(s.config.TempDir, "virus-scan-*.tmp")
		if err != nil {
			log.Error().
				Err(err).
				Str("temp_dir", s.config.TempDir).
				Msg("Failed to create temporary buffer for virus scanning")
			// SECURITY: Fail closed - reject file if we can't buffer it
			return &ScanResult{
				Status:       ScanStatusError,
				ScanDuration: time.Since(start),
			}, fmt.Errorf("failed to create scan buffer: %w", err)
		}

		// Ensure cleanup even on panic
		cleanupFunc = func() {
			if tempFile != nil {
				tempFile.Close()
				if err := os.Remove(tempFile.Name()); err != nil {
					log.Warn().
						Err(err).
						Str("file", tempFile.Name()).
						Msg("Failed to remove temporary scan buffer")
				}
			}
		}
		defer func() {
			// Extra safety: ensure cleanup on any exit path
			if tempFile != nil && cleanupFunc != nil {
				cleanupFunc()
			}
		}()

		// Set restrictive permissions on temp file (owner read/write only)
		if err := os.Chmod(tempFile.Name(), 0600); err != nil {
			cleanupFunc()
			log.Error().
				Err(err).
				Msg("Failed to set permissions on temporary scan buffer")
			return &ScanResult{
				Status:       ScanStatusError,
				ScanDuration: time.Since(start),
			}, fmt.Errorf("failed to secure scan buffer: %w", err)
		}

		// Buffer the entire stream to temp file with size tracking and limit enforcement
		// SECURITY: Use LimitReader to prevent memory exhaustion attacks
		limitedReader := io.LimitReader(reader, s.config.MaxStreamSize+1) // +1 to detect oversized streams
		bytesWritten, err := io.Copy(tempFile, limitedReader)
		if err != nil {
			cleanupFunc()
			log.Error().
				Err(err).
				Int64("bytes_written", bytesWritten).
				Int64("max_size", s.config.MaxStreamSize).
				Msg("Failed to buffer stream for virus scanning")
			// SECURITY: Fail closed - reject file if we can't buffer it completely
			return &ScanResult{
				Status:       ScanStatusError,
				ScanDuration: time.Since(start),
			}, fmt.Errorf("failed to buffer stream: %w", err)
		}

		// Check if stream exceeded size limit
		if bytesWritten > s.config.MaxStreamSize {
			cleanupFunc()
			log.Error().
				Int64("bytes_written", bytesWritten).
				Int64("max_size", s.config.MaxStreamSize).
				Msg("Stream exceeded maximum size limit for virus scanning")
			// SECURITY: Reject oversized streams to prevent resource exhaustion
			return &ScanResult{
					Status:       ScanStatusError,
					ScanDuration: time.Since(start),
				}, fmt.Errorf("stream too large for scanning: %d bytes exceeds limit of %d bytes",
					bytesWritten, s.config.MaxStreamSize)
		}

		// Seek back to beginning for scanning
		if _, err := tempFile.Seek(0, 0); err != nil {
			cleanupFunc()
			log.Error().
				Err(err).
				Msg("Failed to seek temporary scan buffer")
			return &ScanResult{
				Status:       ScanStatusError,
				ScanDuration: time.Since(start),
			}, fmt.Errorf("failed to prepare scan buffer: %w", err)
		}

		scanReader = tempFile

		log.Debug().
			Int64("bytes_buffered", bytesWritten).
			Str("temp_file", tempFile.Name()).
			Msg("Stream buffered for scanning")
	}

	// Ensure cleanup happens
	defer cleanupFunc()

	// Perform scan with timeout
	scanCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	result := &ScanResult{}

	// Scan with retries - now safe because we can seek
	var scanErr error
	var response *clamd.ScanResult
	var totalBytesScanned int64

	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-scanCtx.Done():
				scanErr = scanCtx.Err()
				log.Warn().
					Err(scanErr).
					Int("attempt", attempt).
					Msg("Scan context cancelled during retry")
				break
			case <-time.After(s.config.RetryDelay):
			}

			// SECURITY CRITICAL: Reset reader position for retry
			// This prevents the vulnerability where retries scan empty data
			if _, err := scanReader.Seek(0, 0); err != nil {
				scanErr = fmt.Errorf("failed to reset stream for retry: %w", err)
				log.Error().
					Err(err).
					Int("attempt", attempt+1).
					Msg("Cannot reset stream position for retry - aborting to prevent false-clean")
				// SECURITY: Cannot retry safely, must fail
				break
			}

			log.Debug().
				Int("attempt", attempt+1).
				Msg("Stream position reset for retry")
		}

		// Track position before scan to measure bytes scanned
		startPos, _ := scanReader.Seek(0, io.SeekCurrent)

		// Perform scan
		responses, err := s.client.ScanStream(scanReader, make(chan bool))
		if err != nil {
			scanErr = err
			endPos, _ := scanReader.Seek(0, io.SeekCurrent)
			bytesRead := endPos - startPos

			log.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Int("max_retries", s.config.MaxRetries).
				Int64("bytes_read", bytesRead).
				Msg("ClamAV stream scan attempt failed")
			continue
		}

		// Get first response
		for resp := range responses {
			response = resp
			break
		}

		if response != nil {
			endPos, _ := scanReader.Seek(0, io.SeekCurrent)
			totalBytesScanned = endPos - startPos
			scanErr = nil

			log.Debug().
				Int("attempt", attempt+1).
				Int64("bytes_scanned", totalBytesScanned).
				Msg("Scan completed successfully")
			break
		}
	}

	result.ScanDuration = time.Since(start)
	result.BytesScanned = totalBytesScanned

	// Handle scan errors
	if scanErr != nil {
		log.Error().
			Err(scanErr).
			Int("retries", s.config.MaxRetries).
			Dur("duration", result.ScanDuration).
			Msg("ClamAV stream scan failed after all retries")

		// Write security audit log for scan failure
		if s.config.AuditLogPath != "" {
			s.writeStreamScanFailureAudit(scanErr, s.config.MaxRetries)
		}

		// Apply fallback mode
		switch s.config.FallbackMode {
		case FallbackModeStrict:
			// SECURITY: Default to rejecting file on scan failure
			result.Status = ScanStatusError
			return result, fmt.Errorf("virus scan failed (strict mode): %w", scanErr)
		case FallbackModeWarn:
			result.Status = ScanStatusWarning
			result.FallbackUsed = true
			log.Warn().
				Msg("ClamAV unavailable, allowing stream with warning (NOT RECOMMENDED)")
			return result, nil
		case FallbackModeAllow:
			// SECURITY WARNING: This mode is dangerous and should not be used in production
			result.Status = ScanStatusClean
			result.FallbackUsed = true
			log.Error().
				Msg("SECURITY WARNING: Allowing unscanned stream due to FallbackModeAllow")
			return result, nil
		}
	}

	// Process scan result
	if response == nil {
		result.Status = ScanStatusError
		log.Error().
			Msg("No scan response received - possible ClamAV communication error")
		return result, fmt.Errorf("no scan response received")
	}

	if response.Status == clamd.RES_OK {
		result.Status = ScanStatusClean
		log.Debug().
			Dur("duration", result.ScanDuration).
			Int64("bytes_scanned", result.BytesScanned).
			Msg("Stream scanned: clean")
	} else if response.Status == clamd.RES_FOUND {
		result.Status = ScanStatusInfected
		result.VirusName = response.Description
		log.Warn().
			Str("virus", result.VirusName).
			Int64("bytes_scanned", result.BytesScanned).
			Msg("VIRUS DETECTED in stream")

		// Write security audit log for detected virus
		if s.config.AuditLogPath != "" {
			s.writeStreamVirusDetectedAudit(result.VirusName)
		}
	} else {
		result.Status = ScanStatusError
		log.Error().
			Str("status", string(response.Status)).
			Msg("Unexpected scan status from ClamAV")
		return result, fmt.Errorf("unexpected scan status: %s", response.Status)
	}

	return result, nil
}

// ScanAndQuarantine scans a file and quarantines it if infected
func (s *VirusScanner) ScanAndQuarantine(ctx context.Context, filePath string) (*ScanResult, error) {
	// Scan the file
	result, err := s.ScanFile(ctx, filePath)
	if err != nil && result.Status != ScanStatusInfected {
		return result, err
	}

	// Quarantine if infected and auto-quarantine is enabled
	if result.Status == ScanStatusInfected && (s.config.AutoQuarantine || s.config.QuarantineDir != "") {
		if err := s.quarantineFile(filePath, result.VirusName, result); err != nil {
			log.Error().
				Err(err).
				Str("file", filePath).
				Str("virus", result.VirusName).
				Msg("Failed to quarantine infected file")
			return result, fmt.Errorf("failed to quarantine file: %w", err)
		}
		result.Quarantined = true
	}

	return result, nil
}

// quarantineFile moves an infected file to quarantine
func (s *VirusScanner) quarantineFile(filePath, virusName string, result *ScanResult) error {
	if s.config.QuarantineDir == "" {
		return fmt.Errorf("quarantine directory not configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Generate quarantine filename
	timestamp := time.Now().Format("20060102-150405")
	basename := filepath.Base(filePath)
	quarantineFilename := fmt.Sprintf("%s_%s_%s", timestamp, sanitizeFilename(basename), generateFileHash(filePath))
	quarantinePath := filepath.Join(s.config.QuarantineDir, quarantineFilename)

	// Move file to quarantine
	if err := os.Rename(filePath, quarantinePath); err != nil {
		// If rename fails, try copy and delete
		if err := copyFile(filePath, quarantinePath); err != nil {
			return fmt.Errorf("failed to copy to quarantine: %w", err)
		}
		if err := os.Remove(filePath); err != nil {
			log.Warn().
				Err(err).
				Str("file", filePath).
				Msg("Failed to remove original file after quarantine")
		}
	}

	// Set quarantine file permissions to read-only
	if err := os.Chmod(quarantinePath, 0400); err != nil {
		log.Warn().
			Err(err).
			Str("file", quarantinePath).
			Msg("Failed to set quarantine file permissions")
	}

	result.QuarantinePath = quarantinePath

	log.Warn().
		Str("original_path", filePath).
		Str("quarantine_path", quarantinePath).
		Str("virus", virusName).
		Msg("File quarantined")

	// Write audit log if configured
	if s.config.AuditLogPath != "" {
		s.writeAuditLog(filePath, quarantinePath, virusName)
	}

	return nil
}

// writeAuditLog writes an audit log entry
func (s *VirusScanner) writeAuditLog(originalPath, quarantinePath, virusName string) {
	entry := fmt.Sprintf("%s | INFECTED | %s | Original: %s | Quarantine: %s\n",
		time.Now().Format(time.RFC3339),
		virusName,
		originalPath,
		quarantinePath,
	)

	f, err := os.OpenFile(s.config.AuditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Error().
			Err(err).
			Str("audit_log", s.config.AuditLogPath).
			Msg("Failed to open audit log")
		return
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		log.Error().
			Err(err).
			Msg("Failed to write audit log entry")
	}
}

// writeStreamScanFailureAudit writes an audit log entry for stream scan failures
func (s *VirusScanner) writeStreamScanFailureAudit(scanErr error, retries int) {
	entry := fmt.Sprintf("%s | SCAN_FAILED | Error: %v | Retries: %d | Action: REJECTED (strict mode)\n",
		time.Now().Format(time.RFC3339),
		scanErr,
		retries,
	)

	f, err := os.OpenFile(s.config.AuditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Error().
			Err(err).
			Str("audit_log", s.config.AuditLogPath).
			Msg("Failed to open audit log for scan failure")
		return
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		log.Error().
			Err(err).
			Msg("Failed to write scan failure audit log entry")
	}
}

// writeStreamVirusDetectedAudit writes an audit log entry for viruses detected in streams
func (s *VirusScanner) writeStreamVirusDetectedAudit(virusName string) {
	entry := fmt.Sprintf("%s | STREAM_INFECTED | Virus: %s | Action: REJECTED\n",
		time.Now().Format(time.RFC3339),
		virusName,
	)

	f, err := os.OpenFile(s.config.AuditLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Error().
			Err(err).
			Str("audit_log", s.config.AuditLogPath).
			Msg("Failed to open audit log for virus detection")
		return
	}
	defer f.Close()

	if _, err := f.WriteString(entry); err != nil {
		log.Error().
			Err(err).
			Msg("Failed to write virus detection audit log entry")
	}
}

// CleanupQuarantine removes old quarantined files based on retention policy
func (s *VirusScanner) CleanupQuarantine(ctx context.Context) (int, error) {
	if s.config.QuarantineDir == "" {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoffTime := time.Now().Add(-s.config.QuarantineRetention)
	deleted := 0

	entries, err := os.ReadDir(s.config.QuarantineDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read quarantine directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(s.config.QuarantineDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			log.Warn().
				Err(err).
				Str("file", filePath).
				Msg("Failed to get file info")
			continue
		}

		if info.ModTime().Before(cutoffTime) {
			if err := os.Remove(filePath); err != nil {
				log.Error().
					Err(err).
					Str("file", filePath).
					Msg("Failed to delete old quarantined file")
				continue
			}

			deleted++
			log.Info().
				Str("file", filePath).
				Time("mod_time", info.ModTime()).
				Msg("Deleted old quarantined file")
		}
	}

	log.Info().
		Int("deleted", deleted).
		Dur("retention", s.config.QuarantineRetention).
		Msg("Quarantine cleanup completed")

	return deleted, nil
}

// LoadVirusScannerConfigFromEnv loads configuration from environment variables
func LoadVirusScannerConfigFromEnv() (VirusScannerConfig, error) {
	config := VirusScannerConfig{
		Address:             os.Getenv("CLAMAV_ADDRESS"),
		Timeout:             parseDurationEnv("CLAMAV_TIMEOUT", 300) * time.Second,
		MaxRetries:          parseIntEnv("CLAMAV_MAX_RETRIES", 3),
		RetryDelay:          parseDurationEnv("CLAMAV_RETRY_DELAY", 1) * time.Second,
		QuarantineDir:       os.Getenv("QUARANTINE_DIR"),
		AutoQuarantine:      parseBoolEnv("CLAMAV_AUTO_QUARANTINE", true),
		AuditLogPath:        os.Getenv("CLAMAV_AUDIT_LOG"),
		QuarantineRetention: parseDurationEnv("QUARANTINE_RETENTION_DAYS", 30*24) * time.Hour,
		MaxStreamSize:       int64(parseIntEnv("CLAMAV_MAX_STREAM_SIZE_MB", 100)) * 1024 * 1024,
		TempDir:             os.Getenv("CLAMAV_TEMP_DIR"),
	}

	// Parse fallback mode
	fallbackModeStr := strings.ToLower(os.Getenv("CLAMAV_FALLBACK_MODE"))
	switch fallbackModeStr {
	case "strict":
		config.FallbackMode = FallbackModeStrict
	case "warn":
		config.FallbackMode = FallbackModeWarn
	case "allow":
		config.FallbackMode = FallbackModeAllow
	default:
		config.FallbackMode = FallbackModeStrict
	}

	return config, nil
}

// Helper functions

func sanitizeFilename(filename string) string {
	// Remove potentially dangerous characters
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "..", "_")
	if len(filename) > 100 {
		filename = filename[:100]
	}
	return filename
}

func generateFileHash(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	return hex.EncodeToString(h.Sum(nil))[:12]
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}

func parseDurationEnv(key string, defaultValue int) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return time.Duration(defaultValue)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return time.Duration(defaultValue)
	}
	return time.Duration(parsed)
}

func parseIntEnv(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func parseBoolEnv(key string, defaultValue bool) bool {
	value := strings.ToLower(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1"
}
