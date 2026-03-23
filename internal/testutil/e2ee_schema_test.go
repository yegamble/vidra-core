package testutil

import (
	"testing"
)

func TestE2EESchemaColumns(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2EE schema test in short mode (requires database)")
	}

	testDB := SetupTestDB(t)

	e2eeMessageCols := []string{
		"encrypted_content",
		"content_nonce",
		"pgp_signature",
		"is_encrypted",
		"encryption_version",
	}
	for _, col := range e2eeMessageCols {
		var exists bool
		err := testDB.DB.QueryRow(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'messages' AND column_name = $1
			)`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("Failed to check column messages.%s: %v", col, err)
		}
		if !exists {
			t.Errorf("Missing E2EE column messages.%s", col)
		}
	}

	e2eeConvCols := []string{
		"is_encrypted",
		"key_exchange_complete",
		"encryption_version",
		"last_key_rotation",
		"encryption_status",
	}
	for _, col := range e2eeConvCols {
		var exists bool
		err := testDB.DB.QueryRow(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'conversations' AND column_name = $1
			)`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("Failed to check column conversations.%s: %v", col, err)
		}
		if !exists {
			t.Errorf("Missing E2EE column conversations.%s", col)
		}
	}

	e2eeTables := []string{
		"user_master_keys",
		"conversation_keys",
		"key_exchange_messages",
		"user_signing_keys",
		"crypto_audit_log",
	}
	for _, tbl := range e2eeTables {
		var exists bool
		err := testDB.DB.QueryRow(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_name = $1
			)`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("Failed to check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("Missing E2EE table %s", tbl)
		}
	}
}
