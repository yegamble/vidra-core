//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/notification"
)

// TestUnsubscribeEmailForADeletedUserOnRealPG pins the premise behind the
// one-click unsubscribe handler swallowing SQLSTATE 23503: a write for a user
// that no longer exists really does fail with exactly that code, while the
// service's own path (read first, write nothing when nothing is on) never
// reaches it. A change of constraint or code would turn into 500s to mailbox
// providers, so it is asserted against a live schema.
func TestUnsubscribeEmailForADeletedUserOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	svc := notification.NewService(st.Queries())

	var id uuid.UUID
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"unsub-"+suffix, "unsub-"+suffix+"@example.test").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })

	if err := svc.SetEmailPrefs(ctx, id, map[string]bool{notification.TypeComment: true}); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	changed, err := svc.UnsubscribeEmail(ctx, id)
	if err != nil || !changed {
		t.Fatalf("UnsubscribeEmail = %v, %v; want true, nil", changed, err)
	}
	if on, _ := svc.EmailPrefs(ctx, id); on[notification.TypeComment] {
		t.Fatal("comment email still on")
	}
	if changed, err := svc.UnsubscribeEmail(ctx, id); err != nil || changed {
		t.Fatalf("repeat = %v, %v; want false, nil", changed, err)
	}

	if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	// The service no-ops for a user with nothing on...
	if changed, err := svc.UnsubscribeEmail(ctx, id); err != nil || changed {
		t.Fatalf("after delete = %v, %v; want false, nil", changed, err)
	}
	// ...and the code the handler swallows is the one a racing write gets.
	err = svc.SetEmailPrefs(ctx, id, map[string]bool{notification.TypeComment: false})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("write for a deleted user = %v, want SQLSTATE 23503", err)
	}
}
