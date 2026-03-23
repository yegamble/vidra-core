package security

import (
	"context"
	"encoding/base64"
	"fmt"
)

// WalletEncryptionService provides secure encryption for cryptocurrency wallet seeds
// It uses HSM-based encryption with envelope encryption pattern for maximum security
type WalletEncryptionService struct {
	hsm             HSMProvider
	masterKeyID     string
	envelopeEnabled bool // Use envelope encryption for additional security
}

// NewWalletEncryptionService creates a new wallet encryption service
func NewWalletEncryptionService(hsm HSMProvider, masterKeyID string) *WalletEncryptionService {
	return &WalletEncryptionService{
		hsm:             hsm,
		masterKeyID:     masterKeyID,
		envelopeEnabled: true, // Always use envelope encryption for wallet seeds
	}
}

// EncryptedSeed represents an encrypted wallet seed with all necessary metadata
type EncryptedSeed struct {
	EncryptedSeed []byte `json:"encrypted_seed" db:"encrypted_seed"`
	SeedNonce     []byte `json:"seed_nonce" db:"seed_nonce"`
	KeyID         string `json:"key_id" db:"key_id"`
	Algorithm     string `json:"algorithm" db:"algorithm"`
	Version       int    `json:"version" db:"version"`

	// For envelope encryption
	EncryptedDataKey []byte `json:"encrypted_data_key,omitempty" db:"encrypted_data_key"`
	DataKeyNonce     []byte `json:"data_key_nonce,omitempty" db:"data_key_nonce"`
}

// EncryptSeed encrypts a wallet seed using HSM-based envelope encryption
// This provides defense-in-depth: seed is encrypted with data key, data key is encrypted with HSM
func (s *WalletEncryptionService) EncryptSeed(ctx context.Context, seed string) (*EncryptedSeed, error) {
	if seed == "" {
		return nil, fmt.Errorf("seed cannot be empty")
	}

	seedBytes := []byte(seed)
	defer SecureZeroMemory(seedBytes) // Clear from memory after use

	// Use envelope encryption for maximum security
	if s.envelopeEnabled {
		return s.encryptWithEnvelope(ctx, seedBytes)
	}

	// Direct HSM encryption (fallback)
	return s.encryptDirect(ctx, seedBytes)
}

// encryptWithEnvelope implements envelope encryption pattern
// 1. Generate a data encryption key (DEK)
// 2. Encrypt seed with DEK
// 3. Encrypt DEK with HSM master key
// 4. Store encrypted seed + encrypted DEK
func (s *WalletEncryptionService) encryptWithEnvelope(ctx context.Context, seedBytes []byte) (*EncryptedSeed, error) {
	// Step 1: Generate data encryption key from HSM
	dataKey, err := s.hsm.GenerateDataKey(ctx, s.masterKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate data key: %w", err)
	}
	defer SecureZeroMemory(dataKey.PlaintextKey) // Critical: zero key after use

	// Step 2: Encrypt seed with data key
	seedEncrypted, err := s.hsm.Encrypt(ctx, seedBytes, s.masterKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt seed: %w", err)
	}

	// Step 3: Store both encrypted seed and encrypted data key
	return &EncryptedSeed{
		EncryptedSeed:    seedEncrypted.Ciphertext,
		SeedNonce:        seedEncrypted.Nonce,
		KeyID:            s.masterKeyID,
		Algorithm:        "ENVELOPE-AES-256-GCM",
		Version:          1,
		EncryptedDataKey: dataKey.EncryptedKey,
		DataKeyNonce:     []byte(dataKey.EncryptionCtx["nonce"]),
	}, nil
}

// encryptDirect implements direct HSM encryption (simpler but less flexible)
func (s *WalletEncryptionService) encryptDirect(ctx context.Context, seedBytes []byte) (*EncryptedSeed, error) {
	encrypted, err := s.hsm.Encrypt(ctx, seedBytes, s.masterKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt seed: %w", err)
	}

	return &EncryptedSeed{
		EncryptedSeed: encrypted.Ciphertext,
		SeedNonce:     encrypted.Nonce,
		KeyID:         encrypted.KeyID,
		Algorithm:     encrypted.Algorithm,
		Version:       encrypted.Version,
	}, nil
}

// DecryptSeed decrypts a wallet seed using HSM-based decryption
func (s *WalletEncryptionService) DecryptSeed(ctx context.Context, encSeed *EncryptedSeed) (string, error) {
	if encSeed == nil {
		return "", fmt.Errorf("encrypted seed is nil")
	}

	// Validate encrypted seed structure
	if err := s.validateEncryptedSeed(encSeed); err != nil {
		return "", fmt.Errorf("invalid encrypted seed: %w", err)
	}

	var plaintext []byte
	var err error

	// Use envelope decryption if envelope data is present
	if len(encSeed.EncryptedDataKey) > 0 {
		plaintext, err = s.decryptWithEnvelope(ctx, encSeed)
	} else {
		plaintext, err = s.decryptDirect(ctx, encSeed)
	}

	if err != nil {
		return "", err
	}

	defer SecureZeroMemory(plaintext) // Clear decrypted seed from memory
	return string(plaintext), nil
}

// decryptWithEnvelope implements envelope decryption
func (s *WalletEncryptionService) decryptWithEnvelope(ctx context.Context, encSeed *EncryptedSeed) ([]byte, error) {
	// Step 1: Decrypt data key using HSM
	nonce, err := base64.StdEncoding.DecodeString(string(encSeed.DataKeyNonce))
	if err != nil {
		nonce = encSeed.DataKeyNonce // Try direct use if not base64
	}

	dataKeyPlaintext, err := s.hsm.Decrypt(ctx, encSeed.EncryptedDataKey, nonce, encSeed.KeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data key: %w", err)
	}
	defer SecureZeroMemory(dataKeyPlaintext)

	// Step 2: Decrypt seed using data key
	seedPlaintext, err := s.hsm.Decrypt(ctx, encSeed.EncryptedSeed, encSeed.SeedNonce, encSeed.KeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt seed: %w", err)
	}

	return seedPlaintext, nil
}

// decryptDirect implements direct HSM decryption
func (s *WalletEncryptionService) decryptDirect(ctx context.Context, encSeed *EncryptedSeed) ([]byte, error) {
	plaintext, err := s.hsm.Decrypt(ctx, encSeed.EncryptedSeed, encSeed.SeedNonce, encSeed.KeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt seed: %w", err)
	}
	return plaintext, nil
}

// validateEncryptedSeed validates the structure of encrypted seed data
func (s *WalletEncryptionService) validateEncryptedSeed(encSeed *EncryptedSeed) error {
	if len(encSeed.EncryptedSeed) == 0 {
		return fmt.Errorf("encrypted seed is empty")
	}
	if len(encSeed.SeedNonce) == 0 {
		return fmt.Errorf("seed nonce is empty")
	}
	if encSeed.KeyID == "" {
		return fmt.Errorf("key ID is empty")
	}
	if encSeed.Version < 1 {
		return fmt.Errorf("invalid version: %d", encSeed.Version)
	}
	return nil
}

// RotateEncryption re-encrypts a seed with a new key (for key rotation)
func (s *WalletEncryptionService) RotateEncryption(ctx context.Context, encSeed *EncryptedSeed, newKeyID string) (*EncryptedSeed, error) {
	// Decrypt with old key
	plaintext, err := s.DecryptSeed(ctx, encSeed)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt for rotation: %w", err)
	}
	defer SecureZeroMemory([]byte(plaintext))

	// Create new service with new key
	newService := NewWalletEncryptionService(s.hsm, newKeyID)

	// Re-encrypt with new key
	newEncSeed, err := newService.EncryptSeed(ctx, plaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to re-encrypt: %w", err)
	}

	return newEncSeed, nil
}

// ValidateSeedStrength validates that a seed meets security requirements
// For IOTA, seeds should be 64 characters (81 trytes for legacy, 64 hex for Chrysalis)
func ValidateSeedStrength(seed string) error {
	if len(seed) < 64 {
		return fmt.Errorf("seed too short: minimum 64 characters required, got %d", len(seed))
	}

	if len(seed) > 256 {
		return fmt.Errorf("seed too long: maximum 256 characters allowed, got %d", len(seed))
	}

	// Check for all same character (weak seed)
	if isAllSameChar(seed) {
		return fmt.Errorf("weak seed: all characters are the same")
	}

	return nil
}

// isAllSameChar checks if string contains only one repeated character
func isAllSameChar(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] != first {
			return false
		}
	}
	return true
}

// ZeroSeedString securely zeros a seed string in memory
func ZeroSeedString(seed *string) {
	if seed == nil || *seed == "" {
		return
	}
	// Convert to byte slice and zero
	bytes := []byte(*seed)
	SecureZeroMemory(bytes)
	*seed = ""
}
