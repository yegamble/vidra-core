package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"testing"

	"athena/internal/crypto"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtprotoRepository_Unit_DecodeTokenKey(t *testing.T) {
	raw := []byte("0123456789abcdef0123456789abcdef")

	b64 := base64.StdEncoding.EncodeToString(raw)
	gotB64, err := DecodeTokenKey(b64)
	require.NoError(t, err)
	assert.Equal(t, raw, gotB64)

	b64URL := base64.URLEncoding.EncodeToString(raw)
	gotURL, err := DecodeTokenKey(b64URL)
	require.NoError(t, err)
	assert.Equal(t, raw, gotURL)

	hexRaw := raw[:31]
	hexKey := hex.EncodeToString(hexRaw)
	gotHex, err := DecodeTokenKey(hexKey)
	require.NoError(t, err)
	assert.Equal(t, hexRaw, gotHex)
}

func TestAtprotoRepository_Unit_DecodeTokenKey_ErrorCases(t *testing.T) {
	_, err := DecodeTokenKey("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty key")

	_, err = DecodeTokenKey("not-valid$$$")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid key format")
}

func setupAtprotoMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func TestAtprotoRepository_Unit_NewAtprotoRepository(t *testing.T) {
	db, _ := setupAtprotoMockDB(t)
	defer func() { _ = db.Close() }()

	repo := NewAtprotoRepository(db)
	assert.NotNil(t, repo)
	assert.Equal(t, db, repo.db)
}

func TestAtprotoRepository_Unit_SaveSession(t *testing.T) {
	ctx := context.Background()

	key := make([]byte, crypto.ChaCha20KeySize)
	for i := range key {
		key[i] = byte(i)
	}

	t.Run("success", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		access := "access-token"
		refresh := "refresh-token"
		did := "did:plc:abc123"

		// Note: VALUES (1, $1, $2, $3, $4, $5, CURRENT_TIMESTAMP) - id and fetched_at are not bind params
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO atproto_sessions (id, access_jwt_enc, access_nonce, refresh_jwt_enc, refresh_nonce, did, fetched_at)`)).
			WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), did).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.SaveSession(ctx, key, access, refresh, did)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("encryption error - invalid key length", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		invalidKey := make([]byte, 16)

		err := repo.SaveSession(ctx, invalidKey, "access", "refresh", "did")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid key size")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO atproto_sessions`)).
			WillReturnError(sql.ErrConnDone)

		err := repo.SaveSession(ctx, key, "access", "refresh", "did")
		require.Error(t, err)
	})
}

func TestAtprotoRepository_Unit_LoadSessionDecrypted(t *testing.T) {
	ctx := context.Background()

	key := make([]byte, crypto.ChaCha20KeySize)
	for i := range key {
		key[i] = byte(i)
	}

	t.Run("success", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		cs := crypto.NewCryptoService()
		accessEnc, err := cs.EncryptWithMasterKey([]byte("access-token"), key)
		require.NoError(t, err)
		refreshEnc, err := cs.EncryptWithMasterKey([]byte("refresh-token"), key)
		require.NoError(t, err)

		rows := sqlmock.NewRows([]string{"access_jwt_enc", "access_nonce", "refresh_jwt_enc", "refresh_nonce", "did"}).
			AddRow(accessEnc.Ciphertext, accessEnc.Nonce, refreshEnc.Ciphertext, refreshEnc.Nonce, "did:plc:test")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc, access_nonce, refresh_jwt_enc, refresh_nonce, did FROM atproto_sessions WHERE id=1`)).
			WillReturnRows(rows)

		session, err := repo.LoadSessionDecrypted(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, session)
		assert.Equal(t, "access-token", session.Access)
		assert.Equal(t, "refresh-token", session.Refresh)
		assert.Equal(t, "did:plc:test", session.DID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no rows (no session stored)", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnError(sql.ErrNoRows)

		session, err := repo.LoadSessionDecrypted(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, session)
	})

	t.Run("query error", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnError(sql.ErrConnDone)

		session, err := repo.LoadSessionDecrypted(ctx, key)
		require.Error(t, err)
		assert.Nil(t, session)
	})

	t.Run("invalid key length", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		cs := crypto.NewCryptoService()
		accessEnc, _ := cs.EncryptWithMasterKey([]byte("access"), key)
		refreshEnc, _ := cs.EncryptWithMasterKey([]byte("refresh"), key)

		rows := sqlmock.NewRows([]string{"access_jwt_enc", "access_nonce", "refresh_jwt_enc", "refresh_nonce", "did"}).
			AddRow(accessEnc.Ciphertext, accessEnc.Nonce, refreshEnc.Ciphertext, refreshEnc.Nonce, "did")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnRows(rows)

		wrongKey := make([]byte, 16)

		session, err := repo.LoadSessionDecrypted(ctx, wrongKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid key length")
		assert.Nil(t, session)
	})
}

func TestAtprotoRepository_Unit_LoadSessionStrings(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, crypto.ChaCha20KeySize)

	t.Run("success", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		cs := crypto.NewCryptoService()
		accessEnc, _ := cs.EncryptWithMasterKey([]byte("access"), key)
		refreshEnc, _ := cs.EncryptWithMasterKey([]byte("refresh"), key)

		rows := sqlmock.NewRows([]string{"access_jwt_enc", "access_nonce", "refresh_jwt_enc", "refresh_nonce", "did"}).
			AddRow(accessEnc.Ciphertext, accessEnc.Nonce, refreshEnc.Ciphertext, refreshEnc.Nonce, "did:test")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnRows(rows)

		access, refresh, did, err := repo.LoadSessionStrings(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "access", access)
		assert.Equal(t, "refresh", refresh)
		assert.Equal(t, "did:test", did)
	})

	t.Run("no session returns empty", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnError(sql.ErrNoRows)

		access, refresh, did, err := repo.LoadSessionStrings(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "", access)
		assert.Equal(t, "", refresh)
		assert.Equal(t, "", did)
	})

	t.Run("error returns error", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnError(sql.ErrConnDone)

		access, refresh, did, err := repo.LoadSessionStrings(ctx, key)
		require.Error(t, err)
		assert.Equal(t, "", access)
		assert.Equal(t, "", refresh)
		assert.Equal(t, "", did)
	})
}

func TestAtprotoRepository_Unit_LoadSession(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, crypto.ChaCha20KeySize)

	t.Run("success delegates to LoadSessionStrings", func(t *testing.T) {
		db, mock := setupAtprotoMockDB(t)
		defer func() { _ = db.Close() }()

		repo := NewAtprotoRepository(db)

		cs := crypto.NewCryptoService()
		accessEnc, _ := cs.EncryptWithMasterKey([]byte("test-access"), key)
		refreshEnc, _ := cs.EncryptWithMasterKey([]byte("test-refresh"), key)

		rows := sqlmock.NewRows([]string{"access_jwt_enc", "access_nonce", "refresh_jwt_enc", "refresh_nonce", "did"}).
			AddRow(accessEnc.Ciphertext, accessEnc.Nonce, refreshEnc.Ciphertext, refreshEnc.Nonce, "did:final")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT access_jwt_enc`)).
			WillReturnRows(rows)

		access, refresh, did, err := repo.LoadSession(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "test-access", access)
		assert.Equal(t, "test-refresh", refresh)
		assert.Equal(t, "did:final", did)
	})
}
