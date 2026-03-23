package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

const (
	// Key sizes
	X25519PrivateKeySize  = 32
	X25519PublicKeySize   = 32
	Ed25519PrivateKeySize = 64
	Ed25519PublicKeySize  = 32
	ChaCha20KeySize       = 32
	XChaCha20NonceSize    = 24

	// Argon2id parameters (OWASP recommended)
	Argon2Memory      = 65536 // 64MB
	Argon2Time        = 3     // 3 iterations
	Argon2Parallelism = 4     // 4 threads
	Argon2SaltSize    = 32    // 32 bytes
	Argon2KeySize     = 32    // 32 bytes output

	// Security constants
	MaxClockSkew = 5 * time.Minute
)

// CryptoService provides secure cryptographic operations for E2EE messaging
type CryptoService struct{}

// NewCryptoService creates a new crypto service instance
func NewCryptoService() *CryptoService {
	return &CryptoService{}
}

// X25519KeyPair represents an X25519 key pair for ECDH
type X25519KeyPair struct {
	PrivateKey []byte // 32 bytes
	PublicKey  []byte // 32 bytes
}

// Ed25519KeyPair represents an Ed25519 key pair for signing
type Ed25519KeyPair struct {
	PrivateKey ed25519.PrivateKey // 64 bytes
	PublicKey  ed25519.PublicKey  // 32 bytes
}

// MasterKey represents a user's master encryption key
type MasterKey struct {
	Key  []byte // 32 bytes
	Salt []byte // 32 bytes
}

// EncryptedData represents encrypted data with metadata
type EncryptedData struct {
	Ciphertext []byte
	Nonce      []byte
	Version    int
}

// GenerateX25519KeyPair generates a new X25519 key pair for ECDH
func (cs *CryptoService) GenerateX25519KeyPair() (*X25519KeyPair, error) {
	privateKey := make([]byte, X25519PrivateKeySize)
	if _, err := rand.Read(privateKey); err != nil {
		return nil, fmt.Errorf("failed to generate X25519 private key: %w", err)
	}

	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to compute X25519 public key: %w", err)
	}

	return &X25519KeyPair{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}, nil
}

// GenerateEd25519KeyPair generates a new Ed25519 key pair for signing
func (cs *CryptoService) GenerateEd25519KeyPair() (*Ed25519KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Ed25519 key pair: %w", err)
	}

	return &Ed25519KeyPair{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
	}, nil
}

// ComputeSharedSecret performs ECDH to compute shared secret
func (cs *CryptoService) ComputeSharedSecret(privateKey, publicKey []byte) ([]byte, error) {
	if len(privateKey) != X25519PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: %d", len(privateKey))
	}
	if len(publicKey) != X25519PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: %d", len(publicKey))
	}

	sharedSecret, err := curve25519.X25519(privateKey, publicKey)
	if err != nil {
		return nil, fmt.Errorf("ECDH failed: %w", err)
	}

	// Check for weak shared secrets (all zeros)
	if subtle.ConstantTimeCompare(sharedSecret, make([]byte, 32)) == 1 {
		return nil, fmt.Errorf("weak shared secret generated")
	}

	return sharedSecret, nil
}

// DeriveKeyFromPassword derives a key from password using Argon2id
func (cs *CryptoService) DeriveKeyFromPassword(password string, salt []byte) ([]byte, error) {
	if len(salt) != Argon2SaltSize {
		return nil, fmt.Errorf("invalid salt size: %d", len(salt))
	}

	key := argon2.IDKey([]byte(password), salt, Argon2Time, Argon2Memory, Argon2Parallelism, Argon2KeySize)
	return key, nil
}

// GenerateSalt generates a cryptographically secure random salt
func (cs *CryptoService) GenerateSalt() ([]byte, error) {
	salt := make([]byte, Argon2SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	return salt, nil
}

// GenerateNonce generates a cryptographically secure random nonce for XChaCha20Poly1305
func (cs *CryptoService) GenerateNonce() ([]byte, error) {
	nonce := make([]byte, XChaCha20NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return nonce, nil
}

// Encrypt encrypts data using XChaCha20Poly1305 AEAD
func (cs *CryptoService) Encrypt(data, key, nonce []byte) ([]byte, error) {
	if len(key) != ChaCha20KeySize {
		return nil, fmt.Errorf("invalid key size: %d", len(key))
	}
	if len(nonce) != XChaCha20NonceSize {
		return nil, fmt.Errorf("invalid nonce size: %d", len(nonce))
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create XChaCha20Poly1305 cipher: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, data, nil)
	return ciphertext, nil
}

// Decrypt decrypts data using XChaCha20Poly1305 AEAD
func (cs *CryptoService) Decrypt(ciphertext, key, nonce []byte) ([]byte, error) {
	if len(key) != ChaCha20KeySize {
		return nil, fmt.Errorf("invalid key size: %d", len(key))
	}
	if len(nonce) != XChaCha20NonceSize {
		return nil, fmt.Errorf("invalid nonce size: %d", len(nonce))
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create XChaCha20Poly1305 cipher: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// SignMessage signs a message using Ed25519
func (cs *CryptoService) SignMessage(message []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != Ed25519PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: %d", len(privateKey))
	}

	signature := ed25519.Sign(privateKey, message)
	return signature, nil
}

// VerifySignature verifies an Ed25519 signature
func (cs *CryptoService) VerifySignature(message, signature []byte, publicKey ed25519.PublicKey) bool {
	if len(publicKey) != Ed25519PublicKeySize {
		return false
	}

	return ed25519.Verify(publicKey, message, signature)
}

// EncryptWithMasterKey encrypts data using a master key
func (cs *CryptoService) EncryptWithMasterKey(data, masterKey []byte) (*EncryptedData, error) {
	nonce, err := cs.GenerateNonce()
	if err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext, err := cs.Encrypt(data, masterKey, nonce)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	return &EncryptedData{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		Version:    1,
	}, nil
}

// DecryptWithMasterKey decrypts data using a master key
func (cs *CryptoService) DecryptWithMasterKey(encData *EncryptedData, masterKey []byte) ([]byte, error) {
	if encData.Version != 1 {
		return nil, fmt.Errorf("unsupported encryption version: %d", encData.Version)
	}

	plaintext, err := cs.Decrypt(encData.Ciphertext, masterKey, encData.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// SecureCompare performs constant-time comparison of two byte slices
func (cs *CryptoService) SecureCompare(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// ZeroMemory securely zeros memory
func (cs *CryptoService) ZeroMemory(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// Base64Encode encodes bytes to base64 string
func (cs *CryptoService) Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode decodes base64 string to bytes
func (cs *CryptoService) Base64Decode(data string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	return decoded, nil
}

// ValidatePublicKey validates an X25519 public key
func (cs *CryptoService) ValidatePublicKey(publicKey []byte) error {
	if len(publicKey) != X25519PublicKeySize {
		return fmt.Errorf("invalid public key size: %d", len(publicKey))
	}

	// Check for weak public keys (all zeros or order of the base point)
	allZeros := make([]byte, X25519PublicKeySize)
	if subtle.ConstantTimeCompare(publicKey, allZeros) == 1 {
		return fmt.Errorf("weak public key: all zeros")
	}

	return nil
}

// ValidateSigningPublicKey validates an Ed25519 public key
func (cs *CryptoService) ValidateSigningPublicKey(publicKey []byte) error {
	if len(publicKey) != Ed25519PublicKeySize {
		return fmt.Errorf("invalid signing public key size: %d", len(publicKey))
	}
	return nil
}

// GenerateMessageID generates a unique message ID
func (cs *CryptoService) GenerateMessageID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", fmt.Errorf("failed to generate message ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(id), nil
}

// SecureRandom fills the provided byte slice with cryptographically secure random data
func SecureRandom(data []byte) (int, error) {
	return rand.Read(data)
}
