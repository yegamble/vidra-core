package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SignatureVerifier handles plugin signature verification
type SignatureVerifier struct {
	trustedKeys map[string]ed25519.PublicKey // map of author -> public key
	keyFile     string                       // path to trusted keys file
}

// NewSignatureVerifier creates a new signature verifier
func NewSignatureVerifier(keyFile string) (*SignatureVerifier, error) {
	verifier := &SignatureVerifier{
		trustedKeys: make(map[string]ed25519.PublicKey),
		keyFile:     keyFile,
	}

	// Load trusted keys if file exists
	if err := verifier.LoadTrustedKeys(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load trusted keys: %w", err)
	}

	return verifier, nil
}

// TrustedKey represents a trusted public key
type TrustedKey struct {
	Author    string `json:"author"`
	PublicKey string `json:"public_key"` // base64 encoded
	AddedAt   string `json:"added_at"`
	Comment   string `json:"comment,omitempty"`
}

// LoadTrustedKeys loads trusted public keys from file
func (v *SignatureVerifier) LoadTrustedKeys() error {
	data, err := os.ReadFile(v.keyFile)
	if err != nil {
		return err
	}

	var keys []TrustedKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("failed to parse trusted keys: %w", err)
	}

	for _, key := range keys {
		pubKeyBytes, err := base64.StdEncoding.DecodeString(key.PublicKey)
		if err != nil {
			return fmt.Errorf("invalid public key for %s: %w", key.Author, err)
		}

		if len(pubKeyBytes) != ed25519.PublicKeySize {
			return fmt.Errorf("invalid public key size for %s: got %d, want %d", key.Author, len(pubKeyBytes), ed25519.PublicKeySize)
		}

		v.trustedKeys[key.Author] = ed25519.PublicKey(pubKeyBytes)
	}

	return nil
}

// AddTrustedKey adds a trusted public key
func (v *SignatureVerifier) AddTrustedKey(author string, publicKey ed25519.PublicKey, comment string) error {
	v.trustedKeys[author] = publicKey

	// Save to file
	return v.saveTrustedKeys(comment)
}

// RemoveTrustedKey removes a trusted public key
func (v *SignatureVerifier) RemoveTrustedKey(author string) error {
	delete(v.trustedKeys, author)
	return v.saveTrustedKeys("")
}

// saveTrustedKeys saves trusted keys to file
func (v *SignatureVerifier) saveTrustedKeys(comment string) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(v.keyFile), 0750); err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}

	var keys []TrustedKey
	for author, pubKey := range v.trustedKeys {
		keys = append(keys, TrustedKey{
			Author:    author,
			PublicKey: base64.StdEncoding.EncodeToString(pubKey),
			Comment:   comment,
		})
	}

	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal trusted keys: %w", err)
	}

	if err := os.WriteFile(v.keyFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write trusted keys: %w", err)
	}

	return nil
}

// VerifySignature verifies a plugin signature
func (v *SignatureVerifier) VerifySignature(pluginData []byte, signature []byte, author string) error {
	// Get public key for author
	publicKey, exists := v.trustedKeys[author]
	if !exists {
		return fmt.Errorf("no trusted key for author: %s", author)
	}

	// Verify signature
	if !ed25519.Verify(publicKey, pluginData, signature) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// IsAuthorTrusted checks if an author is trusted
func (v *SignatureVerifier) IsAuthorTrusted(author string) bool {
	_, exists := v.trustedKeys[author]
	return exists
}

// GetTrustedAuthors returns list of trusted authors
func (v *SignatureVerifier) GetTrustedAuthors() []string {
	authors := make([]string, 0, len(v.trustedKeys))
	for author := range v.trustedKeys {
		authors = append(authors, author)
	}
	return authors
}

// SignPlugin signs a plugin package (utility for plugin developers)
func SignPlugin(pluginData []byte, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: got %d, want %d", len(privateKey), ed25519.PrivateKeySize)
	}

	signature := ed25519.Sign(privateKey, pluginData)
	return signature, nil
}

// GenerateKeyPair generates a new Ed25519 key pair (utility for plugin developers)
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return pubKey, privKey, nil
}
