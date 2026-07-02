// Package secretbox provides authenticated envelope encryption for secrets held
// at rest — currently federation actor private keys (see .ralph/specs/federation.md
// §3). It uses AES-256-GCM under a 32-byte key-encryption key (KEK) supplied via
// config (FEDERATION_KEY_KEK). Sealed values are serialized as
// "enc:" + base64(nonce || ciphertext) so a caller can cheaply tell an encrypted
// value from a raw (dev-only, no-KEK) one. A real KMS/HSM can later replace the
// local KEK behind this same Seal/Open seam.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// EncPrefix marks a value produced by Seal.
const EncPrefix = "enc:"

// Cipher seals and opens secrets under a fixed KEK. Safe for concurrent use.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte KEK (AES-256).
func NewCipher(kek []byte) (*Cipher, error) {
	if len(kek) != 32 {
		return nil, fmt.Errorf("secretbox: KEK must be 32 bytes, got %d", len(kek))
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// NewCipherFromBase64 decodes a standard-base64 32-byte KEK and builds a Cipher.
func NewCipherFromBase64(b64 string) (*Cipher, error) {
	kek, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("secretbox: KEK must be valid base64: %w", err)
	}
	return NewCipher(kek)
}

// Seal encrypts plaintext and returns "enc:" + base64(nonce || ciphertext). Each
// call uses a fresh random nonce, so sealing the same plaintext twice differs.
func (c *Cipher) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return EncPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal. It errors on a missing prefix, invalid base64, a too-short
// blob, or authentication failure (wrong KEK or tampering).
func (c *Cipher) Open(stored string) ([]byte, error) {
	if !strings.HasPrefix(stored, EncPrefix) {
		return nil, errors.New("secretbox: value is not enc-prefixed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, EncPrefix))
	if err != nil {
		return nil, fmt.Errorf("secretbox: base64: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	return c.aead.Open(nil, nonce, ct, nil)
}

// IsSealed reports whether a stored value was produced by Seal (vs a raw dev value).
func IsSealed(stored string) bool { return strings.HasPrefix(stored, EncPrefix) }
