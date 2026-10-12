//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/feedtoken"
)

// Rotation replaces (never accumulates), revoke is idempotent, a deactivated
// account's token stops resolving, and the account-deletion purge (which anonymises the users row, so the
// ON DELETE CASCADE never fires) removes the row explicitly.
func TestFeedTokenLifecycleAgainstSQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	svc := feedtoken.NewService(st.Queries())
	a, err := svc.Rotate(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Rotate(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.Owner(ctx, a); ok {
		t.Error("rotated-out token still resolves")
	}
	if id, ok, _ := svc.Owner(ctx, b); !ok || id != userID {
		t.Errorf("current token = %v %v", id, ok)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.Owner(ctx, b); ok {
		t.Error("deactivated account's token still resolves")
	}
	if _, err := svc.Rotate(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := st.Queries().DeleteUserFeedTokenByUser(ctx, userID); err != nil {
		t.Fatalf("account purge: %v", err)
	}
	if _, ok, _ := svc.CreatedAt(ctx, userID); ok {
		t.Error("account purge left the token row")
	}
	_ = svc.Revoke(ctx, userID)
	if err := svc.Revoke(ctx, userID); err != nil {
		t.Errorf("second revoke = %v, want idempotent", err)
	}
	if _, ok, _ := svc.CreatedAt(ctx, userID); ok {
		t.Error("token survives revoke")
	}
}
