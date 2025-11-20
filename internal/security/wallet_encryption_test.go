package security

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWalletEncryption_EncryptDecrypt tests basic encryption and decryption
func TestWalletEncryption_EncryptDecrypt(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)

	err = hsm.AddMasterKey("test-master-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "test-master-key")
	ctx := context.Background()

	testSeed := strings.Repeat("a", 64) // Valid 64-character seed

	// Encrypt
	encrypted, err := service.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)
	assert.NotNil(t, encrypted)
	assert.NotEmpty(t, encrypted.EncryptedSeed)
	assert.NotEmpty(t, encrypted.SeedNonce)
	assert.Equal(t, "test-master-key", encrypted.KeyID)
	assert.NotEqual(t, []byte(testSeed), encrypted.EncryptedSeed, "Seed should be encrypted")

	// Decrypt
	decrypted, err := service.DecryptSeed(ctx, encrypted)
	require.NoError(t, err)
	assert.Equal(t, testSeed, decrypted, "Decrypted seed should match original")
}

// TestWalletEncryption_EnvelopeEncryption tests envelope encryption pattern
func TestWalletEncryption_EnvelopeEncryption(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)

	err = hsm.AddMasterKey("envelope-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "envelope-key")
	service.envelopeEnabled = true
	ctx := context.Background()

	testSeed := strings.Repeat("b", 64)

	// Encrypt with envelope
	encrypted, err := service.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)

	// Verify envelope encryption fields are populated
	assert.NotEmpty(t, encrypted.EncryptedDataKey, "Envelope encryption should include encrypted data key")
	assert.NotEmpty(t, encrypted.DataKeyNonce, "Envelope encryption should include data key nonce")
	assert.Equal(t, "ENVELOPE-AES-256-GCM", encrypted.Algorithm)

	// Decrypt
	decrypted, err := service.DecryptSeed(ctx, encrypted)
	require.NoError(t, err)
	assert.Equal(t, testSeed, decrypted)
}

// TestWalletEncryption_DirectEncryption tests direct HSM encryption
func TestWalletEncryption_DirectEncryption(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)

	err = hsm.AddMasterKey("direct-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "direct-key")
	service.envelopeEnabled = false // Disable envelope encryption
	ctx := context.Background()

	testSeed := strings.Repeat("c", 64)

	// Encrypt directly
	encrypted, err := service.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)

	// Verify direct encryption (no envelope fields)
	assert.Empty(t, encrypted.EncryptedDataKey, "Direct encryption should not have data key")
	assert.Empty(t, encrypted.DataKeyNonce, "Direct encryption should not have data key nonce")

	// Decrypt
	decrypted, err := service.DecryptSeed(ctx, encrypted)
	require.NoError(t, err)
	assert.Equal(t, testSeed, decrypted)
}

// TestWalletEncryption_DifferentSeeds tests that different seeds produce different ciphertexts
func TestWalletEncryption_DifferentSeeds(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)

	err = hsm.AddMasterKey("test-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "test-key")
	ctx := context.Background()

	seed1 := strings.Repeat("a", 64)
	seed2 := strings.Repeat("b", 64)

	enc1, err := service.EncryptSeed(ctx, seed1)
	require.NoError(t, err)

	enc2, err := service.EncryptSeed(ctx, seed2)
	require.NoError(t, err)

	// Ciphertexts should be different
	assert.NotEqual(t, enc1.EncryptedSeed, enc2.EncryptedSeed)
}

// TestWalletEncryption_SameSeedDifferentCiphertext tests that same seed produces different ciphertext
func TestWalletEncryption_SameSeedDifferentCiphertext(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)

	err = hsm.AddMasterKey("test-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "test-key")
	ctx := context.Background()

	seed := strings.Repeat("a", 64)

	// Encrypt same seed twice
	enc1, err := service.EncryptSeed(ctx, seed)
	require.NoError(t, err)

	enc2, err := service.EncryptSeed(ctx, seed)
	require.NoError(t, err)

	// Ciphertexts should be different (due to different nonces)
	assert.NotEqual(t, enc1.EncryptedSeed, enc2.EncryptedSeed)
	assert.NotEqual(t, enc1.SeedNonce, enc2.SeedNonce)

	// But both should decrypt to same seed
	dec1, err := service.DecryptSeed(ctx, enc1)
	require.NoError(t, err)
	dec2, err := service.DecryptSeed(ctx, enc2)
	require.NoError(t, err)
	assert.Equal(t, seed, dec1)
	assert.Equal(t, seed, dec2)
}

// TestWalletEncryption_KeyRotation tests key rotation functionality
func TestWalletEncryption_KeyRotation(t *testing.T) {
	hsm := NewSoftwareHSM()

	oldKey := make([]byte, 32)
	_, err := rand.Read(oldKey)
	require.NoError(t, err)
	err = hsm.AddMasterKey("old-key", oldKey)
	require.NoError(t, err)

	newKey := make([]byte, 32)
	_, err = rand.Read(newKey)
	require.NoError(t, err)
	err = hsm.AddMasterKey("new-key", newKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "old-key")
	ctx := context.Background()

	testSeed := strings.Repeat("a", 64)

	// Encrypt with old key
	oldEncrypted, err := service.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)
	assert.Equal(t, "old-key", oldEncrypted.KeyID)

	// Rotate to new key
	newEncrypted, err := service.RotateEncryption(ctx, oldEncrypted, "new-key")
	require.NoError(t, err)
	assert.Equal(t, "new-key", newEncrypted.KeyID)

	// Verify data is different
	assert.NotEqual(t, oldEncrypted.EncryptedSeed, newEncrypted.EncryptedSeed)

	// Both should decrypt to same seed
	newService := NewWalletEncryptionService(hsm, "new-key")
	decrypted, err := newService.DecryptSeed(ctx, newEncrypted)
	require.NoError(t, err)
	assert.Equal(t, testSeed, decrypted)
}

// TestWalletEncryption_InvalidKey tests decryption with wrong key
func TestWalletEncryption_InvalidKey(t *testing.T) {
	hsm := NewSoftwareHSM()

	key1 := make([]byte, 32)
	_, err := rand.Read(key1)
	require.NoError(t, err)
	err = hsm.AddMasterKey("key1", key1)
	require.NoError(t, err)

	key2 := make([]byte, 32)
	_, err = rand.Read(key2)
	require.NoError(t, err)
	err = hsm.AddMasterKey("key2", key2)
	require.NoError(t, err)

	service1 := NewWalletEncryptionService(hsm, "key1")
	ctx := context.Background()

	testSeed := strings.Repeat("a", 64)

	// Encrypt with key1
	encrypted, err := service1.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)

	// Try to decrypt with key2 - should fail
	service2 := NewWalletEncryptionService(hsm, "key2")
	encrypted.KeyID = "key2" // Manually change to wrong key
	_, err = service2.DecryptSeed(ctx, encrypted)
	assert.Error(t, err, "Decryption with wrong key should fail")
}

// TestWalletEncryption_EmptySeed tests validation of empty seed
func TestWalletEncryption_EmptySeed(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)
	err = hsm.AddMasterKey("test-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "test-key")
	ctx := context.Background()

	// Empty seed should error
	_, err = service.EncryptSeed(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "seed cannot be empty")
}

// TestWalletEncryption_CorruptedCiphertext tests tampered ciphertext detection
func TestWalletEncryption_CorruptedCiphertext(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)
	err = hsm.AddMasterKey("test-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "test-key")
	ctx := context.Background()

	testSeed := strings.Repeat("a", 64)

	// Encrypt
	encrypted, err := service.EncryptSeed(ctx, testSeed)
	require.NoError(t, err)

	// Corrupt ciphertext
	encrypted.EncryptedSeed[0] ^= 0xFF

	// Decryption should fail
	_, err = service.DecryptSeed(ctx, encrypted)
	assert.Error(t, err, "Corrupted ciphertext should fail decryption")
}

// TestValidateSeedStrength tests seed strength validation
func TestValidateSeedStrength(t *testing.T) {
	tests := []struct {
		name    string
		seed    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid 64 character seed",
			seed:    "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4",
			wantErr: false,
		},
		{
			name:    "valid 81 character seed (IOTA legacy)",
			seed:    "ABCDEFGHIJKLMNOPQRSTUVWXYZ9ABCDEFGHIJKLMNOPQRSTUVWXYZ9ABCDEFGHIJKLMNOPQRSTUVWXY",
			wantErr: false,
		},
		{
			name:    "too short - 32 chars",
			seed:    strings.Repeat("a", 32),
			wantErr: true,
			errMsg:  "seed too short",
		},
		{
			name:    "too short - 63 chars",
			seed:    strings.Repeat("a", 63),
			wantErr: true,
			errMsg:  "seed too short",
		},
		{
			name:    "too long - 300 chars",
			seed:    strings.Repeat("a", 300),
			wantErr: true,
			errMsg:  "seed too long",
		},
		{
			name:    "weak seed - all same character",
			seed:    strings.Repeat("A", 64),
			wantErr: true,
			errMsg:  "weak seed",
		},
		{
			name:    "empty seed",
			seed:    "",
			wantErr: true,
			errMsg:  "seed too short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSeedStrength(tt.seed)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestWalletEncryption_ConcurrentOperations tests thread safety
func TestWalletEncryption_ConcurrentOperations(t *testing.T) {
	hsm := NewSoftwareHSM()
	masterKey := make([]byte, 32)
	_, err := rand.Read(masterKey)
	require.NoError(t, err)
	err = hsm.AddMasterKey("concurrent-key", masterKey)
	require.NoError(t, err)

	service := NewWalletEncryptionService(hsm, "concurrent-key")
	ctx := context.Background()

	// Run concurrent encryptions
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(index int) {
			seed := strings.Repeat("a", 64)
			encrypted, err := service.EncryptSeed(ctx, seed)
			assert.NoError(t, err)

			decrypted, err := service.DecryptSeed(ctx, encrypted)
			assert.NoError(t, err)
			assert.Equal(t, seed, decrypted)

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestSecureZeroMemory tests memory zeroing
func TestSecureZeroMemory(t *testing.T) {
	data := []byte("sensitive-seed-data")
	original := make([]byte, len(data))
	copy(original, data)

	SecureZeroMemory(data)

	// All bytes should be zero
	for i, b := range data {
		assert.Equal(t, byte(0), b, "Byte %d should be zero", i)
	}

	// Original should still have data (different memory)
	assert.NotEqual(t, data, original)
}

// TestZeroSeedString tests string zeroing
func TestZeroSeedString(t *testing.T) {
	seed := strings.Repeat("a", 64)
	ZeroSeedString(&seed)
	assert.Empty(t, seed, "Seed should be empty after zeroing")

	// Test with nil
	var nilSeed *string
	ZeroSeedString(nilSeed) // Should not panic
}
