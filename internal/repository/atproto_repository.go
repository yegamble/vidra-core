package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"

	"athena/internal/crypto"

	"github.com/jmoiron/sqlx"
)

type AtprotoSession struct {
	Access  string
	Refresh string
	DID     string
}

// AtprotoRepository handles persistence of ATProto session tokens (encrypted at rest).
type AtprotoRepository struct{ db *sqlx.DB }

func NewAtprotoRepository(db *sqlx.DB) *AtprotoRepository { return &AtprotoRepository{db: db} }

// SaveSession stores the tokens encrypted with the provided 32-byte key.
func (r *AtprotoRepository) SaveSession(ctx context.Context, key []byte, access, refresh, did string) error {
	cs := crypto.NewCryptoService()
	// Encrypt access
	accEnc, err := cs.EncryptWithMasterKey([]byte(access), key)
	if err != nil {
		return err
	}
	refEnc, err := cs.EncryptWithMasterKey([]byte(refresh), key)
	if err != nil {
		return err
	}
	// Upsert singleton row id=1
	q := `INSERT INTO atproto_sessions (id, access_jwt_enc, access_nonce, refresh_jwt_enc, refresh_nonce, did, fetched_at)
          VALUES (1, $1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
          ON CONFLICT (id) DO UPDATE SET
            access_jwt_enc=EXCLUDED.access_jwt_enc,
            access_nonce=EXCLUDED.access_nonce,
            refresh_jwt_enc=EXCLUDED.refresh_jwt_enc,
            refresh_nonce=EXCLUDED.refresh_nonce,
            did=EXCLUDED.did,
            fetched_at=EXCLUDED.fetched_at`
	_, err = r.db.ExecContext(ctx, q, accEnc.Ciphertext, accEnc.Nonce, refEnc.Ciphertext, refEnc.Nonce, did)
	return err
}

// LoadSession loads and decrypts the stored tokens using the provided key.
func (r *AtprotoRepository) LoadSessionDecrypted(ctx context.Context, key []byte) (*AtprotoSession, error) {
	var row struct {
		Access       []byte         `db:"access_jwt_enc"`
		AccessNonce  []byte         `db:"access_nonce"`
		Refresh      []byte         `db:"refresh_jwt_enc"`
		RefreshNonce []byte         `db:"refresh_nonce"`
		DID          sql.NullString `db:"did"`
	}
	err := r.db.GetContext(ctx, &row, `SELECT access_jwt_enc, access_nonce, refresh_jwt_enc, refresh_nonce, did FROM atproto_sessions WHERE id=1`)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cs := crypto.NewCryptoService()
	if len(key) != crypto.ChaCha20KeySize {
		return nil, fmt.Errorf("invalid key length; expected %d bytes (got %d)", crypto.ChaCha20KeySize, len(key))
	}
	acc, err := cs.Decrypt(row.Access, key, row.AccessNonce)
	if err != nil {
		return nil, err
	}
	ref, err := cs.Decrypt(row.Refresh, key, row.RefreshNonce)
	if err != nil {
		return nil, err
	}
	did := ""
	if row.DID.Valid {
		did = row.DID.String
	}
	return &AtprotoSession{Access: string(acc), Refresh: string(ref), DID: did}, nil
}

// LoadSessionStrings returns tokens as strings for usecase interface compatibility
func (r *AtprotoRepository) LoadSessionStrings(ctx context.Context, key []byte) (string, string, string, error) {
	s, err := r.LoadSessionDecrypted(ctx, key)
	if err != nil || s == nil {
		if err == nil {
			return "", "", "", nil
		}
		return "", "", "", err
	}
	return s.Access, s.Refresh, s.DID, nil
}

// LoadSession returns tokens as strings (usecase-compatible signature)
func (r *AtprotoRepository) LoadSession(ctx context.Context, key []byte) (string, string, string, error) {
	return r.LoadSessionStrings(ctx, key)
}

// DecodeTokenKey decodes a base64 or hex-encoded key and returns raw bytes.
func DecodeTokenKey(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty key")
	}
	// Try base64
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	// Try URL-safe base64
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	// Try hex
	const hexdigits = "0123456789abcdefABCDEF"
	for _, c := range s {
		if !strings.ContainsRune(hexdigits, c) {
			return nil, fmt.Errorf("invalid key format")
		}
	}
	b := make([]byte, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		var x byte
		_, err := fmt.Sscanf(s[i:i+2], "%02x", &x)
		if err != nil {
			return nil, err
		}
		b[i/2] = x
	}
	return b, nil
}
