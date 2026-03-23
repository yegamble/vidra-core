package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/usecase"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupOAuthMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func newOAuthRepo(t *testing.T) (*oauthRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupOAuthMockDB(t)
	repo := NewOAuthRepository(db).(*oauthRepository)
	return repo, mock, func() { _ = db.Close() }
}

func sampleOAuthClient() *usecase.OAuthClient {
	secret := "hashed-secret"
	return &usecase.OAuthClient{
		ID:               "id-1",
		ClientID:         "client-1",
		ClientSecretHash: &secret,
		Name:             "Test Client",
		GrantTypes:       []string{"password", "refresh_token"},
		Scopes:           []string{"basic", "profile"},
		RedirectURIs:     []string{"https://app.example/callback"},
		IsConfidential:   true,
		AllowedScopes:    []string{"basic", "profile", "email"},
	}
}

func TestOAuthRepository_Unit_CreateClient(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	client := sampleOAuthClient()

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO oauth_clients`)).
		WithArgs(
			client.ID,
			client.ClientID,
			client.ClientSecretHash,
			client.Name,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			client.IsConfidential,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, repo.CreateClient(ctx, client))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_CreateClient_Error(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO oauth_clients`)).
		WillReturnError(errors.New("insert failed"))

	err := repo.CreateClient(ctx, sampleOAuthClient())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create oauth client")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetClientByClientID(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	client := sampleOAuthClient()

	rows := sqlmock.NewRows([]string{
		"id", "client_id", "client_secret_hash", "name", "grant_types", "scopes",
		"redirect_uris", "is_confidential", "allowed_scopes",
	}).AddRow(
		client.ID,
		client.ClientID,
		*client.ClientSecretHash,
		client.Name,
		"{password,refresh_token}",
		"{basic,profile}",
		"{https://app.example/callback}",
		client.IsConfidential,
		"{basic,profile,email}",
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, COALESCE(allowed_scopes, ARRAY['basic']::TEXT[]) as allowed_scopes FROM oauth_clients WHERE client_id = $1`)).
		WithArgs(client.ClientID).
		WillReturnRows(rows)

	got, err := repo.GetClientByClientID(ctx, client.ClientID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, client.ClientID, got.ClientID)
	assert.Equal(t, client.Name, got.Name)
	assert.ElementsMatch(t, client.GrantTypes, got.GrantTypes)
	assert.ElementsMatch(t, client.AllowedScopes, got.AllowedScopes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetClientByClientID_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, COALESCE(allowed_scopes, ARRAY['basic']::TEXT[]) as allowed_scopes FROM oauth_clients WHERE client_id = $1`)).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetClientByClientID(ctx, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oauth client not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_ListClients(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{
		"id", "client_id", "client_secret_hash", "name", "grant_types", "scopes",
		"redirect_uris", "is_confidential", "allowed_scopes",
	}).AddRow(
		"id-1", "client-1", "secret", "Client One",
		"{password}", "{basic}", "{https://app.example/cb}", true, "{basic,email}",
	).AddRow(
		"id-2", "client-2", nil, "Client Two",
		"{authorization_code}", "{basic,profile}", "{https://app2.example/cb}", false, "{basic,profile}",
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, COALESCE(allowed_scopes, ARRAY['basic']::TEXT[]) as allowed_scopes FROM oauth_clients ORDER BY created_at DESC`)).
		WillReturnRows(rows)

	clients, err := repo.ListClients(ctx)
	require.NoError(t, err)
	require.Len(t, clients, 2)
	assert.Equal(t, "client-1", clients[0].ClientID)
	assert.Equal(t, "client-2", clients[1].ClientID)
	assert.Nil(t, clients[1].ClientSecretHash)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_UpdateAndDeleteClient(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	secret := "new-secret"
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE oauth_clients
              SET client_secret_hash = $2, is_confidential = $3, updated_at = NOW()
              WHERE client_id = $1`)).
		WithArgs("client-1", &secret, true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.UpdateClientSecret(ctx, "client-1", &secret, true))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM oauth_clients WHERE client_id = $1`)).
		WithArgs("client-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.DeleteClient(ctx, "client-1"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_AuthorizationCodeOps(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	code := &usecase.OAuthAuthorizationCode{
		ID:                  "code-id",
		Code:                "code-value",
		ClientID:            "client-1",
		UserID:              "user-1",
		RedirectURI:         "https://app.example/callback",
		Scope:               "basic",
		State:               "state-1",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().Add(10 * time.Minute),
	}

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`)).
		WithArgs(
			code.ID, code.Code, code.ClientID, code.UserID, code.RedirectURI, code.Scope, code.State,
			code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, repo.CreateAuthorizationCode(ctx, code))

	getRows := sqlmock.NewRows([]string{
		"id", "code", "client_id", "user_id", "redirect_uri", "scope", "state",
		"code_challenge", "code_challenge_method", "expires_at", "used_at", "created_at",
	}).AddRow(
		code.ID, code.Code, code.ClientID, code.UserID, code.RedirectURI, code.Scope, code.State,
		code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt, nil, time.Now(),
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at, used_at, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`)).
		WithArgs(code.Code).
		WillReturnRows(getRows)

	gotCode, err := repo.GetAuthorizationCode(ctx, code.Code)
	require.NoError(t, err)
	require.NotNil(t, gotCode)
	assert.Equal(t, code.Code, gotCode.Code)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE oauth_authorization_codes SET used_at = NOW() WHERE code = $1`)).
		WithArgs(code.Code).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkCodeAsUsed(ctx, code.Code))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM oauth_authorization_codes WHERE expires_at < NOW() OR used_at IS NOT NULL`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.DeleteExpiredCodes(ctx))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_AccessTokenOps(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	token := &usecase.OAuthAccessToken{
		ID:        "tok-id",
		TokenHash: "tok-hash",
		ClientID:  "client-1",
		UserID:    "user-1",
		Scope:     "basic profile",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO oauth_access_tokens (id, token_hash, client_id, user_id, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`)).
		WithArgs(token.ID, token.TokenHash, token.ClientID, token.UserID, token.Scope, token.ExpiresAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.CreateAccessToken(ctx, token))

	getRows := sqlmock.NewRows([]string{
		"id", "token_hash", "client_id", "user_id", "scope", "expires_at", "created_at", "revoked_at",
	}).AddRow(
		token.ID, token.TokenHash, token.ClientID, token.UserID, token.Scope, token.ExpiresAt, time.Now(), nil,
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE token_hash = $1`)).
		WithArgs(token.TokenHash).
		WillReturnRows(getRows)

	gotToken, err := repo.GetAccessToken(ctx, token.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, gotToken)
	assert.Equal(t, token.TokenHash, gotToken.TokenHash)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE oauth_access_tokens SET revoked_at = NOW() WHERE token_hash = $1`)).
		WithArgs(token.TokenHash).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RevokeAccessToken(ctx, token.TokenHash))

	userRows := sqlmock.NewRows([]string{
		"id", "token_hash", "client_id", "user_id", "scope", "expires_at", "created_at", "revoked_at",
	}).AddRow(
		"tok-id-2", "tok-hash-2", "client-1", token.UserID, "basic", time.Now().Add(time.Hour), time.Now(), nil,
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC`)).
		WithArgs(token.UserID).
		WillReturnRows(userRows)

	userTokens, err := repo.ListUserTokens(ctx, token.UserID)
	require.NoError(t, err)
	require.Len(t, userTokens, 1)
	assert.Equal(t, "tok-hash-2", userTokens[0].TokenHash)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM oauth_access_tokens WHERE expires_at < NOW()`)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.DeleteExpiredTokens(ctx))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetAuthorizationCode_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at, used_at, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`)).
		WithArgs("nonexistent_code").
		WillReturnError(sql.ErrNoRows)

	gotCode, err := repo.GetAuthorizationCode(ctx, "nonexistent_code")
	require.Error(t, err)
	assert.Nil(t, gotCode)
	assert.Contains(t, err.Error(), "authorization code not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetAuthorizationCode_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at, used_at, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`)).
		WithArgs("some_code").
		WillReturnError(sql.ErrConnDone)

	gotCode, err := repo.GetAuthorizationCode(ctx, "some_code")
	require.Error(t, err)
	assert.Nil(t, gotCode)
	assert.Contains(t, err.Error(), "get authorization code")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetAccessToken_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE token_hash = $1`)).
		WithArgs("nonexistent_token").
		WillReturnError(sql.ErrNoRows)

	gotToken, err := repo.GetAccessToken(ctx, "nonexistent_token")
	require.Error(t, err)
	assert.Nil(t, gotToken)
	assert.Contains(t, err.Error(), "access token not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthRepository_Unit_GetAccessToken_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newOAuthRepo(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE token_hash = $1`)).
		WithArgs("some_token").
		WillReturnError(sql.ErrConnDone)

	gotToken, err := repo.GetAccessToken(ctx, "some_token")
	require.Error(t, err)
	assert.Nil(t, gotToken)
	assert.Contains(t, err.Error(), "get access token")
	require.NoError(t, mock.ExpectationsWereMet())
}
