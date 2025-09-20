package usecase

import (
	"context"
	"time"
)

// OAuthClient represents a minimal OAuth2 client
type OAuthClient struct {
	ID               string   `db:"id"`
	ClientID         string   `db:"client_id"`
	ClientSecretHash *string  `db:"client_secret_hash"`
	Name             string   `db:"name"`
	GrantTypes       []string `db:"grant_types"`
	Scopes           []string `db:"scopes"`
	RedirectURIs     []string `db:"redirect_uris"`
	IsConfidential   bool     `db:"is_confidential"`
	AllowedScopes    []string `db:"allowed_scopes"`
}

// OAuthAuthorizationCode represents an authorization code
type OAuthAuthorizationCode struct {
	ID                  string     `db:"id"`
	Code                string     `db:"code"`
	ClientID            string     `db:"client_id"`
	UserID              string     `db:"user_id"`
	RedirectURI         string     `db:"redirect_uri"`
	Scope               string     `db:"scope"`
	State               string     `db:"state"`
	CodeChallenge       string     `db:"code_challenge"`
	CodeChallengeMethod string     `db:"code_challenge_method"`
	ExpiresAt           time.Time  `db:"expires_at"`
	UsedAt              *time.Time `db:"used_at"`
	CreatedAt           time.Time  `db:"created_at"`
}

// OAuthAccessToken represents an access token for revocation/introspection
type OAuthAccessToken struct {
	ID        string     `db:"id"`
	TokenHash string     `db:"token_hash"`
	ClientID  string     `db:"client_id"`
	UserID    string     `db:"user_id"`
	Scope     string     `db:"scope"`
	ExpiresAt time.Time  `db:"expires_at"`
	CreatedAt time.Time  `db:"created_at"`
	RevokedAt *time.Time `db:"revoked_at"`
}

// OAuthRepository provides access to OAuth client storage
type OAuthRepository interface {
	// Client operations
	CreateClient(ctx context.Context, c *OAuthClient) error
	GetClientByClientID(ctx context.Context, clientID string) (*OAuthClient, error)
	ListClients(ctx context.Context) ([]*OAuthClient, error)
	UpdateClientSecret(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error
	DeleteClient(ctx context.Context, clientID string) error

	// Authorization code operations
	CreateAuthorizationCode(ctx context.Context, code *OAuthAuthorizationCode) error
	GetAuthorizationCode(ctx context.Context, code string) (*OAuthAuthorizationCode, error)
	MarkCodeAsUsed(ctx context.Context, code string) error
	DeleteExpiredCodes(ctx context.Context) error

	// Access token operations
	CreateAccessToken(ctx context.Context, token *OAuthAccessToken) error
	GetAccessToken(ctx context.Context, tokenHash string) (*OAuthAccessToken, error)
	RevokeAccessToken(ctx context.Context, tokenHash string) error
	ListUserTokens(ctx context.Context, userID string) ([]*OAuthAccessToken, error)
	DeleteExpiredTokens(ctx context.Context) error
}
