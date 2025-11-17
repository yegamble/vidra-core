package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

// ActivityPubKeyEncryption handles encryption and decryption of ActivityPub private keys
// Uses AES-256-GCM for authenticated encryption at rest
type ActivityPubKeyEncryption struct {
	encryptionKey []byte
}

// NewActivityPubKeyEncryption creates a new encryption handler
// masterKey should be a strong random key (minimum 32 bytes recommended)
func NewActivityPubKeyEncryption(masterKey string) (*ActivityPubKeyEncryption, error) {
	if len(masterKey) < 32 {
		return nil, fmt.Errorf("master key must be at least 32 characters long")
	}

	// Derive a 32-byte encryption key using PBKDF2
	// Using a fixed salt for key derivation (in production, consider using a unique salt per installation)
	salt := []byte("athena-activitypub-key-encryption-v1")
	derivedKey := pbkdf2.Key([]byte(masterKey), salt, 100000, 32, sha256.New)

	return &ActivityPubKeyEncryption{
		encryptionKey: derivedKey,
	}, nil
}

// EncryptPrivateKey encrypts an ActivityPub private key using AES-256-GCM
// Returns base64-encoded encrypted data with nonce prepended
func (e *ActivityPubKeyEncryption) EncryptPrivateKey(privateKeyPEM string) (string, error) {
	if privateKeyPEM == "" {
		return "", fmt.Errorf("private key cannot be empty")
	}

	// Create AES cipher
	block, err := aes.NewCipher(e.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the data
	// GCM provides both encryption and authentication
	ciphertext := gcm.Seal(nonce, nonce, []byte(privateKeyPEM), nil)

	// Encode to base64 for storage
	encrypted := base64.StdEncoding.EncodeToString(ciphertext)

	return encrypted, nil
}

// DecryptPrivateKey decrypts an ActivityPub private key
// Expects base64-encoded data with nonce prepended
func (e *ActivityPubKeyEncryption) DecryptPrivateKey(encryptedData string) (string, error) {
	if encryptedData == "" {
		return "", fmt.Errorf("encrypted data cannot be empty")
	}

	// Decode from base64
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	// Create AES cipher
	block, err := aes.NewCipher(e.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt and verify
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted checks if a key appears to be encrypted (base64-encoded)
// This is a heuristic check - it looks for the format of our encrypted keys
func (e *ActivityPubKeyEncryption) IsEncrypted(data string) bool {
	// Check if it looks like a PEM-encoded key (plaintext)
	if len(data) > 10 && data[:10] == "-----BEGIN" {
		return false
	}

	// Try to decode as base64
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		// Not valid base64, assume plaintext
		return false
	}

	// Check if it has at least the nonce size (12 bytes for GCM)
	// and doesn't look like a PEM key
	return len(decoded) > 12
}

// MigrateToEncrypted encrypts a plaintext private key
// This is used during migration to encrypt existing keys
func (e *ActivityPubKeyEncryption) MigrateToEncrypted(privateKeyPEM string) (string, error) {
	// Check if already encrypted
	if e.IsEncrypted(privateKeyPEM) {
		// Already encrypted, return as-is
		return privateKeyPEM, nil
	}

	// Encrypt the plaintext key
	return e.EncryptPrivateKey(privateKeyPEM)
}
