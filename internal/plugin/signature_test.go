package plugin

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	if len(pubKey) != ed25519.PublicKeySize {
		t.Errorf("Invalid public key size: got %d, want %d", len(pubKey), ed25519.PublicKeySize)
	}

	if len(privKey) != ed25519.PrivateKeySize {
		t.Errorf("Invalid private key size: got %d, want %d", len(privKey), ed25519.PrivateKeySize)
	}
}

func TestSignAndVerify(t *testing.T) {
	// Generate key pair
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Sign data
	data := []byte("test plugin data")
	signature, err := SignPlugin(data, privKey)
	if err != nil {
		t.Fatalf("Failed to sign plugin: %v", err)
	}

	// Verify signature
	if !ed25519.Verify(pubKey, data, signature) {
		t.Error("Signature verification failed")
	}

	// Verify with wrong data
	wrongData := []byte("wrong data")
	if ed25519.Verify(pubKey, wrongData, signature) {
		t.Error("Signature verification should have failed with wrong data")
	}

	// Verify with wrong signature
	wrongSignature := make([]byte, len(signature))
	if ed25519.Verify(pubKey, data, wrongSignature) {
		t.Error("Signature verification should have failed with wrong signature")
	}
}

func TestSignatureVerifier(t *testing.T) {
	// Create temp key file
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "trusted_keys.json")

	// Create verifier
	verifier, err := NewSignatureVerifier(keyFile)
	if err != nil {
		t.Fatalf("Failed to create verifier: %v", err)
	}

	// Generate test key pair
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Add trusted key
	author := "test-author"
	if err := verifier.AddTrustedKey(author, pubKey, "Test key"); err != nil {
		t.Fatalf("Failed to add trusted key: %v", err)
	}

	// Check if author is trusted
	if !verifier.IsAuthorTrusted(author) {
		t.Error("Author should be trusted")
	}

	// Sign plugin
	pluginData := []byte("test plugin package")
	signature, err := SignPlugin(pluginData, privKey)
	if err != nil {
		t.Fatalf("Failed to sign plugin: %v", err)
	}

	// Verify signature
	if err := verifier.VerifySignature(pluginData, signature, author); err != nil {
		t.Errorf("Signature verification failed: %v", err)
	}

	// Test with untrusted author
	untrustedAuthor := "untrusted"
	if err := verifier.VerifySignature(pluginData, signature, untrustedAuthor); err == nil {
		t.Error("Verification should fail for untrusted author")
	}

	// Test with invalid signature
	invalidSig := make([]byte, len(signature))
	if err := verifier.VerifySignature(pluginData, invalidSig, author); err == nil {
		t.Error("Verification should fail with invalid signature")
	}

	// Remove trusted key
	if err := verifier.RemoveTrustedKey(author); err != nil {
		t.Fatalf("Failed to remove trusted key: %v", err)
	}

	// Check if author is no longer trusted
	if verifier.IsAuthorTrusted(author) {
		t.Error("Author should not be trusted after removal")
	}
}

func TestLoadTrustedKeys(t *testing.T) {
	// Create temp key file
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "trusted_keys.json")

	// Create verifier and add keys
	verifier1, err := NewSignatureVerifier(keyFile)
	if err != nil {
		t.Fatalf("Failed to create verifier: %v", err)
	}

	pubKey1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	pubKey2, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	if err := verifier1.AddTrustedKey("author1", pubKey1, "Test key 1"); err != nil {
		t.Fatalf("Failed to add key 1: %v", err)
	}

	if err := verifier1.AddTrustedKey("author2", pubKey2, "Test key 2"); err != nil {
		t.Fatalf("Failed to add key 2: %v", err)
	}

	// Create new verifier and load keys from file
	verifier2, err := NewSignatureVerifier(keyFile)
	if err != nil {
		t.Fatalf("Failed to create verifier 2: %v", err)
	}

	// Check if keys were loaded
	if !verifier2.IsAuthorTrusted("author1") {
		t.Error("Author 1 should be trusted after loading")
	}

	if !verifier2.IsAuthorTrusted("author2") {
		t.Error("Author 2 should be trusted after loading")
	}

	// Verify key data matches
	authors := verifier2.GetTrustedAuthors()
	if len(authors) != 2 {
		t.Errorf("Expected 2 trusted authors, got %d", len(authors))
	}
}

func TestGetTrustedAuthors(t *testing.T) {
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "trusted_keys.json")

	verifier, err := NewSignatureVerifier(keyFile)
	if err != nil {
		t.Fatalf("Failed to create verifier: %v", err)
	}

	// Initially should be empty
	authors := verifier.GetTrustedAuthors()
	if len(authors) != 0 {
		t.Errorf("Expected 0 trusted authors initially, got %d", len(authors))
	}

	// Add keys
	for i := 1; i <= 3; i++ {
		pubKey, _, err := GenerateKeyPair()
		if err != nil {
			t.Fatalf("Failed to generate key pair %d: %v", i, err)
		}
		author := "author" + string(rune('0'+i))
		if err := verifier.AddTrustedKey(author, pubKey, "Test"); err != nil {
			t.Fatalf("Failed to add key %d: %v", i, err)
		}
	}

	// Check count
	authors = verifier.GetTrustedAuthors()
	if len(authors) != 3 {
		t.Errorf("Expected 3 trusted authors, got %d", len(authors))
	}
}

func TestInvalidKeyFile(t *testing.T) {
	// Try to load from non-existent file (should succeed with empty keys)
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "nonexistent.json")

	verifier, err := NewSignatureVerifier(keyFile)
	if err != nil {
		t.Fatalf("Should succeed even with non-existent key file: %v", err)
	}

	authors := verifier.GetTrustedAuthors()
	if len(authors) != 0 {
		t.Errorf("Expected 0 authors with non-existent file, got %d", len(authors))
	}

	// Try to load from invalid JSON
	invalidJSON := filepath.Join(tempDir, "invalid.json")
	if err := os.WriteFile(invalidJSON, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write invalid JSON: %v", err)
	}

	_, err = NewSignatureVerifier(invalidJSON)
	if err == nil {
		t.Error("Should fail with invalid JSON")
	}
}

func TestInvalidPublicKeySize(t *testing.T) {
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "trusted_keys.json")

	// Write invalid key data
	invalidKeyData := `[{
		"author": "test",
		"public_key": "` + base64.StdEncoding.EncodeToString([]byte("short")) + `",
		"added_at": "2023-01-01T00:00:00Z"
	}]`

	if err := os.WriteFile(keyFile, []byte(invalidKeyData), 0644); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	_, err := NewSignatureVerifier(keyFile)
	if err == nil {
		t.Error("Should fail with invalid key size")
	}
}

func TestSignPluginInvalidPrivateKey(t *testing.T) {
	// Try to sign with invalid private key
	invalidKey := []byte("short")
	data := []byte("test data")

	_, err := SignPlugin(data, invalidKey)
	if err == nil {
		t.Error("Should fail with invalid private key size")
	}
}
