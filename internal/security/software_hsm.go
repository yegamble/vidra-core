package security

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// SoftwareHSM implements HSMProvider using software-based encryption
// This is a fallback when hardware HSM is not available
// SECURITY WARNING: This stores keys in memory and should only be used with:
// 1. Environment variables containing base64-encoded keys
// 2. Kubernetes secrets mounted as volumes
// 3. Development/testing environments
// For production, use: PKCS11HSM, CloudHSM, or VaultHSM
type SoftwareHSM struct {
	masterKeys    map[string][]byte
	mu            sync.RWMutex
	keyDerivation KeyDerivationConfig
}

// KeyDerivationConfig defines parameters for key derivation
type KeyDerivationConfig struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltSize    uint32
	KeySize     uint32
}

// DefaultKeyDerivationConfig returns secure default parameters
func DefaultKeyDerivationConfig() KeyDerivationConfig {
	return KeyDerivationConfig{
		Memory:      64 * 1024, // 64 MB
		Iterations:  4,         // 4 iterations
		Parallelism: 4,         // 4 parallel threads
		SaltSize:    32,        // 32 bytes salt
		KeySize:     32,        // 32 bytes key
	}
}

// NewSoftwareHSM creates a new software-based HSM implementation
func NewSoftwareHSM() *SoftwareHSM {
	return &SoftwareHSM{
		masterKeys:    make(map[string][]byte),
		keyDerivation: DefaultKeyDerivationConfig(),
	}
}

// AddMasterKey adds a master encryption key to the HSM
// In production, this would be loaded from secure key storage
func (h *SoftwareHSM) AddMasterKey(keyID string, key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Make a copy to avoid external modification
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	h.masterKeys[keyID] = keyCopy

	return nil
}

// AddMasterKeyFromBase64 adds a master key from base64-encoded string
func (h *SoftwareHSM) AddMasterKeyFromBase64(keyID string, base64Key string) error {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return fmt.Errorf("failed to decode base64 key: %w", err)
	}
	return h.AddMasterKey(keyID, key)
}

// Encrypt implements HSMProvider.Encrypt
func (h *SoftwareHSM) Encrypt(ctx context.Context, plaintext []byte, keyID string) (*EncryptedData, error) {
	h.mu.RLock()
	masterKey, exists := h.masterKeys[keyID]
	h.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("master key %s not found", keyID)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return &EncryptedData{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyID:      keyID,
		Algorithm:  "AES-256-GCM",
		Version:    1,
	}, nil
}

// Decrypt implements HSMProvider.Decrypt
func (h *SoftwareHSM) Decrypt(ctx context.Context, ciphertext []byte, nonce []byte, keyID string) ([]byte, error) {
	h.mu.RLock()
	masterKey, exists := h.masterKeys[keyID]
	h.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("master key %s not found", keyID)
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// GenerateDataKey implements HSMProvider.GenerateDataKey
func (h *SoftwareHSM) GenerateDataKey(ctx context.Context, masterKeyID string) (*DataKey, error) {
	// Generate random data encryption key
	plaintextKey := make([]byte, 32)
	if _, err := rand.Read(plaintextKey); err != nil {
		return nil, fmt.Errorf("failed to generate data key: %w", err)
	}

	// Encrypt the data key using master key
	encrypted, err := h.Encrypt(ctx, plaintextKey, masterKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data key: %w", err)
	}

	return &DataKey{
		PlaintextKey:  plaintextKey,
		EncryptedKey:  encrypted.Ciphertext,
		KeyID:         masterKeyID,
		EncryptionCtx: map[string]string{"nonce": base64.StdEncoding.EncodeToString(encrypted.Nonce)},
	}, nil
}

// RotateKey implements HSMProvider.RotateKey
func (h *SoftwareHSM) RotateKey(ctx context.Context, oldKeyID string) (string, error) {
	// Generate new key ID
	newKeyID := fmt.Sprintf("%s-rotated-%d", oldKeyID, time.Now().Unix())

	// Generate new master key
	newMasterKey := make([]byte, 32)
	if _, err := rand.Read(newMasterKey); err != nil {
		return "", fmt.Errorf("failed to generate new master key: %w", err)
	}

	// Add new master key
	if err := h.AddMasterKey(newKeyID, newMasterKey); err != nil {
		return "", fmt.Errorf("failed to add new master key: %w", err)
	}

	// In production, you would re-encrypt all data with the new key
	// and securely delete the old key

	return newKeyID, nil
}

// GetKeyMetadata implements HSMProvider.GetKeyMetadata
func (h *SoftwareHSM) GetKeyMetadata(ctx context.Context, keyID string) (*KeyMetadata, error) {
	h.mu.RLock()
	_, exists := h.masterKeys[keyID]
	h.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("key %s not found", keyID)
	}

	return &KeyMetadata{
		KeyID:       keyID,
		Algorithm:   "AES-256-GCM",
		KeyType:     "software",
		CreatedAt:   "unknown", // Would be tracked in production
		RotatedAt:   "never",
		IsEnabled:   true,
		Description: "Software-based master encryption key",
	}, nil
}

// IsAvailable implements HSMProvider.IsAvailable
func (h *SoftwareHSM) IsAvailable(ctx context.Context) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.masterKeys) > 0
}

// RemoveKey securely removes a key from memory
// This should only be called during key rotation after all data is re-encrypted
func (h *SoftwareHSM) RemoveKey(keyID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	key, exists := h.masterKeys[keyID]
	if !exists {
		return fmt.Errorf("key %s not found", keyID)
	}

	// Securely zero the key material before removing
	SecureZeroMemory(key)
	delete(h.masterKeys, keyID)

	return nil
}

// DeriveKeyFromPassword derives an encryption key from a password
// This is useful for user-specific encryption where the password is the key source
func (h *SoftwareHSM) DeriveKeyFromPassword(password string, salt []byte) ([]byte, error) {
	if len(salt) != int(h.keyDerivation.SaltSize) {
		return nil, fmt.Errorf("invalid salt size: expected %d, got %d",
			h.keyDerivation.SaltSize, len(salt))
	}

	key := argon2.IDKey(
		[]byte(password),
		salt,
		h.keyDerivation.Iterations,
		h.keyDerivation.Memory,
		h.keyDerivation.Parallelism,
		h.keyDerivation.KeySize,
	)

	return key, nil
}

// GenerateSalt generates a cryptographically secure salt for key derivation
func (h *SoftwareHSM) GenerateSalt() ([]byte, error) {
	salt := make([]byte, h.keyDerivation.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	return salt, nil
}
