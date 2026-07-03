//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestUserMFAQueriesRoundTrip proves the user_mfa / mfa_recovery_codes schema
// (migration 0056) and its sqlc queries against real PostgreSQL: the pending →
// enabled enrollment lifecycle (incl. the upsert's WHERE NOT enabled guard),
// the single-use recovery-code redemption (UseRecoveryCode execrows), and the
// disable teardown.
func TestUserMFAQueriesRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := time.Now().UnixNano()
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     fmt.Sprintf("mfa-it-%d", suffix),
		Email:        fmt.Sprintf("mfa-it-%d@example.test", suffix),
		PasswordHash: "x",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		// user_mfa and mfa_recovery_codes rows cascade with the user.
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})

	// Pending enrollment: insert, then restart replaces the secret in place.
	row, err := q.UpsertUserMFA(ctx, sqlcgen.UpsertUserMFAParams{UserID: user.ID, TotpSecretSealed: "enc:first"})
	if err != nil || row.Enabled {
		t.Fatalf("upsert = %+v, %v; want pending row", row, err)
	}
	row, err = q.UpsertUserMFA(ctx, sqlcgen.UpsertUserMFAParams{UserID: user.ID, TotpSecretSealed: "enc:second"})
	if err != nil || row.TotpSecretSealed != "enc:second" || row.Enabled {
		t.Fatalf("re-upsert = %+v, %v; want replaced pending secret", row, err)
	}

	// Enable flips exactly once (execrows contract).
	if n, err := q.EnableUserMFA(ctx, user.ID); err != nil || n != 1 {
		t.Fatalf("enable = %d, %v; want 1 row", n, err)
	}
	if n, err := q.EnableUserMFA(ctx, user.ID); err != nil || n != 0 {
		t.Fatalf("second enable = %d, %v; want 0 rows", n, err)
	}
	if row, err := q.GetUserMFA(ctx, user.ID); err != nil || !row.Enabled {
		t.Fatalf("get = %+v, %v; want enabled", row, err)
	}
	// The WHERE NOT enabled guard blocks an upsert against an enabled row
	// (sqlc :one reports no row).
	if _, err := q.UpsertUserMFA(ctx, sqlcgen.UpsertUserMFAParams{UserID: user.ID, TotpSecretSealed: "enc:third"}); err == nil {
		t.Fatal("upsert against an ENABLED row must not modify it")
	}
	if row, _ := q.GetUserMFA(ctx, user.ID); row.TotpSecretSealed != "enc:second" {
		t.Fatalf("secret = %q, want enc:second untouched", row.TotpSecretSealed)
	}

	// Recovery codes: 10 hashes in, count, single-use redemption.
	hash := func(i int) string {
		sum := sha256.Sum256([]byte(fmt.Sprintf("code-%d-%d", suffix, i)))
		return hex.EncodeToString(sum[:])
	}
	for i := 0; i < 10; i++ {
		if err := q.CreateRecoveryCode(ctx, sqlcgen.CreateRecoveryCodeParams{UserID: user.ID, CodeHash: hash(i)}); err != nil {
			t.Fatalf("create code %d: %v", i, err)
		}
	}
	if n, err := q.CountUnusedRecoveryCodes(ctx, user.ID); err != nil || n != 10 {
		t.Fatalf("count = %d, %v; want 10", n, err)
	}
	if n, err := q.UseRecoveryCode(ctx, sqlcgen.UseRecoveryCodeParams{UserID: user.ID, CodeHash: hash(3)}); err != nil || n != 1 {
		t.Fatalf("use = %d, %v; want 1 row", n, err)
	}
	// Single-use: the same hash redeems zero rows the second time.
	if n, err := q.UseRecoveryCode(ctx, sqlcgen.UseRecoveryCodeParams{UserID: user.ID, CodeHash: hash(3)}); err != nil || n != 0 {
		t.Fatalf("re-use = %d, %v; want 0 rows", n, err)
	}
	if n, err := q.CountUnusedRecoveryCodes(ctx, user.ID); err != nil || n != 9 {
		t.Fatalf("count after use = %d, %v; want 9", n, err)
	}
	// An unknown hash redeems nothing.
	if n, err := q.UseRecoveryCode(ctx, sqlcgen.UseRecoveryCodeParams{UserID: user.ID, CodeHash: hash(99)}); err != nil || n != 0 {
		t.Fatalf("unknown-hash use = %d, %v; want 0 rows", n, err)
	}

	// Disable teardown: delete the config + codes; second delete is 0 rows.
	if n, err := q.DeleteUserMFA(ctx, user.ID); err != nil || n != 1 {
		t.Fatalf("delete = %d, %v; want 1 row", n, err)
	}
	if n, err := q.DeleteUserMFA(ctx, user.ID); err != nil || n != 0 {
		t.Fatalf("second delete = %d, %v; want 0 rows", n, err)
	}
	if err := q.DeleteRecoveryCodes(ctx, user.ID); err != nil {
		t.Fatalf("delete codes: %v", err)
	}
	if n, err := q.CountUnusedRecoveryCodes(ctx, user.ID); err != nil || n != 0 {
		t.Fatalf("count after teardown = %d, %v; want 0", n, err)
	}
}
