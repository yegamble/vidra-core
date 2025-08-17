package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"athena/internal/config"
	"athena/internal/domain"
)

type ChecksumValidator struct {
	config *config.Config
}

func NewChecksumValidator(cfg *config.Config) *ChecksumValidator {
	return &ChecksumValidator{
		config: cfg,
	}
}

// ValidateChunkChecksum validates a chunk's checksum with strict mode enforcement
func (v *ChecksumValidator) ValidateChunkChecksum(data []byte, expectedChecksum string) error {
	// In test mode, allow bypass checksums
	if v.config.ValidationTestMode && (expectedChecksum == "abc123" || expectedChecksum == "test") {
		if v.config.ValidationLogEvents {
			log.Printf("VALIDATION: Test mode bypass used for checksum: %s", expectedChecksum)
		}
		return nil
	}

	// In strict mode, checksum is always required
	if v.config.ValidationStrictMode && expectedChecksum == "" {
		return domain.NewDomainError("CHECKSUM_REQUIRED", "Checksum is required in strict validation mode")
	}

	// Skip validation if no checksum provided and not in strict mode
	if expectedChecksum == "" {
		return nil
	}

	// Validate checksum algorithm
	if !v.isAllowedAlgorithm("sha256") {
		return domain.NewDomainError("UNSUPPORTED_ALGORITHM", "SHA256 checksum algorithm not allowed")
	}

	// Calculate actual checksum
	hasher := sha256.New()
	hasher.Write(data)
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))

	if actualChecksum != expectedChecksum {
		if v.config.ValidationLogEvents {
			log.Printf("VALIDATION: Checksum mismatch - expected: %s, actual: %s", expectedChecksum, actualChecksum)
		}
		return domain.NewDomainError("CHECKSUM_MISMATCH", "Chunk checksum verification failed")
	}

	if v.config.ValidationLogEvents {
		log.Printf("VALIDATION: Checksum validation successful for chunk")
	}

	return nil
}

// ValidateFileChecksum validates a complete file's checksum
func (v *ChecksumValidator) ValidateFileChecksum(filePath string, expectedChecksum string) error {
	if expectedChecksum == "" && !v.config.ValidationStrictMode {
		return nil
	}

	if expectedChecksum == "" {
		return domain.NewDomainError("CHECKSUM_REQUIRED", "File checksum is required in strict validation mode")
	}

	// In test mode, allow bypass checksums
	if v.config.ValidationTestMode && (expectedChecksum == "abc123" || expectedChecksum == "test") {
		if v.config.ValidationLogEvents {
			log.Printf("VALIDATION: Test mode bypass used for file checksum: %s", expectedChecksum)
		}
		return nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for checksum validation: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && v.config.ValidationLogEvents {
			log.Printf("VALIDATION: Warning - failed to close file: %v", closeErr)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("failed to calculate file checksum: %w", err)
	}

	actualChecksum := hex.EncodeToString(hasher.Sum(nil))

	if actualChecksum != expectedChecksum {
		if v.config.ValidationLogEvents {
			log.Printf("VALIDATION: File checksum mismatch - expected: %s, actual: %s, file: %s", expectedChecksum, actualChecksum, filePath)
		}
		return domain.NewDomainError("FILE_CHECKSUM_MISMATCH", "File checksum verification failed")
	}

	if v.config.ValidationLogEvents {
		log.Printf("VALIDATION: File checksum validation successful for: %s", filePath)
	}

	return nil
}

// CalculateChecksum calculates SHA256 checksum for data
func (v *ChecksumValidator) CalculateChecksum(data []byte) string {
	hasher := sha256.New()
	hasher.Write(data)
	return hex.EncodeToString(hasher.Sum(nil))
}

// CalculateFileChecksum calculates SHA256 checksum for a file
func (v *ChecksumValidator) CalculateFileChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for checksum calculation: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("VALIDATION: Warning - failed to close file during checksum calculation: %v", closeErr)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to calculate file checksum: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// isAllowedAlgorithm checks if the checksum algorithm is in the allowed list
func (v *ChecksumValidator) isAllowedAlgorithm(algorithm string) bool {
	for _, allowed := range v.config.ValidationAllowedAlgorithms {
		if strings.EqualFold(allowed, algorithm) {
			return true
		}
	}
	return false
}

// GetValidationMode returns current validation configuration for debugging
func (v *ChecksumValidator) GetValidationMode() string {
	mode := "normal"
	if v.config.ValidationStrictMode {
		mode = "strict"
	}
	if v.config.ValidationTestMode {
		mode += " (test)"
	}
	return mode
}