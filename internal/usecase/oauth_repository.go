package usecase

import "context"

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
}

// OAuthRepository provides access to OAuth client storage
type OAuthRepository interface {
	CreateClient(ctx context.Context, c *OAuthClient) error
	GetClientByClientID(ctx context.Context, clientID string) (*OAuthClient, error)
	ListClients(ctx context.Context) ([]*OAuthClient, error)
	UpdateClientSecret(ctx context.Context, clientID string, secretHash *string, isConfidential bool) error
	DeleteClient(ctx context.Context, clientID string) error
}
