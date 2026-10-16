//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestOIDCProviderQueriesPersist exercises migration 0182 against real
// PostgreSQL: create/get/list/update/delete, the name CHECK, the immutable
// issuer (UpdateOIDCProvider has no issuer column), and the linked-identity
// probe the rebinding guard relies on.
func TestOIDCProviderQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()
	name := "it-" + userID.String()[:8]
	defer func() { _, _ = q.DeleteOIDCProvider(context.Background(), name) }()

	if _, err := q.CreateOIDCProvider(ctx, sqlcgen.CreateOIDCProviderParams{
		Name: "Not Valid", IssuerUrl: "https://x.example", ClientID: "c", ClientSecret: "enc:x",
		Scopes: []string{}, AllowedEmailDomains: []string{},
	}); err == nil {
		t.Fatal("name CHECK did not reject an invalid name")
	}

	row, err := q.CreateOIDCProvider(ctx, sqlcgen.CreateOIDCProviderParams{
		Name: name, DisplayName: "Corp", IssuerUrl: "https://sso.example", ClientID: "c",
		ClientSecret: "enc:sealed", Scopes: []string{"email"}, Enabled: true, AutoProvision: false,
		AllowedEmailDomains: []string{"corp.example"}, UpdatedBy: pgconv.UUID(userID),
	})
	if err != nil {
		t.Fatalf("CreateOIDCProvider: %v", err)
	}
	if row.AutoProvision || row.AllowedEmailDomains[0] != "corp.example" || row.Scopes[0] != "email" {
		t.Errorf("created = %+v", row)
	}

	upd, err := q.UpdateOIDCProvider(ctx, sqlcgen.UpdateOIDCProviderParams{
		Name: name, DisplayName: "Corp SSO", ClientID: "c2", ClientSecret: "enc:rotated",
		Scopes: []string{}, Enabled: false, AutoProvision: true, AllowedEmailDomains: []string{},
	})
	if err != nil {
		t.Fatalf("UpdateOIDCProvider: %v", err)
	}
	if upd.IssuerUrl != "https://sso.example" || upd.ClientID != "c2" || upd.Enabled || upd.UpdatedBy.Valid {
		t.Errorf("updated = %+v", upd)
	}

	rows, err := q.ListOIDCProviders(ctx)
	if err != nil {
		t.Fatalf("ListOIDCProviders: %v", err)
	}
	found := false
	for _, r := range rows {
		found = found || r.Name == name
	}
	if !found {
		t.Error("ListOIDCProviders did not return the row")
	}

	if linked, err := q.OAuthIdentitiesExistForProvider(ctx, name); err != nil || linked {
		t.Fatalf("identities before link = %v, %v", linked, err)
	}
	if _, err := q.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: name, Subject: "sub-1", UserID: userID, Email: "it@example.test",
	}); err != nil {
		t.Fatalf("CreateOAuthIdentity: %v", err)
	}
	if linked, err := q.OAuthIdentitiesExistForProvider(ctx, name); err != nil || !linked {
		t.Errorf("identities after link = %v, %v", linked, err)
	}

	if n, err := q.DeleteOIDCProvider(ctx, name); err != nil || n != 1 {
		t.Errorf("DeleteOIDCProvider = %d, %v", n, err)
	}
	if _, err := q.GetOIDCProvider(ctx, name); err == nil {
		t.Error("row still present after delete")
	}
}
