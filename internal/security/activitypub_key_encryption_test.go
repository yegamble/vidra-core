package security

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestNewActivityPubKeyEncryption(t *testing.T) {
	tests := []struct {
		name      string
		masterKey string
		wantErr   bool
	}{
		{
			name:      "valid master key",
			masterKey: "this-is-a-secure-master-key-for-testing-purposes-12345",
			wantErr:   false,
		},
		{
			name:      "short master key",
			masterKey: "short",
			wantErr:   true,
		},
		{
			name:      "exactly 32 chars",
			masterKey: "12345678901234567890123456789012",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewActivityPubKeyEncryption(tt.masterKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewActivityPubKeyEncryption() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptDecryptPrivateKey(t *testing.T) {
	// Generate a test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	// Encode to PEM
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM := string(pem.EncodeToMemory(privateKeyBlock))

	// Create encryption instance
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, err := NewActivityPubKeyEncryption(masterKey)
	if err != nil {
		t.Fatalf("Failed to create encryption instance: %v", err)
	}

	// Test encryption
	encrypted, err := enc.EncryptPrivateKey(privateKeyPEM)
	if err != nil {
		t.Fatalf("EncryptPrivateKey() failed: %v", err)
	}

	// Verify it's actually encrypted (not plaintext)
	if strings.Contains(encrypted, "BEGIN RSA PRIVATE KEY") {
		t.Error("Encrypted data contains plaintext PEM header")
	}

	// Verify it's different from original
	if encrypted == privateKeyPEM {
		t.Error("Encrypted data is identical to plaintext")
	}

	// Test decryption
	decrypted, err := enc.DecryptPrivateKey(encrypted)
	if err != nil {
		t.Fatalf("DecryptPrivateKey() failed: %v", err)
	}

	// Verify decrypted matches original
	if decrypted != privateKeyPEM {
		t.Error("Decrypted data does not match original")
	}
}

func TestEncryptPrivateKey_EmptyInput(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	_, err := enc.EncryptPrivateKey("")
	if err == nil {
		t.Error("Expected error for empty private key")
	}
}

func TestDecryptPrivateKey_InvalidInput(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "invalid base64",
			input: "not-valid-base64!@#$",
		},
		{
			name:  "too short ciphertext",
			input: "YWJj", // "abc" in base64
		},
		{
			name:  "tampered ciphertext",
			input: "dGFtcGVyZWQtZGF0YS10aGF0LXdpbGwtZmFpbC1hdXRoZW50aWNhdGlvbg==",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enc.DecryptPrivateKey(tt.input)
			if err == nil {
				t.Error("Expected error for invalid input")
			}
		})
	}
}

func TestDecryptPrivateKey_WrongKey(t *testing.T) {
	// Encrypt with one key
	masterKey1 := "first-master-key-for-testing-12345678901234"
	enc1, _ := NewActivityPubKeyEncryption(masterKey1)

	privateKeyPEM := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"
	encrypted, err := enc1.EncryptPrivateKey(privateKeyPEM)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Try to decrypt with different key
	masterKey2 := "second-master-key-for-testing-12345678901234"
	enc2, _ := NewActivityPubKeyEncryption(masterKey2)

	_, err = enc2.DecryptPrivateKey(encrypted)
	if err == nil {
		t.Error("Expected error when decrypting with wrong key")
	}
}

func TestIsEncrypted(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	tests := []struct {
		name     string
		data     string
		expected bool
	}{
		{
			name:     "plaintext PEM key",
			data:     "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----",
			expected: false,
		},
		{
			name:     "non-base64 text",
			data:     "this is just plain text",
			expected: false,
		},
		{
			name:     "empty string",
			data:     "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enc.IsEncrypted(tt.data)
			if result != tt.expected {
				t.Errorf("IsEncrypted() = %v, want %v", result, tt.expected)
			}
		})
	}

	// Test with actual encrypted data
	t.Run("actual encrypted data", func(t *testing.T) {
		privateKeyPEM := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"
		encrypted, _ := enc.EncryptPrivateKey(privateKeyPEM)

		if !enc.IsEncrypted(encrypted) {
			t.Error("IsEncrypted() should return true for encrypted data")
		}
	})
}

func TestMigrateToEncrypted(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	// Test migrating plaintext key
	t.Run("migrate plaintext", func(t *testing.T) {
		plaintext := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"
		encrypted, err := enc.MigrateToEncrypted(plaintext)
		if err != nil {
			t.Fatalf("MigrateToEncrypted() failed: %v", err)
		}

		// Should be encrypted now
		if !enc.IsEncrypted(encrypted) {
			t.Error("Migrated key should be encrypted")
		}

		// Should decrypt back to original
		decrypted, err := enc.DecryptPrivateKey(encrypted)
		if err != nil {
			t.Fatalf("Failed to decrypt migrated key: %v", err)
		}
		if decrypted != plaintext {
			t.Error("Decrypted migrated key doesn't match original")
		}
	})

	// Test migrating already encrypted key
	t.Run("migrate already encrypted", func(t *testing.T) {
		plaintext := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"
		encrypted, _ := enc.EncryptPrivateKey(plaintext)

		// Migrate again
		reencrypted, err := enc.MigrateToEncrypted(encrypted)
		if err != nil {
			t.Fatalf("MigrateToEncrypted() failed on already encrypted key: %v", err)
		}

		// Should return the same encrypted value
		if reencrypted != encrypted {
			t.Error("MigrateToEncrypted() should not re-encrypt already encrypted keys")
		}
	})
}

func TestEncryption_MultipleEncryptions(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	plaintext := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"

	// Encrypt the same data multiple times
	encrypted1, _ := enc.EncryptPrivateKey(plaintext)
	encrypted2, _ := enc.EncryptPrivateKey(plaintext)

	// Encrypted values should be different (due to random nonce)
	if encrypted1 == encrypted2 {
		t.Error("Multiple encryptions of same data should produce different ciphertext (IND-CPA)")
	}

	// But both should decrypt to the same plaintext
	decrypted1, _ := enc.DecryptPrivateKey(encrypted1)
	decrypted2, _ := enc.DecryptPrivateKey(encrypted2)

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Error("Both encrypted values should decrypt to the same plaintext")
	}
}

// TestEncryption_NoPlaintextLeakage verifies that encrypted keys don't leak plaintext
func TestEncryption_NoPlaintextLeakage(t *testing.T) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	// Generate a real RSA key
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM := string(pem.EncodeToMemory(privateKeyBlock))

	encrypted, _ := enc.EncryptPrivateKey(privateKeyPEM)

	// Check that none of the distinctive PEM markers are in the encrypted output
	markers := []string{
		"BEGIN RSA PRIVATE KEY",
		"END RSA PRIVATE KEY",
		"BEGIN PRIVATE KEY",
		"END PRIVATE KEY",
	}

	for _, marker := range markers {
		if strings.Contains(encrypted, marker) {
			t.Errorf("Encrypted data contains plaintext marker: %s", marker)
		}
	}

	// Check that the encrypted data doesn't contain recognizable parts of the key
	// (This is a basic check - in real scenarios, you'd want more sophisticated tests)
	if strings.Contains(encrypted, string(privateKeyBytes[:20])) {
		t.Error("Encrypted data appears to contain plaintext key material")
	}
}

// Benchmark encryption performance
func BenchmarkEncryptPrivateKey(b *testing.B) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM := string(pem.EncodeToMemory(privateKeyBlock))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.EncryptPrivateKey(privateKeyPEM)
	}
}

// Benchmark decryption performance
func BenchmarkDecryptPrivateKey(b *testing.B) {
	masterKey := "test-master-key-for-encryption-testing-12345678"
	enc, _ := NewActivityPubKeyEncryption(masterKey)

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM := string(pem.EncodeToMemory(privateKeyBlock))

	encrypted, _ := enc.EncryptPrivateKey(privateKeyPEM)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.DecryptPrivateKey(encrypted)
	}
}
