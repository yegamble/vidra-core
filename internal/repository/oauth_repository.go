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
        INSERT INTO oauth_clients (id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := r.db.ExecContext(ctx, query,
		c.ID, c.ClientID, c.ClientSecretHash, c.Name, pq.Array(c.GrantTypes), pq.Array(c.Scopes), pq.Array(c.RedirectURIs), c.IsConfidential,
	)
	if err != nil {
		return fmt.Errorf("create oauth client: %w", err)
	}
	return nil
}

func (r *oauthRepository) GetClientByClientID(ctx context.Context, clientID string) (*usecase.OAuthClient, error) {
	query := `
        SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential
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
	return &c, nil
}

func (r *oauthRepository) ListClients(ctx context.Context) ([]*usecase.OAuthClient, error) {
	query := `
        SELECT id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential
        FROM oauth_clients ORDER BY created_at DESC`
	rows, err := r.db.QueryxContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list oauth clients: %w", err)
	}
	defer rows.Close()
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
