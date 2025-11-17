package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"athena/internal/config"
	"athena/internal/security"
)

// KeyRecord represents a key record from the database
type KeyRecord struct {
	ActorID       string `db:"actor_id"`
	PublicKeyPem  string `db:"public_key_pem"`
	PrivateKeyPem string `db:"private_key_pem"`
	KeysEncrypted bool   `db:"keys_encrypted"`
}

func main() {
	log.Println("ActivityPub Private Key Encryption Migration Tool")
	log.Println("==================================================")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Check if ActivityPub is enabled
	if !cfg.EnableActivityPub {
		log.Println("ActivityPub is not enabled. Exiting.")
		return
	}

	// Check if encryption key is configured
	if cfg.ActivityPubKeyEncryptionKey == "" {
		log.Fatal("ACTIVITYPUB_KEY_ENCRYPTION_KEY is not set. Please set this environment variable.")
	}

	// Connect to database
	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create encryption instance
	encryption, err := security.NewActivityPubKeyEncryption(cfg.ActivityPubKeyEncryptionKey)
	if err != nil {
		log.Fatalf("Failed to create encryption instance: %v", err)
	}

	// Count total keys to migrate
	var totalKeys int
	err = db.Get(&totalKeys, "SELECT COUNT(*) FROM ap_actor_keys WHERE keys_encrypted = FALSE OR keys_encrypted IS NULL")
	if err != nil {
		log.Fatalf("Failed to count keys: %v", err)
	}

	log.Printf("Found %d private keys to encrypt\n", totalKeys)

	if totalKeys == 0 {
		log.Println("No keys need encryption. All done!")
		return
	}

	// Ask for confirmation
	fmt.Print("\nThis will encrypt all plaintext private keys in the database.\n")
	fmt.Print("This operation cannot be undone without the encryption key.\n")
	fmt.Print("Make sure you have backed up your database!\n\n")
	fmt.Print("Do you want to proceed? (yes/no): ")

	var response string
	fmt.Scanln(&response)

	if response != "yes" {
		log.Println("Migration cancelled.")
		return
	}

	// Fetch all unencrypted keys
	var keys []KeyRecord
	err = db.Select(&keys, "SELECT actor_id, public_key_pem, private_key_pem, COALESCE(keys_encrypted, FALSE) as keys_encrypted FROM ap_actor_keys WHERE keys_encrypted = FALSE OR keys_encrypted IS NULL")
	if err != nil {
		log.Fatalf("Failed to fetch keys: %v", err)
	}

	// Encrypt each key
	ctx := context.Background()
	encrypted := 0
	skipped := 0
	failed := 0

	for i, key := range keys {
		log.Printf("Processing key %d/%d (actor: %s)...\n", i+1, len(keys), key.ActorID)

		// Check if already encrypted
		if encryption.IsEncrypted(key.PrivateKeyPem) {
			log.Printf("  Key appears to already be encrypted, marking as encrypted\n")
			err := markAsEncrypted(ctx, db, key.ActorID)
			if err != nil {
				log.Printf("  ERROR: Failed to mark key as encrypted: %v\n", err)
				failed++
				continue
			}
			skipped++
			continue
		}

		// Encrypt the key
		encryptedKey, err := encryption.EncryptPrivateKey(key.PrivateKeyPem)
		if err != nil {
			log.Printf("  ERROR: Failed to encrypt private key: %v\n", err)
			failed++
			continue
		}

		// Update in database
		err = updateEncryptedKey(ctx, db, key.ActorID, encryptedKey)
		if err != nil {
			log.Printf("  ERROR: Failed to update database: %v\n", err)
			failed++
			continue
		}

		log.Printf("  Successfully encrypted\n")
		encrypted++
	}

	log.Println("\n==================================================")
	log.Printf("Migration complete!\n")
	log.Printf("  Encrypted: %d\n", encrypted)
	log.Printf("  Skipped (already encrypted): %d\n", skipped)
	log.Printf("  Failed: %d\n", failed)

	if failed > 0 {
		log.Printf("\nWARNING: %d keys failed to encrypt. Please review the errors above.\n", failed)
		os.Exit(1)
	}

	log.Println("\nAll private keys have been successfully encrypted!")
}

func updateEncryptedKey(ctx context.Context, db *sqlx.DB, actorID, encryptedPrivateKey string) error {
	query := `
		UPDATE ap_actor_keys
		SET private_key_pem = $1,
		    keys_encrypted = TRUE,
		    updated_at = CURRENT_TIMESTAMP
		WHERE actor_id = $2
	`
	_, err := db.ExecContext(ctx, query, encryptedPrivateKey, actorID)
	return err
}

func markAsEncrypted(ctx context.Context, db *sqlx.DB, actorID string) error {
	query := `
		UPDATE ap_actor_keys
		SET keys_encrypted = TRUE,
		    updated_at = CURRENT_TIMESTAMP
		WHERE actor_id = $1
	`
	_, err := db.ExecContext(ctx, query, actorID)
	return err
}

func verifyDecryption(db *sqlx.DB, encryption *security.ActivityPubKeyEncryption) error {
	// Fetch one encrypted key and verify we can decrypt it
	var key KeyRecord
	err := db.Get(&key, "SELECT actor_id, private_key_pem FROM ap_actor_keys WHERE keys_encrypted = TRUE LIMIT 1")
	if err != nil {
		if err == sql.ErrNoRows {
			// No encrypted keys to verify
			return nil
		}
		return fmt.Errorf("failed to fetch test key: %w", err)
	}

	// Try to decrypt
	_, err = encryption.DecryptPrivateKey(key.PrivateKeyPem)
	if err != nil {
		return fmt.Errorf("failed to decrypt test key for actor %s: %w", key.ActorID, err)
	}

	log.Printf("Verification successful: Can decrypt keys with current encryption key\n")
	return nil
}
