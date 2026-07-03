//go:build integration

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestOAuthIdentityQueriesRoundTrip proves the oauth_identities schema
// (migration 0055) and its sqlc queries against real PostgreSQL: create,
// unique (provider, subject) resolution, per-user listing/counting, and the
// execrows delete the unlink flow relies on. Also exercises UsernameExists,
// the probe backing OAuth username derivation.
func TestOAuthIdentityQueriesRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := time.Now().UnixNano()
	username := fmt.Sprintf("oauth-it-%d", suffix)
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: username,
		Email:    fmt.Sprintf("oauth-it-%d@example.test", suffix),
		// OAuth-created accounts have no password.
		PasswordHash: "",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})

	// UsernameExists is case-insensitive, mirroring the unique index.
	if taken, err := q.UsernameExists(ctx, "OAUTH-IT-"+fmt.Sprint(suffix)); err != nil || !taken {
		t.Errorf("UsernameExists(upper) = %v, %v; want true", taken, err)
	}
	if taken, err := q.UsernameExists(ctx, username+"-free"); err != nil || taken {
		t.Errorf("UsernameExists(free) = %v, %v; want false", taken, err)
	}

	provider, subject := "it-provider", fmt.Sprintf("sub-%d", suffix)
	ident, err := q.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: provider, Subject: subject, UserID: user.ID, Email: user.Email,
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}

	// (provider, subject) resolves back to the link.
	got, err := q.GetOAuthIdentity(ctx, sqlcgen.GetOAuthIdentityParams{Provider: provider, Subject: subject})
	if err != nil || got.ID != ident.ID || got.UserID != user.ID {
		t.Fatalf("get identity = %+v, %v", got, err)
	}

	// The UNIQUE (provider, subject) constraint holds.
	if _, err := q.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: provider, Subject: subject, UserID: user.ID, Email: user.Email,
	}); err == nil {
		t.Error("duplicate (provider, subject) should violate the unique constraint")
	}

	if list, err := q.ListOAuthIdentitiesByUser(ctx, user.ID); err != nil || len(list) != 1 || list[0].Provider != provider {
		t.Errorf("list = %+v, %v; want the one link", list, err)
	}
	if n, err := q.CountOAuthIdentitiesByUser(ctx, user.ID); err != nil || n != 1 {
		t.Errorf("count = %d, %v; want 1", n, err)
	}

	// Unlink: execrows distinguishes deleted vs never-linked.
	if rows, err := q.DeleteOAuthIdentity(ctx, sqlcgen.DeleteOAuthIdentityParams{UserID: user.ID, Provider: provider}); err != nil || rows != 1 {
		t.Fatalf("delete = %d, %v; want 1 row", rows, err)
	}
	if rows, err := q.DeleteOAuthIdentity(ctx, sqlcgen.DeleteOAuthIdentityParams{UserID: user.ID, Provider: provider}); err != nil || rows != 0 {
		t.Errorf("second delete = %d, %v; want 0 rows", rows, err)
	}
}
