package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"vidra-core/internal/crypto"
	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

// UserAtprotoAccount is the per-user ATProto identity surface (plaintext after decryption).
// Tokens never leave the repo as plaintext at rest — they live encrypted in the DB and are
// only decrypted on Get(). Callers should treat AccessJWT/RefreshJWT as security-sensitive
// (no logging, no telemetry breadcrumbs).
type UserAtprotoAccount struct {
	UserID          string
	DID             string
	Handle          string
	PDSURL          string
	AccessJWT       string
	RefreshJWT      string
	LastRefreshedAt time.Time
	CreatedAt       time.Time
}

// UserAtprotoRepository persists per-user ATProto accounts with at-rest encryption of session
// tokens. The master key passed to Save/Get is the same key the singleton AtprotoRepository
// uses (see internal/repository/atproto_repository.go) — instance-resolved at app startup.
type UserAtprotoRepository struct {
	db *sqlx.DB
}

func NewUserAtprotoRepository(db *sqlx.DB) *UserAtprotoRepository {
	return &UserAtprotoRepository{db: db}
}

// Save inserts or updates a user's ATProto account. Tokens are encrypted with `key` before
// persisting. Conflicting did across users → ErrDIDAlreadyLinked (callers translate to 409).
func (r *UserAtprotoRepository) Save(ctx context.Context, key []byte, acct *UserAtprotoAccount) error {
	if acct == nil {
		return fmt.Errorf("UserAtprotoRepository.Save: nil account")
	}
	cs := crypto.NewCryptoService()
	accEnc, err := cs.EncryptWithMasterKey([]byte(acct.AccessJWT), key)
	if err != nil {
		return fmt.Errorf("encrypt access jwt: %w", err)
	}
	refEnc, err := cs.EncryptWithMasterKey([]byte(acct.RefreshJWT), key)
	if err != nil {
		return fmt.Errorf("encrypt refresh jwt: %w", err)
	}

	q := `INSERT INTO user_atproto_accounts
		(user_id, did, handle, pds_url, access_jwt_enc, access_nonce, refresh_jwt_enc, refresh_nonce, last_refreshed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			did = EXCLUDED.did,
			handle = EXCLUDED.handle,
			pds_url = EXCLUDED.pds_url,
			access_jwt_enc = EXCLUDED.access_jwt_enc,
			access_nonce = EXCLUDED.access_nonce,
			refresh_jwt_enc = EXCLUDED.refresh_jwt_enc,
			refresh_nonce = EXCLUDED.refresh_nonce,
			last_refreshed_at = NOW()`

	_, err = r.db.ExecContext(ctx, q,
		acct.UserID, acct.DID, acct.Handle, acct.PDSURL,
		accEnc.Ciphertext, accEnc.Nonce,
		refEnc.Ciphertext, refEnc.Nonce,
	)
	if err != nil {
		// Postgres unique-violation on did is the only realistic path — wrap as a domain
		// signal so handlers can return 409 cleanly.
		if isUniqueViolation(err, "user_atproto_accounts_did_key") {
			return ErrDIDAlreadyLinked
		}
		return fmt.Errorf("inserting user atproto account: %w", err)
	}
	return nil
}

// Get returns the decrypted account for userID, or domain.ErrNotFound when missing.
func (r *UserAtprotoRepository) Get(ctx context.Context, key []byte, userID string) (*UserAtprotoAccount, error) {
	var row struct {
		UserID          string    `db:"user_id"`
		DID             string    `db:"did"`
		Handle          string    `db:"handle"`
		PDSURL          string    `db:"pds_url"`
		AccessJWTEnc    []byte    `db:"access_jwt_enc"`
		AccessNonce     []byte    `db:"access_nonce"`
		RefreshJWTEnc   []byte    `db:"refresh_jwt_enc"`
		RefreshNonce    []byte    `db:"refresh_nonce"`
		LastRefreshedAt time.Time `db:"last_refreshed_at"`
		CreatedAt       time.Time `db:"created_at"`
	}
	q := `SELECT user_id, did, handle, pds_url, access_jwt_enc, access_nonce,
		refresh_jwt_enc, refresh_nonce, last_refreshed_at, created_at
		FROM user_atproto_accounts WHERE user_id = $1`
	if err := r.db.GetContext(ctx, &row, q, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("getting user atproto account: %w", err)
	}

	cs := crypto.NewCryptoService()
	access, err := cs.DecryptWithMasterKey(&crypto.EncryptedData{
		Ciphertext: row.AccessJWTEnc, Nonce: row.AccessNonce, Version: 1,
	}, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt access jwt: %w", err)
	}
	refresh, err := cs.DecryptWithMasterKey(&crypto.EncryptedData{
		Ciphertext: row.RefreshJWTEnc, Nonce: row.RefreshNonce, Version: 1,
	}, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh jwt: %w", err)
	}

	return &UserAtprotoAccount{
		UserID:          row.UserID,
		DID:             row.DID,
		Handle:          row.Handle,
		PDSURL:          row.PDSURL,
		AccessJWT:       string(access),
		RefreshJWT:      string(refresh),
		LastRefreshedAt: row.LastRefreshedAt,
		CreatedAt:       row.CreatedAt,
	}, nil
}

// Delete removes the row. Idempotent — no error when already gone.
func (r *UserAtprotoRepository) Delete(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_atproto_accounts WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user atproto account: %w", err)
	}
	return nil
}

// UpdateTokens replaces just the encrypted access/refresh tokens (after a refresh round-trip).
// Leaves did / handle / pds_url unchanged. Used by the per-user refresh path.
func (r *UserAtprotoRepository) UpdateTokens(ctx context.Context, key []byte, userID, access, refresh string) error {
	cs := crypto.NewCryptoService()
	accEnc, err := cs.EncryptWithMasterKey([]byte(access), key)
	if err != nil {
		return fmt.Errorf("encrypt access jwt: %w", err)
	}
	refEnc, err := cs.EncryptWithMasterKey([]byte(refresh), key)
	if err != nil {
		return fmt.Errorf("encrypt refresh jwt: %w", err)
	}
	res, err := r.db.ExecContext(ctx, `UPDATE user_atproto_accounts
		SET access_jwt_enc = $2, access_nonce = $3,
		    refresh_jwt_enc = $4, refresh_nonce = $5,
		    last_refreshed_at = NOW()
		WHERE user_id = $1`,
		userID, accEnc.Ciphertext, accEnc.Nonce, refEnc.Ciphertext, refEnc.Nonce)
	if err != nil {
		return fmt.Errorf("update tokens: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ErrDIDAlreadyLinked signals a unique-violation on did when a different user has already
// linked the same Bluesky identity.
var ErrDIDAlreadyLinked = errors.New("atproto: DID already linked to another user")

// isUniqueViolation does a naive substring check on the pg error string. We prefer this over
// pulling in pgx errcode parsing for one call site; matches what the rest of the codebase
// does in similar repos.
func isUniqueViolation(err error, indexName string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "duplicate key") && contains(msg, indexName)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
