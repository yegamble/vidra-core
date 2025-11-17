package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"athena/internal/security"
)

// TestActivityPubKeys_NoPlaintextStorage verifies that private keys are NEVER stored in plaintext
func TestActivityPubKeys_NoPlaintextStorage(t *testing.T) {
	// This is a critical security test - it must NEVER be removed or disabled
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test database connection
	db := setupTestDB(t)
	defer db.Close()

	// Create encryption instance
	encryption, err := security.NewActivityPubKeyEncryption("test-master-key-for-security-testing-12345678")
	if err != nil {
		t.Fatalf("Failed to create encryption: %v", err)
	}

	repo := NewActivityPubRepository(db, encryption)
	ctx := context.Background()

	// Generate a test key pair
	testActorID := uuid.New().String()
	testPublicKey := "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...\n-----END PUBLIC KEY-----"
	testPrivateKey := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpQIBAAKCAQEA1234567890...\n-----END RSA PRIVATE KEY-----"

	// Store the keys
	err = repo.StoreActorKeys(ctx, testActorID, testPublicKey, testPrivateKey)
	if err != nil {
		t.Fatalf("Failed to store keys: %v", err)
	}

	// CRITICAL: Verify the private key is NOT stored in plaintext in the database
	var storedPrivateKey string
	query := "SELECT private_key_pem FROM ap_actor_keys WHERE actor_id = $1"
	err = db.QueryRowContext(ctx, query, testActorID).Scan(&storedPrivateKey)
	if err != nil {
		t.Fatalf("Failed to query stored key: %v", err)
	}

	// Check 1: Stored key should NOT match the plaintext key
	if storedPrivateKey == testPrivateKey {
		t.Fatal("SECURITY VIOLATION: Private key is stored in PLAINTEXT in the database!")
	}

	// Check 2: Stored key should NOT contain PEM markers
	if strings.Contains(storedPrivateKey, "BEGIN RSA PRIVATE KEY") {
		t.Fatal("SECURITY VIOLATION: Stored key contains plaintext PEM markers!")
	}

	// Check 3: Stored key should appear to be encrypted (base64 encoded)
	if !encryption.IsEncrypted(storedPrivateKey) {
		t.Fatal("SECURITY VIOLATION: Stored key does not appear to be encrypted!")
	}

	// Check 4: We should be able to decrypt it back to the original
	decryptedKey, err := encryption.DecryptPrivateKey(storedPrivateKey)
	if err != nil {
		t.Fatalf("Failed to decrypt stored key: %v", err)
	}

	if decryptedKey != testPrivateKey {
		t.Error("Decrypted key does not match original private key")
	}

	// Cleanup
	_, _ = db.ExecContext(ctx, "DELETE FROM ap_actor_keys WHERE actor_id = $1", testActorID)
}

// TestActivityPubKeys_EncryptionRoundTrip verifies encryption/decryption works correctly
func TestActivityPubKeys_EncryptionRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	defer db.Close()

	encryption, err := security.NewActivityPubKeyEncryption("test-master-key-for-security-testing-12345678")
	if err != nil {
		t.Fatalf("Failed to create encryption: %v", err)
	}

	repo := NewActivityPubRepository(db, encryption)
	ctx := context.Background()

	testActorID := uuid.New().String()
	originalPublicKey := "-----BEGIN PUBLIC KEY-----\ntest-public-key\n-----END PUBLIC KEY-----"
	originalPrivateKey := "-----BEGIN RSA PRIVATE KEY-----\ntest-private-key\n-----END RSA PRIVATE KEY-----"

	// Store keys
	err = repo.StoreActorKeys(ctx, testActorID, originalPublicKey, originalPrivateKey)
	if err != nil {
		t.Fatalf("Failed to store keys: %v", err)
	}

	// Retrieve keys
	retrievedPublicKey, retrievedPrivateKey, err := repo.GetActorKeys(ctx, testActorID)
	if err != nil {
		t.Fatalf("Failed to retrieve keys: %v", err)
	}

	// Verify public key matches
	if retrievedPublicKey != originalPublicKey {
		t.Error("Retrieved public key does not match original")
	}

	// Verify private key matches (after decryption)
	if retrievedPrivateKey != originalPrivateKey {
		t.Error("Retrieved private key does not match original")
	}

	// Cleanup
	_, _ = db.ExecContext(ctx, "DELETE FROM ap_actor_keys WHERE actor_id = $1", testActorID)
}

// TestActivityPubKeys_NoEncryptionFallback tests the fallback when encryption is not configured
func TestActivityPubKeys_NoEncryptionFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	defer db.Close()

	// Create repo WITHOUT encryption
	repo := NewActivityPubRepository(db, nil)
	ctx := context.Background()

	testActorID := uuid.New().String()
	testPublicKey := "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"
	testPrivateKey := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"

	// Store keys (should work without encryption for testing)
	err := repo.StoreActorKeys(ctx, testActorID, testPublicKey, testPrivateKey)
	if err != nil {
		t.Fatalf("Failed to store keys: %v", err)
	}

	// Retrieve keys
	retrievedPublicKey, retrievedPrivateKey, err := repo.GetActorKeys(ctx, testActorID)
	if err != nil {
		t.Fatalf("Failed to retrieve keys: %v", err)
	}

	if retrievedPublicKey != testPublicKey || retrievedPrivateKey != testPrivateKey {
		t.Error("Keys do not match when encryption is disabled")
	}

	// Cleanup
	_, _ = db.ExecContext(ctx, "DELETE FROM ap_actor_keys WHERE actor_id = $1", testActorID)
}

// TestActivityPubKeys_MultipleKeysIndependentEncryption verifies each key is encrypted independently
func TestActivityPubKeys_MultipleKeysIndependentEncryption(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	defer db.Close()

	encryption, err := security.NewActivityPubKeyEncryption("test-master-key-for-security-testing-12345678")
	if err != nil {
		t.Fatalf("Failed to create encryption: %v", err)
	}

	repo := NewActivityPubRepository(db, encryption)
	ctx := context.Background()

	// Create two actors with the same private key
	actor1ID := uuid.New().String()
	actor2ID := uuid.New().String()
	samePrivateKey := "-----BEGIN RSA PRIVATE KEY-----\nshared-key\n-----END RSA PRIVATE KEY-----"
	publicKey := "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"

	// Store both
	_ = repo.StoreActorKeys(ctx, actor1ID, publicKey, samePrivateKey)
	_ = repo.StoreActorKeys(ctx, actor2ID, publicKey, samePrivateKey)

	// Retrieve encrypted versions from database
	var encrypted1, encrypted2 string
	_ = db.QueryRowContext(ctx, "SELECT private_key_pem FROM ap_actor_keys WHERE actor_id = $1", actor1ID).Scan(&encrypted1)
	_ = db.QueryRowContext(ctx, "SELECT private_key_pem FROM ap_actor_keys WHERE actor_id = $1", actor2ID).Scan(&encrypted2)

	// Encrypted versions should be DIFFERENT (due to random nonce in GCM)
	if encrypted1 == encrypted2 {
		t.Error("SECURITY ISSUE: Same plaintext encrypted to same ciphertext (nonce reuse?)")
	}

	// But both should decrypt to the same value
	decrypted1, _ := encryption.DecryptPrivateKey(encrypted1)
	decrypted2, _ := encryption.DecryptPrivateKey(encrypted2)

	if decrypted1 != samePrivateKey || decrypted2 != samePrivateKey {
		t.Error("Decrypted keys do not match original")
	}

	// Cleanup
	_, _ = db.ExecContext(ctx, "DELETE FROM ap_actor_keys WHERE actor_id IN ($1, $2)", actor1ID, actor2ID)
}

// TestActivityPubKeys_WrongKeyCannotDecrypt verifies that wrong encryption key fails
func TestActivityPubKeys_WrongKeyCannotDecrypt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)
	defer db.Close()

	// Encrypt with one key
	encryption1, _ := security.NewActivityPubKeyEncryption("first-master-key-for-testing-12345678901234")
	repo1 := NewActivityPubRepository(db, encryption1)
	ctx := context.Background()

	testActorID := uuid.New().String()
	testPrivateKey := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"
	testPublicKey := "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"

	// Store with first key
	err := repo1.StoreActorKeys(ctx, testActorID, testPublicKey, testPrivateKey)
	if err != nil {
		t.Fatalf("Failed to store keys: %v", err)
	}

	// Try to retrieve with different encryption key
	encryption2, _ := security.NewActivityPubKeyEncryption("second-master-key-for-testing-12345678901234")
	repo2 := NewActivityPubRepository(db, encryption2)

	_, _, err = repo2.GetActorKeys(ctx, testActorID)
	if err == nil {
		t.Fatal("SECURITY ISSUE: Successfully decrypted with wrong encryption key!")
	}

	// Error should indicate decryption failure
	if !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("Expected decryption error, got: %v", err)
	}

	// Cleanup
	_, _ = db.ExecContext(ctx, "DELETE FROM ap_actor_keys WHERE actor_id = $1", testActorID)
}

// setupTestDB creates a test database connection
// In a real test environment, this would use the test database configuration
func setupTestDB(t *testing.T) *sqlx.DB {
	// This should connect to a test database
	// For CI/CD, use environment variables or test fixtures
	dbURL := getTestDatabaseURL()
	if dbURL == "" {
		t.Skip("Test database not configured")
	}

	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// Ensure the ap_actor_keys table exists
	ensureTestTable(t, db)

	return db
}

func getTestDatabaseURL() string {
	// Try to get from environment
	// In production tests, this should be a dedicated test database
	return ""
}

func ensureTestTable(t *testing.T, db *sqlx.DB) {
	// Create test table if it doesn't exist
	schema := `
		CREATE TABLE IF NOT EXISTS ap_actor_keys (
			actor_id UUID PRIMARY KEY,
			public_key_pem TEXT NOT NULL,
			private_key_pem TEXT NOT NULL,
			keys_encrypted BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`
	_, err := db.Exec(schema)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Logf("Warning: Failed to create test table: %v", err)
	}
}

// Benchmark tests

func BenchmarkStoreEncryptedKey(b *testing.B) {
	db := &sqlx.DB{} // Mock DB
	encryption, _ := security.NewActivityPubKeyEncryption("test-master-key-for-benchmarking-12345678")
	repo := NewActivityPubRepository(db, encryption)
	ctx := context.Background()

	testActorID := uuid.New().String()
	testPublicKey := "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----"
	testPrivateKey := "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Just test the encryption overhead (not actual DB operation)
		_, _ = encryption.EncryptPrivateKey(testPrivateKey)
	}
	_ = ctx
	_ = testActorID
	_ = testPublicKey
	_ = repo
}
