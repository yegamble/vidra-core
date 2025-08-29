package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestCryptoService_GenerateX25519KeyPair(t *testing.T) {
	cs := NewCryptoService()

	keyPair, err := cs.GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateX25519KeyPair failed: %v", err)
	}

	if len(keyPair.PrivateKey) != X25519PrivateKeySize {
		t.Errorf("Expected private key size %d, got %d", X25519PrivateKeySize, len(keyPair.PrivateKey))
	}

	if len(keyPair.PublicKey) != X25519PublicKeySize {
		t.Errorf("Expected public key size %d, got %d", X25519PublicKeySize, len(keyPair.PublicKey))
	}

	// Keys should not be all zeros
	allZeros := make([]byte, X25519PrivateKeySize)
	if bytes.Equal(keyPair.PrivateKey, allZeros) {
		t.Error("Private key should not be all zeros")
	}
	if bytes.Equal(keyPair.PublicKey, allZeros) {
		t.Error("Public key should not be all zeros")
	}
}

func TestCryptoService_GenerateEd25519KeyPair(t *testing.T) {
	cs := NewCryptoService()

	keyPair, err := cs.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair failed: %v", err)
	}

	if len(keyPair.PrivateKey) != Ed25519PrivateKeySize {
		t.Errorf("Expected private key size %d, got %d", Ed25519PrivateKeySize, len(keyPair.PrivateKey))
	}

	if len(keyPair.PublicKey) != Ed25519PublicKeySize {
		t.Errorf("Expected public key size %d, got %d", Ed25519PublicKeySize, len(keyPair.PublicKey))
	}
}

func TestCryptoService_ComputeSharedSecret(t *testing.T) {
	cs := NewCryptoService()

	// Generate two key pairs
	keyPair1, err := cs.GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	keyPair2, err := cs.GenerateX25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	// Compute shared secrets
	secret1, err := cs.ComputeSharedSecret(keyPair1.PrivateKey, keyPair2.PublicKey)
	if err != nil {
		t.Fatalf("Failed to compute shared secret 1: %v", err)
	}

	secret2, err := cs.ComputeSharedSecret(keyPair2.PrivateKey, keyPair1.PublicKey)
	if err != nil {
		t.Fatalf("Failed to compute shared secret 2: %v", err)
	}

	// Shared secrets should be equal
	if !bytes.Equal(secret1, secret2) {
		t.Error("Shared secrets should be equal")
	}

	// Secret should not be all zeros
	allZeros := make([]byte, 32)
	if bytes.Equal(secret1, allZeros) {
		t.Error("Shared secret should not be all zeros")
	}
}

func TestCryptoService_ComputeSharedSecret_WeakKeys(t *testing.T) {
	cs := NewCryptoService()

	// Test with weak public key (all zeros)
	privateKey := make([]byte, X25519PrivateKeySize)
	rand.Read(privateKey)

	weakPublicKey := make([]byte, X25519PublicKeySize) // All zeros

	_, err := cs.ComputeSharedSecret(privateKey, weakPublicKey)
	if err == nil {
		t.Error("Expected error for weak public key, got nil")
	}
}

func TestCryptoService_DeriveKeyFromPassword(t *testing.T) {
	cs := NewCryptoService()

	password := "test-password-123"
	salt, err := cs.GenerateSalt()
	if err != nil {
		t.Fatalf("Failed to generate salt: %v", err)
	}

	key1, err := cs.DeriveKeyFromPassword(password, salt)
	if err != nil {
		t.Fatalf("Failed to derive key 1: %v", err)
	}

	key2, err := cs.DeriveKeyFromPassword(password, salt)
	if err != nil {
		t.Fatalf("Failed to derive key 2: %v", err)
	}

	// Same password and salt should produce same key
	if !bytes.Equal(key1, key2) {
		t.Error("Same password and salt should produce same key")
	}

	// Different salt should produce different key
	salt2, err := cs.GenerateSalt()
	if err != nil {
		t.Fatalf("Failed to generate salt 2: %v", err)
	}

	key3, err := cs.DeriveKeyFromPassword(password, salt2)
	if err != nil {
		t.Fatalf("Failed to derive key 3: %v", err)
	}

	if bytes.Equal(key1, key3) {
		t.Error("Different salts should produce different keys")
	}

	// Different password should produce different key
	key4, err := cs.DeriveKeyFromPassword("different-password", salt)
	if err != nil {
		t.Fatalf("Failed to derive key 4: %v", err)
	}

	if bytes.Equal(key1, key4) {
		t.Error("Different passwords should produce different keys")
	}

	// Check key length
	if len(key1) != Argon2KeySize {
		t.Errorf("Expected key size %d, got %d", Argon2KeySize, len(key1))
	}
}

func TestCryptoService_EncryptDecrypt(t *testing.T) {
	cs := NewCryptoService()

	plaintext := []byte("This is a secret message for testing encryption")
	key := make([]byte, ChaCha20KeySize)
	rand.Read(key)

	nonce, err := cs.GenerateNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce: %v", err)
	}

	// Encrypt
	ciphertext, err := cs.Encrypt(plaintext, key, nonce)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Ciphertext should be different from plaintext
	if bytes.Equal(plaintext, ciphertext) {
		t.Error("Ciphertext should be different from plaintext")
	}

	// Decrypt
	decrypted, err := cs.Decrypt(ciphertext, key, nonce)
	if err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}

	// Decrypted should equal original plaintext
	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Decrypted text should equal original plaintext")
	}
}

func TestCryptoService_EncryptDecrypt_WrongKey(t *testing.T) {
	cs := NewCryptoService()

	plaintext := []byte("Secret message")
	key1 := make([]byte, ChaCha20KeySize)
	key2 := make([]byte, ChaCha20KeySize)
	rand.Read(key1)
	rand.Read(key2)

	nonce, err := cs.GenerateNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce: %v", err)
	}

	// Encrypt with key1
	ciphertext, err := cs.Encrypt(plaintext, key1, nonce)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Try to decrypt with key2 (wrong key)
	_, err = cs.Decrypt(ciphertext, key2, nonce)
	if err == nil {
		t.Error("Decryption with wrong key should fail")
	}
}

func TestCryptoService_SignVerify(t *testing.T) {
	cs := NewCryptoService()

	keyPair, err := cs.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	message := []byte("This is a message to be signed")

	// Sign message
	signature, err := cs.SignMessage(message, keyPair.PrivateKey)
	if err != nil {
		t.Fatalf("Failed to sign message: %v", err)
	}

	// Verify signature
	valid := cs.VerifySignature(message, signature, keyPair.PublicKey)
	if !valid {
		t.Error("Valid signature should verify successfully")
	}

	// Verify with wrong message
	wrongMessage := []byte("This is a different message")
	valid = cs.VerifySignature(wrongMessage, signature, keyPair.PublicKey)
	if valid {
		t.Error("Signature should not verify with wrong message")
	}

	// Verify with wrong public key
	wrongKeyPair, err := cs.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("Failed to generate wrong key pair: %v", err)
	}

	valid = cs.VerifySignature(message, signature, wrongKeyPair.PublicKey)
	if valid {
		t.Error("Signature should not verify with wrong public key")
	}
}

func TestCryptoService_EncryptDecryptWithMasterKey(t *testing.T) {
	cs := NewCryptoService()

	plaintext := []byte("Secret data to encrypt with master key")
	masterKey := make([]byte, ChaCha20KeySize)
	rand.Read(masterKey)

	// Encrypt
	encData, err := cs.EncryptWithMasterKey(plaintext, masterKey)
	if err != nil {
		t.Fatalf("Encryption with master key failed: %v", err)
	}

	if encData.Version != 1 {
		t.Errorf("Expected version 1, got %d", encData.Version)
	}

	if len(encData.Nonce) != XChaCha20NonceSize {
		t.Errorf("Expected nonce size %d, got %d", XChaCha20NonceSize, len(encData.Nonce))
	}

	// Decrypt
	decrypted, err := cs.DecryptWithMasterKey(encData, masterKey)
	if err != nil {
		t.Fatalf("Decryption with master key failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Error("Decrypted data should equal original plaintext")
	}
}

func TestCryptoService_ValidatePublicKey(t *testing.T) {
	cs := NewCryptoService()

	// Valid key
	validKey := make([]byte, X25519PublicKeySize)
	rand.Read(validKey)

	err := cs.ValidatePublicKey(validKey)
	if err != nil {
		t.Errorf("Valid key should pass validation: %v", err)
	}

	// Invalid size
	invalidSizeKey := make([]byte, 16)
	err = cs.ValidatePublicKey(invalidSizeKey)
	if err == nil {
		t.Error("Invalid size key should fail validation")
	}

	// All zeros (weak key)
	weakKey := make([]byte, X25519PublicKeySize)
	err = cs.ValidatePublicKey(weakKey)
	if err == nil {
		t.Error("Weak key (all zeros) should fail validation")
	}
}

func TestCryptoService_Base64EncodeDecode(t *testing.T) {
	cs := NewCryptoService()

	originalData := []byte("Test data for base64 encoding")

	// Encode
	encoded := cs.Base64Encode(originalData)
	if len(encoded) == 0 {
		t.Error("Encoded data should not be empty")
	}

	// Decode
	decoded, err := cs.Base64Decode(encoded)
	if err != nil {
		t.Fatalf("Base64 decode failed: %v", err)
	}

	if !bytes.Equal(originalData, decoded) {
		t.Error("Decoded data should equal original data")
	}

	// Test invalid base64
	_, err = cs.Base64Decode("invalid base64 !!!")
	if err == nil {
		t.Error("Invalid base64 should fail to decode")
	}
}

func TestCryptoService_SecureCompare(t *testing.T) {
	cs := NewCryptoService()

	data1 := []byte("same data")
	data2 := []byte("same data")
	data3 := []byte("different data")

	// Same data should compare equal
	if !cs.SecureCompare(data1, data2) {
		t.Error("Same data should compare equal")
	}

	// Different data should not compare equal
	if cs.SecureCompare(data1, data3) {
		t.Error("Different data should not compare equal")
	}

	// Different lengths should not compare equal
	data4 := []byte("same")
	if cs.SecureCompare(data1, data4) {
		t.Error("Different length data should not compare equal")
	}
}

func TestCryptoService_ZeroMemory(t *testing.T) {
	cs := NewCryptoService()

	data := []byte("sensitive data to be zeroed")
	originalData := make([]byte, len(data))
	copy(originalData, data)

	cs.ZeroMemory(data)

	// All bytes should be zero
	for i, b := range data {
		if b != 0 {
			t.Errorf("Byte at index %d should be zero, got %d", i, b)
		}
	}

	// Should be different from original
	if bytes.Equal(data, originalData) {
		t.Error("Zeroed data should be different from original")
	}
}

func TestCryptoService_GenerateNonce(t *testing.T) {
	cs := NewCryptoService()

	nonce1, err := cs.GenerateNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce 1: %v", err)
	}

	nonce2, err := cs.GenerateNonce()
	if err != nil {
		t.Fatalf("Failed to generate nonce 2: %v", err)
	}

	if len(nonce1) != XChaCha20NonceSize {
		t.Errorf("Expected nonce size %d, got %d", XChaCha20NonceSize, len(nonce1))
	}

	// Nonces should be different
	if bytes.Equal(nonce1, nonce2) {
		t.Error("Different nonce generations should produce different nonces")
	}

	// Nonces should not be all zeros
	allZeros := make([]byte, XChaCha20NonceSize)
	if bytes.Equal(nonce1, allZeros) {
		t.Error("Nonce should not be all zeros")
	}
}

func TestCryptoService_GenerateSalt(t *testing.T) {
	cs := NewCryptoService()

	salt1, err := cs.GenerateSalt()
	if err != nil {
		t.Fatalf("Failed to generate salt 1: %v", err)
	}

	salt2, err := cs.GenerateSalt()
	if err != nil {
		t.Fatalf("Failed to generate salt 2: %v", err)
	}

	if len(salt1) != Argon2SaltSize {
		t.Errorf("Expected salt size %d, got %d", Argon2SaltSize, len(salt1))
	}

	// Salts should be different
	if bytes.Equal(salt1, salt2) {
		t.Error("Different salt generations should produce different salts")
	}

	// Salts should not be all zeros
	allZeros := make([]byte, Argon2SaltSize)
	if bytes.Equal(salt1, allZeros) {
		t.Error("Salt should not be all zeros")
	}
}

// Benchmark tests
func BenchmarkCryptoService_GenerateX25519KeyPair(b *testing.B) {
	cs := NewCryptoService()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := cs.GenerateX25519KeyPair()
		if err != nil {
			b.Fatalf("GenerateX25519KeyPair failed: %v", err)
		}
	}
}

func BenchmarkCryptoService_ComputeSharedSecret(b *testing.B) {
	cs := NewCryptoService()
	keyPair1, _ := cs.GenerateX25519KeyPair()
	keyPair2, _ := cs.GenerateX25519KeyPair()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := cs.ComputeSharedSecret(keyPair1.PrivateKey, keyPair2.PublicKey)
		if err != nil {
			b.Fatalf("ComputeSharedSecret failed: %v", err)
		}
	}
}

func BenchmarkCryptoService_Encrypt(b *testing.B) {
	cs := NewCryptoService()
	plaintext := []byte("This is a benchmark message for encryption testing")
	key := make([]byte, ChaCha20KeySize)
	rand.Read(key)
	nonce, _ := cs.GenerateNonce()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := cs.Encrypt(plaintext, key, nonce)
		if err != nil {
			b.Fatalf("Encrypt failed: %v", err)
		}
	}
}

func BenchmarkCryptoService_DeriveKeyFromPassword(b *testing.B) {
	cs := NewCryptoService()
	password := "benchmark-password"
	salt, _ := cs.GenerateSalt()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := cs.DeriveKeyFromPassword(password, salt)
		if err != nil {
			b.Fatalf("DeriveKeyFromPassword failed: %v", err)
		}
	}
}
