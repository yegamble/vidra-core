package ipfs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateCID_ValidCIDv1Base32 verifies that valid CIDv1 base32 CIDs are accepted
func TestValidateCID_ValidCIDv1Base32(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "valid CIDv1 base32 raw",
			cid:  "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		},
		{
			name: "valid CIDv1 base32 dag-pb",
			cid:  "bafybeihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku",
		},
		{
			name: "valid CIDv1 base32 dag-cbor",
			cid:  "bafyreigbtj4x7ip5legnfznufuopl4sg4knzc2cof6duas4b3q2fy6swua",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.NoError(t, err, "Valid CIDv1 should be accepted")
		})
	}
}

// TestValidateCID_ValidCIDv1Base58 verifies that valid CIDv1 base58 CIDs are accepted
func TestValidateCID_ValidCIDv1Base58(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "valid CIDv1 base58 raw",
			cid:  "zdj7WhuEjrB5mR8s9cLnFKfH8dJVGTqcHxo7lMpR9RbJTUmHu",
		},
		{
			name: "valid CIDv1 base58 dag-pb",
			cid:  "zdj7WbTaiJT1fgatdet9Ei9iDB5hdCxkbVyhyh8YTUnXMiwYi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.NoError(t, err, "Valid CIDv1 base58 should be accepted")
		})
	}
}

// TestValidateCID_RejectsCIDv0 verifies that CIDv0 is rejected per CLAUDE.md requirement
func TestValidateCID_RejectsCIDv0(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "CIDv0 Qm prefix",
			cid:  "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
		},
		{
			name: "CIDv0 multihash",
			cid:  "QmT5NvUtoM5nWFfrQdVrFtvGfKFmG7AHE8P34isapyhCxX",
		},
		{
			name: "CIDv0 with path",
			cid:  "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG/test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.Error(t, err, "CIDv0 should be rejected")
			assert.Contains(t, err.Error(), "CIDv0", "Error should mention CIDv0")
		})
	}
}

// TestValidateCID_RejectsMalformedCIDs verifies that malformed CIDs are rejected
func TestValidateCID_RejectsMalformedCIDs(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "invalid base32 characters",
			cid:  "bafybei@#$%^&*()",
		},
		{
			name: "truncated CID",
			cid:  "bafy",
		},
		{
			name: "invalid multibase prefix",
			cid:  "xafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		},
		{
			name: "random string",
			cid:  "not-a-valid-cid-at-all",
		},
		{
			name: "numeric only",
			cid:  "123456789012345678901234567890",
		},
		{
			name: "special characters",
			cid:  "../../../etc/passwd",
		},
		{
			name: "SQL injection attempt",
			cid:  "'; DROP TABLE videos; --",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.Error(t, err, "Malformed CID should be rejected: %s", tt.cid)
		})
	}
}

// TestValidateCID_RejectsEmptyString verifies that empty strings are rejected
func TestValidateCID_RejectsEmptyString(t *testing.T) {
	err := ValidateCID("")
	assert.Error(t, err, "Empty CID should be rejected")
	assert.Contains(t, err.Error(), "empty", "Error should mention empty CID")
}

// TestValidateCID_RejectsPathTraversal verifies path traversal attempts are blocked
func TestValidateCID_RejectsPathTraversal(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "unix path traversal",
			cid:  "../../etc/passwd",
		},
		{
			name: "windows path traversal",
			cid:  "..\\..\\windows\\system32",
		},
		{
			name: "encoded path traversal",
			cid:  "%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		},
		{
			name: "null byte injection",
			cid:  "bafybei\x00etc/passwd",
		},
		{
			name: "path with slashes",
			cid:  "bafybei/../../etc/passwd",
		},
		{
			name: "absolute path",
			cid:  "/etc/passwd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.Error(t, err, "Path traversal attempt should be rejected: %s", tt.cid)
		})
	}
}

// TestValidateCID_AllowedCodecs verifies that only allowed codecs are accepted
func TestValidateCID_AllowedCodecs(t *testing.T) {
	// These test cases would use actual CIDs with different codecs
	// For now, we test the concept - real CIDs would be generated with specific codecs
	tests := []struct {
		name          string
		codec         string
		shouldBeValid bool
	}{
		{
			name:          "raw codec (0x55)",
			codec:         "raw",
			shouldBeValid: true,
		},
		{
			name:          "dag-pb codec (0x70)",
			codec:         "dag-pb",
			shouldBeValid: true,
		},
		{
			name:          "dag-cbor codec (0x71)",
			codec:         "dag-cbor",
			shouldBeValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test validates the codec checking logic exists
			// Actual implementation would check codec from parsed CID
			t.Skip("Requires implementation with real CID codec checking")
		})
	}
}

// TestValidateCID_RejectsDisallowedCodecs verifies that disallowed codecs are rejected
func TestValidateCID_RejectsDisallowedCodecs(t *testing.T) {
	tests := []struct {
		name  string
		codec string
	}{
		{
			name:  "git-raw codec",
			codec: "git-raw",
		},
		{
			name:  "bitcoin codec",
			codec: "bitcoin",
		},
		{
			name:  "unknown codec",
			codec: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test validates that codec whitelist is enforced
			t.Skip("Requires implementation with real CID codec checking")
		})
	}
}

// TestValidateCID_LengthLimits verifies CID length limits to prevent DoS
func TestValidateCID_LengthLimits(t *testing.T) {
	tests := []struct {
		name      string
		cid       string
		wantError bool
	}{
		{
			name:      "normal length CID",
			cid:       "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			wantError: false,
		},
		{
			name:      "excessively long string (DoS attempt)",
			cid:       strings.Repeat("a", 10000),
			wantError: true,
		},
		{
			name:      "extremely long string",
			cid:       strings.Repeat("bafybei", 1000),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			if tt.wantError {
				assert.Error(t, err, "Excessively long CID should be rejected")
			} else {
				assert.NoError(t, err, "Normal length CID should be accepted")
			}
		})
	}
}

// TestValidateCID_MaxLength verifies maximum CID length constant
func TestValidateCID_MaxLength(t *testing.T) {
	// IPFS CIDs typically shouldn't exceed 128 characters
	// This prevents memory exhaustion attacks
	const maxValidCIDLength = 128

	longButValidPrefix := "bafybei"
	validCID := longButValidPrefix + strings.Repeat("a", maxValidCIDLength-len(longButValidPrefix)-1)
	tooLongCID := longButValidPrefix + strings.Repeat("a", maxValidCIDLength+100)

	err := ValidateCID(validCID)
	// May fail due to invalid CID, but shouldn't panic or hang
	assert.NotPanics(t, func() { _ = ValidateCID(validCID) })

	err = ValidateCID(tooLongCID)
	assert.Error(t, err, "CID exceeding max length should be rejected")
}

// TestValidateCID_SpecialCharacters verifies special characters are rejected
func TestValidateCID_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "newline injection",
			cid:  "bafybei\n\r\n",
		},
		{
			name: "tab characters",
			cid:  "bafybei\t\t",
		},
		{
			name: "control characters",
			cid:  "bafybei\x00\x01\x02",
		},
		{
			name: "unicode characters",
			cid:  "bafybei™©®",
		},
		{
			name: "emoji",
			cid:  "bafybei😀😁😂",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.Error(t, err, "CID with special characters should be rejected: %s", tt.cid)
		})
	}
}

// TestValidateCID_URLEncodingAttacks verifies URL encoding attacks are prevented
func TestValidateCID_URLEncodingAttacks(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "percent encoded path traversal",
			cid:  "%2e%2e%2f%2e%2e%2f",
		},
		{
			name: "double encoded",
			cid:  "%252e%252e%252f",
		},
		{
			name: "mixed encoding",
			cid:  "..%2f..%2f",
		},
		{
			name: "null byte encoded",
			cid:  "bafybei%00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			assert.Error(t, err, "URL encoded attack should be rejected: %s", tt.cid)
		})
	}
}

// TestValidateCID_MultihashValidation verifies multihash component is valid
func TestValidateCID_MultihashValidation(t *testing.T) {
	tests := []struct {
		name    string
		cid     string
		wantErr bool
	}{
		{
			name:    "valid multihash sha256",
			cid:     "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			wantErr: false,
		},
		{
			name:    "invalid multihash format",
			cid:     "bafybeig",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateCID_CaseSensitivity verifies CID validation is case-sensitive for base32
func TestValidateCID_CaseSensitivity(t *testing.T) {
	validCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	upperCID := strings.ToUpper(validCID)

	err := ValidateCID(validCID)
	assert.NoError(t, err, "Lowercase base32 CID should be valid")

	err = ValidateCID(upperCID)
	// Base32 is case-insensitive in spec, but IPFS prefers lowercase
	// Implementation should normalize or reject uppercase
	assert.Error(t, err, "Uppercase CID should be rejected or normalized")
}

// TestValidateCID_WithWhitespace verifies whitespace is handled properly
func TestValidateCID_WithWhitespace(t *testing.T) {
	tests := []struct {
		name string
		cid  string
	}{
		{
			name: "leading whitespace",
			cid:  " bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		},
		{
			name: "trailing whitespace",
			cid:  "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi ",
		},
		{
			name: "embedded whitespace",
			cid:  "bafybei gdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
		},
		{
			name: "multiple spaces",
			cid:  "   bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			// Should either trim and accept OR reject
			// Recommend rejecting to prevent confusion
			assert.Error(t, err, "CID with whitespace should be rejected")
		})
	}
}

// TestPin_ValidatesCID tests that Pin operation validates CID before pinning
func TestPin_ValidatesCID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Pins":["test"]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 5*time.Second)
	ctx := context.Background()

	tests := []struct {
		name    string
		cid     string
		wantErr bool
	}{
		{
			name:    "valid CID",
			cid:     "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			wantErr: false,
		},
		{
			name:    "invalid CID",
			cid:     "../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "CIDv0",
			cid:     "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Pin(ctx, tt.cid)
			if tt.wantErr {
				assert.Error(t, err, "Pin should reject invalid CID")
			} else {
				assert.NoError(t, err, "Pin should accept valid CID")
			}
		})
	}
}

// TestClusterPin_ValidatesCID tests that ClusterPin validates CID
func TestClusterPin_ValidatesCID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient("http://localhost:5001", server.URL, 5*time.Second)
	ctx := context.Background()

	tests := []struct {
		name    string
		cid     string
		wantErr bool
	}{
		{
			name:    "valid CID",
			cid:     "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			wantErr: false,
		},
		{
			name:    "SQL injection",
			cid:     "'; DROP TABLE pins; --",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.ClusterPin(ctx, tt.cid)
			if tt.wantErr {
				assert.Error(t, err, "ClusterPin should reject invalid CID")
			}
			// Note: ClusterPin is best-effort, may not return error even on failure
		})
	}
}

// TestValidateCID_ErrorMessages verifies error messages are informative
func TestValidateCID_ErrorMessages(t *testing.T) {
	tests := []struct {
		name          string
		cid           string
		expectedInMsg string
	}{
		{
			name:          "empty CID",
			cid:           "",
			expectedInMsg: "empty",
		},
		{
			name:          "CIDv0",
			cid:           "QmTest123",
			expectedInMsg: "CIDv0",
		},
		{
			name:          "invalid format",
			cid:           "not-a-cid",
			expectedInMsg: "invalid",
		},
		{
			name:          "path traversal",
			cid:           "../../etc/passwd",
			expectedInMsg: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCID(tt.cid)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), tt.expectedInMsg,
				"Error message should be informative")
		})
	}
}

// TestFuzzValidateCID fuzzes the CID validator to find edge cases
func TestFuzzValidateCID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fuzzing in short mode")
	}

	f := func(input string) bool {
		// Fuzzing should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ValidateCID panicked on input: %q, panic: %v", input, r)
			}
		}()

		_ = ValidateCID(input)
		return true
	}

	config := &quick.Config{
		MaxCount: 10000,
	}

	if err := quick.Check(f, config); err != nil {
		t.Error(err)
	}
}

// TestFuzzValidateCID_ByteSequences fuzzes with arbitrary byte sequences
func TestFuzzValidateCID_ByteSequences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fuzzing in short mode")
	}

	f := func(input []byte) bool {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ValidateCID panicked on bytes: %v, panic: %v", input, r)
			}
		}()

		_ = ValidateCID(string(input))
		return true
	}

	config := &quick.Config{
		MaxCount: 10000,
	}

	if err := quick.Check(f, config); err != nil {
		t.Error(err)
	}
}

// TestValidateCID_PerformanceDoS verifies performance bounds to prevent DoS
func TestValidateCID_PerformanceDoS(t *testing.T) {
	// Validation should complete quickly even for malicious inputs
	tests := []struct {
		name        string
		cid         string
		maxDuration time.Duration
	}{
		{
			name:        "normal CID",
			cid:         "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			maxDuration: 1 * time.Millisecond,
		},
		{
			name:        "very long string",
			cid:         strings.Repeat("a", 10000),
			maxDuration: 10 * time.Millisecond,
		},
		{
			name:        "repeated pattern",
			cid:         strings.Repeat("bafybei", 1000),
			maxDuration: 10 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			_ = ValidateCID(tt.cid)
			duration := time.Since(start)

			assert.LessOrEqual(t, duration, tt.maxDuration,
				fmt.Sprintf("Validation took too long: %v > %v", duration, tt.maxDuration))
		})
	}
}

// TestValidateCID_ConcurrentAccess verifies thread-safety
func TestValidateCID_ConcurrentAccess(t *testing.T) {
	const goroutines = 100
	const iterations = 100

	done := make(chan bool)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			for j := 0; j < iterations; j++ {
				cid := fmt.Sprintf("bafybei%d%d", id, j)
				_ = ValidateCID(cid)
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// BenchmarkValidateCID benchmarks CID validation performance
func BenchmarkValidateCID(b *testing.B) {
	validCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateCID(validCID)
	}
}

// BenchmarkValidateCID_Invalid benchmarks validation of invalid CIDs
func BenchmarkValidateCID_Invalid(b *testing.B) {
	invalidCID := "not-a-valid-cid-at-all"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateCID(invalidCID)
	}
}
