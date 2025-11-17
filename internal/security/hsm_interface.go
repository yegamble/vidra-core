package security

import (
	"context"
	"crypto/rand"
	"fmt"
)

// HSMProvider defines the interface for Hardware Security Module operations
// This abstraction allows for different HSM implementations (PKCS#11, AWS CloudHSM, etc.)
type HSMProvider interface {
	// Encrypt encrypts data using the HSM
	// Returns ciphertext and metadata needed for decryption
	Encrypt(ctx context.Context, plaintext []byte, keyID string) (*EncryptedData, error)

	// Decrypt decrypts data using the HSM
	Decrypt(ctx context.Context, ciphertext []byte, nonce []byte, keyID string) ([]byte, error)

	// GenerateDataKey generates a data encryption key wrapped by the HSM master key
	// Returns both plaintext and encrypted versions of the key
	GenerateDataKey(ctx context.Context, masterKeyID string) (*DataKey, error)

	// RotateKey rotates an encryption key (for key rotation policies)
	RotateKey(ctx context.Context, oldKeyID string) (newKeyID string, err error)

	// GetKeyMetadata retrieves metadata about a key without exposing the key material
	GetKeyMetadata(ctx context.Context, keyID string) (*KeyMetadata, error)

	// IsAvailable checks if the HSM is available and operational
	IsAvailable(ctx context.Context) bool
}

// EncryptedData represents data encrypted by the HSM
type EncryptedData struct {
	Ciphertext []byte `json:"ciphertext"`
	Nonce      []byte `json:"nonce"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Version    int    `json:"version"`
}

// DataKey represents a data encryption key
type DataKey struct {
	PlaintextKey  []byte // Used for encryption, must be zeroed after use
	EncryptedKey  []byte // Stored alongside encrypted data
	KeyID         string // Master key ID used to wrap this key
	EncryptionCtx map[string]string
}

// KeyMetadata contains information about an encryption key
type KeyMetadata struct {
	KeyID       string
	Algorithm   string
	KeyType     string
	CreatedAt   string
	RotatedAt   string
	IsEnabled   bool
	Description string
}

// SecureZeroMemory securely zeros sensitive memory
func SecureZeroMemory(data []byte) {
	if data == nil {
		return
	}
	for i := range data {
		data[i] = 0
	}
}

// GenerateNonce generates a cryptographically secure random nonce
func GenerateNonce(size int) ([]byte, error) {
	nonce := make([]byte, size)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return nonce, nil
}

// ValidateEncryptedData validates the structure of encrypted data
func ValidateEncryptedData(data *EncryptedData) error {
	if data == nil {
		return fmt.Errorf("encrypted data is nil")
	}
	if len(data.Ciphertext) == 0 {
		return fmt.Errorf("ciphertext is empty")
	}
	if len(data.Nonce) == 0 {
		return fmt.Errorf("nonce is empty")
	}
	if data.KeyID == "" {
		return fmt.Errorf("key ID is empty")
	}
	if data.Algorithm == "" {
		return fmt.Errorf("algorithm is empty")
	}
	if data.Version < 1 {
		return fmt.Errorf("invalid version: %d", data.Version)
	}
	return nil
}
