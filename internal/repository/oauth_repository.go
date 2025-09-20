package repository

import (
	"context"
	"database/sql"
	"fmt"

	"athena/internal/usecase"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type oauthRepository struct {
	db *sqlx.DB
}

func NewOAuthRepository(db *sqlx.DB) usecase.OAuthRepository {
	return &oauthRepository{db: db}
}

func (r *oauthRepository) CreateClient(ctx context.Context, c *usecase.OAuthClient) error {
	query := `
        INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, allowed_scopes)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.db.ExecContext(ctx, query,
		c.ID, c.ClientID, c.ClientSecretHash, c.Name, pq.Array(c.GrantTypes), pq.Array(c.Scopes), pq.Array(c.RedirectURIs), c.IsConfidential, pq.Array(c.AllowedScopes),
	)
	if err != nil {
		return fmt.Errorf("create oauth client: %w", err)
	}
	return nil
}

func (r *oauthRepository) GetClientByClientID(ctx context.Context, clientID string) (*usecase.OAuthClient, error) {
	query := `
        SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, COALESCE(allowed_scopes, ARRAY['basic']::TEXT[]) as allowed_scopes
        FROM oauth_clients WHERE client_id = $1`

	var c usecase.OAuthClient
	// custom struct because sqlx cannot scan directly into []string from TEXT[] without helpers
	type row struct {
		ID               string         `db:"id"`
		ClientID         string         `db:"client_id"`
		ClientSecretHash sql.NullString `db:"client_secret_hash"`
		Name             string         `db:"name"`
		GrantTypes       pq.StringArray `db:"grant_types"`
		Scopes           pq.StringArray `db:"scopes"`
		RedirectURIs     pq.StringArray `db:"redirect_uris"`
		IsConfidential   bool           `db:"is_confidential"`
		AllowedScopes    pq.StringArray `db:"allowed_scopes"`
	}
	var rw row
	if err := r.db.GetContext(ctx, &rw, query, clientID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("oauth client not found")
		}
		return nil, fmt.Errorf("get oauth client: %w", err)
	}
	c.ID = rw.ID
	c.ClientID = rw.ClientID
	if rw.ClientSecretHash.Valid {
		s := rw.ClientSecretHash.String
		c.ClientSecretHash = &s
	}
	c.Name = rw.Name
	c.GrantTypes = []string(rw.GrantTypes)
	c.Scopes = []string(rw.Scopes)
	c.RedirectURIs = []string(rw.RedirectURIs)
	c.IsConfidential = rw.IsConfidential
	c.AllowedScopes = []string(rw.AllowedScopes)
	return &c, nil
}

func (r *oauthRepository) ListClients(ctx context.Context) ([]*usecase.OAuthClient, error) {
	query := `
        SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential, COALESCE(allowed_scopes, ARRAY['basic']::TEXT[]) as allowed_scopes
        FROM oauth_clients ORDER BY created_at DESC`
	rows, err := r.db.QueryxContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list oauth clients: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var res []*usecase.OAuthClient
	for rows.Next() {
		var rw struct {
			ID               string         `db:"id"`
			ClientID         string         `db:"client_id"`
			ClientSecretHash sql.NullString `db:"client_secret_hash"`
			Name             string         `db:"name"`
			GrantTypes       pq.StringArray `db:"grant_types"`
			Scopes           pq.StringArray `db:"scopes"`
			RedirectURIs     pq.StringArray `db:"redirect_uris"`
			IsConfidential   bool           `db:"is_confidential"`
			AllowedScopes    pq.StringArray `db:"allowed_scopes"`
		}
		if err := rows.StructScan(&rw); err != nil {
			return nil, fmt.Errorf("scan oauth client: %w", err)
		}
		c := &usecase.OAuthClient{
			ID:             rw.ID,
			ClientID:       rw.ClientID,
			Name:           rw.Name,
			GrantTypes:     []string(rw.GrantTypes),
			Scopes:         []string(rw.Scopes),
			RedirectURIs:   []string(rw.RedirectURIs),
			IsConfidential: rw.IsConfidential,
			AllowedScopes:  []string(rw.AllowedScopes),
		}
		if rw.ClientSecretHash.Valid {
			s := rw.ClientSecretHash.String
			c.ClientSecretHash = &s
		}
		res = append(res, c)
	}
	return res, nil
}

func (r *oauthRepository) UpdateClientSecret(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error {
	query := `UPDATE oauth_clients
              SET client_secret_hash = $2, is_confidential = $3, updated_at = NOW()
              WHERE client_id = $1`
	_, err := r.db.ExecContext(ctx, query, clientID, secretHash, isConfidential)
	if err != nil {
		return fmt.Errorf("update oauth client secret: %w", err)
	}
	return nil
}

func (r *oauthRepository) DeleteClient(ctx context.Context, clientID string) error {
	query := `DELETE FROM oauth_clients WHERE client_id = $1`
	_, err := r.db.ExecContext(ctx, query, clientID)
	if err != nil {
		return fmt.Errorf("delete oauth client: %w", err)
	}
	return nil
}

// Authorization code operations
func (r *oauthRepository) CreateAuthorizationCode(ctx context.Context, code *usecase.OAuthAuthorizationCode) error {
	query := `
		INSERT INTO oauth_authorization_codes (id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := r.db.ExecContext(ctx, query,
		code.ID, code.Code, code.ClientID, code.UserID, code.RedirectURI, code.Scope, code.State,
		code.CodeChallenge, code.CodeChallengeMethod, code.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create authorization code: %w", err)
	}
	return nil
}

func (r *oauthRepository) GetAuthorizationCode(ctx context.Context, code string) (*usecase.OAuthAuthorizationCode, error) {
	query := `
		SELECT id, code, client_id, user_id, redirect_uri, scope, state, code_challenge, code_challenge_method, expires_at, used_at, created_at
		FROM oauth_authorization_codes
		WHERE code = $1`

	var ac usecase.OAuthAuthorizationCode
	err := r.db.GetContext(ctx, &ac, query, code)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("authorization code not found")
		}
		return nil, fmt.Errorf("get authorization code: %w", err)
	}
	return &ac, nil
}

func (r *oauthRepository) MarkCodeAsUsed(ctx context.Context, code string) error {
	query := `UPDATE oauth_authorization_codes SET used_at = NOW() WHERE code = $1`
	_, err := r.db.ExecContext(ctx, query, code)
	if err != nil {
		return fmt.Errorf("mark code as used: %w", err)
	}
	return nil
}

func (r *oauthRepository) DeleteExpiredCodes(ctx context.Context) error {
	query := `DELETE FROM oauth_authorization_codes WHERE expires_at < NOW() OR used_at IS NOT NULL`
	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete expired codes: %w", err)
	}
	return nil
}

// Access token operations
func (r *oauthRepository) CreateAccessToken(ctx context.Context, token *usecase.OAuthAccessToken) error {
	query := `
		INSERT INTO oauth_access_tokens (id, token_hash, client_id, user_id, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := r.db.ExecContext(ctx, query,
		token.ID, token.TokenHash, token.ClientID, token.UserID, token.Scope, token.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create access token: %w", err)
	}
	return nil
}

func (r *oauthRepository) GetAccessToken(ctx context.Context, tokenHash string) (*usecase.OAuthAccessToken, error) {
	query := `
		SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE token_hash = $1`

	var token usecase.OAuthAccessToken
	err := r.db.GetContext(ctx, &token, query, tokenHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("access token not found")
		}
		return nil, fmt.Errorf("get access token: %w", err)
	}
	return &token, nil
}

func (r *oauthRepository) RevokeAccessToken(ctx context.Context, tokenHash string) error {
	query := `UPDATE oauth_access_tokens SET revoked_at = NOW() WHERE token_hash = $1`
	_, err := r.db.ExecContext(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("revoke access token: %w", err)
	}
	return nil
}

func (r *oauthRepository) ListUserTokens(ctx context.Context, userID string) ([]*usecase.OAuthAccessToken, error) {
	query := `
		SELECT id, token_hash, client_id, user_id, scope, expires_at, created_at, revoked_at
		FROM oauth_access_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC`

	var tokens []*usecase.OAuthAccessToken
	err := r.db.SelectContext(ctx, &tokens, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list user tokens: %w", err)
	}
	return tokens, nil
}

func (r *oauthRepository) DeleteExpiredTokens(ctx context.Context) error {
	query := `DELETE FROM oauth_access_tokens WHERE expires_at < NOW()`
	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete expired tokens: %w", err)
	}
	return nil
}
