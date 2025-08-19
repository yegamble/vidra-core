package validation

import (
	"testing"

	"athena/internal/config"
	"athena/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestChecksumValidator_ValidateChunkChecksum(t *testing.T) {
	tests := []struct {
		name             string
		config           *config.Config
		data             []byte
		expectedChecksum string
		expectError      bool
		errorCode        string
	}{
		{
			name: "valid checksum passes",
			config: &config.Config{
				ValidationStrictMode:        false,
				ValidationAllowedAlgorithms: []string{"sha256"},
				ValidationTestMode:          false,
				ValidationLogEvents:         false,
			},
			data:             []byte("test data"),
			expectedChecksum: "916f0027a575074ce72a331777c3478d6513f786a591bd892da1a577bf2335f9",
			expectError:      false,
		},
		{
			name: "invalid checksum fails",
			config: &config.Config{
				ValidationStrictMode:        false,
				ValidationAllowedAlgorithms: []string{"sha256"},
				ValidationTestMode:          false,
				ValidationLogEvents:         false,
			},
			data:             []byte("test data"),
			expectedChecksum: "invalid_checksum",
			expectError:      true,
			errorCode:        "CHECKSUM_MISMATCH",
		},
		{
			name: "strict mode requires checksum",
			config: &config.Config{
				ValidationStrictMode:        true,
				ValidationAllowedAlgorithms: []string{"sha256"},
				ValidationTestMode:          false,
				ValidationLogEvents:         false,
			},
			data:             []byte("test data"),
			expectedChecksum: "",
			expectError:      true,
			errorCode:        "CHECKSUM_REQUIRED",
		},
		{
			name: "test mode allows bypass",
			config: &config.Config{
				ValidationStrictMode:        true,
				ValidationAllowedAlgorithms: []string{"sha256"},
				ValidationTestMode:          true,
				ValidationLogEvents:         false,
			},
			data:             []byte("test data"),
			expectedChecksum: "abc123",
			expectError:      false,
		},
		{
			name: "non-strict mode allows empty checksum",
			config: &config.Config{
				ValidationStrictMode:        false,
				ValidationAllowedAlgorithms: []string{"sha256"},
				ValidationTestMode:          false,
				ValidationLogEvents:         false,
			},
			data:             []byte("test data"),
			expectedChecksum: "",
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewChecksumValidator(tt.config)
			err := validator.ValidateChunkChecksum(tt.data, tt.expectedChecksum)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorCode != "" {
					domainErr, ok := err.(domain.DomainError)
					assert.True(t, ok, "Expected domain error")
					assert.Equal(t, tt.errorCode, domainErr.Code)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestChecksumValidator_CalculateChecksum(t *testing.T) {
	validator := NewChecksumValidator(&config.Config{})

	data := []byte("test data")
	expected := "916f0027a575074ce72a331777c3478d6513f786a591bd892da1a577bf2335f9"

	actual := validator.CalculateChecksum(data)
	assert.Equal(t, expected, actual)
}

func TestChecksumValidator_GetValidationMode(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected string
	}{
		{
			name: "normal mode",
			config: &config.Config{
				ValidationStrictMode: false,
				ValidationTestMode:   false,
			},
			expected: "normal",
		},
		{
			name: "strict mode",
			config: &config.Config{
				ValidationStrictMode: true,
				ValidationTestMode:   false,
			},
			expected: "strict",
		},
		{
			name: "test mode",
			config: &config.Config{
				ValidationStrictMode: false,
				ValidationTestMode:   true,
			},
			expected: "normal (test)",
		},
		{
			name: "strict test mode",
			config: &config.Config{
				ValidationStrictMode: true,
				ValidationTestMode:   true,
			},
			expected: "strict (test)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewChecksumValidator(tt.config)
			actual := validator.GetValidationMode()
			assert.Equal(t, tt.expected, actual)
		})
	}
}
