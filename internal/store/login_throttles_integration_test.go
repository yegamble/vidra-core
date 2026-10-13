//go:build integration

// Integration test: the failed-login counters' SQL (migration 0175) against a
// real PostgreSQL. The window restart, the atomic increment and the lock filter
// are database behaviours a fake would only restate.
package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestLoginThrottleQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	n := time.Now().UnixNano()
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: fmt.Sprintf("thr-%d", n), Email: fmt.Sprintf("thr-%d@example.test", n), PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID) })

	now := time.Now()
	rec := func(at time.Time) int32 {
		c, err := q.RecordLoginFailure(ctx, sqlcgen.RecordLoginFailureParams{UserID: user.ID, Scope: "ip:1.2.3.0/24", Now: at, WindowSeconds: 900})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if rec(now) != 1 || rec(now) != 2 || rec(now.Add(time.Minute)) != 3 {
		t.Fatal("failures inside the window must accumulate")
	}
	if got := rec(now.Add(16 * time.Minute)); got != 1 {
		t.Fatalf("count after the window lapsed = %d, want a restart at 1", got)
	}

	lockUntil := now.Add(time.Minute)
	if err := q.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{
		UserID: user.ID, Scope: "ip:1.2.3.0/24", LockedUntil: pgtype.Timestamptz{Time: lockUntil, Valid: true}, ResetCount: true,
	}); err != nil {
		t.Fatal(err)
	}
	locks := func(at time.Time) int64 {
		c, err := q.CountActiveLoginLocks(ctx, sqlcgen.CountActiveLoginLocksParams{
			UserID: user.ID, Scopes: []string{"ip:1.2.3.0/24", "all"}, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if locks(now) != 1 || locks(now.Add(2*time.Minute)) != 0 {
		t.Fatal("lock must be active before locked_until and gone after")
	}
	if rec(now) != 1 {
		t.Fatal("ResetCount must restart the counter")
	}
	cleared, err := q.ClearLoginThrottles(ctx, user.ID)
	if err != nil || cleared != 1 || locks(now) != 0 {
		t.Fatalf("admin clear = %d, %v; locks=%d", cleared, err, locks(now))
	}
}
