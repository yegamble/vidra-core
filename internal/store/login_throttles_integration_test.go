//go:build integration

// Integration test: the failed-login counters' SQL (migration 0175) against a
// real PostgreSQL. The window restart, the atomic increment and the lock filter
// are database behaviours a fake would only restate.
package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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
	att := func(at time.Time) sqlcgen.RecordLoginAttemptRow {
		if err := q.EnsureLoginThrottle(ctx, sqlcgen.EnsureLoginThrottleParams{UserID: user.ID, Scope: "ip:1.2.3.0/24", Now: at}); err != nil {
			t.Fatal(err)
		}
		r, err := q.RecordLoginAttempt(ctx, sqlcgen.RecordLoginAttemptParams{UserID: user.ID, Scope: "ip:1.2.3.0/24", Now: at, WindowSeconds: 900})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if att(now).FailCount != 1 || att(now).FailCount != 2 || att(now.Add(time.Minute)).FailCount != 3 {
		t.Fatal("attempts inside the window must accumulate")
	}
	if got := att(now.Add(16 * time.Minute)); got.FailCount != 1 {
		t.Fatalf("count after the window lapsed = %d, want a restart at 1", got.FailCount)
	}

	setLock := func(until time.Time, reset bool) {
		if err := q.SetLoginLock(ctx, sqlcgen.SetLoginLockParams{UserID: user.ID, Scope: "ip:1.2.3.0/24", LockedUntil: until, ResetCount: reset}); err != nil {
			t.Fatal(err)
		}
	}
	lockUntil := now.Add(time.Minute)
	setLock(lockUntil, false)
	setLock(now.Add(10*time.Second), false) // GREATEST: a shorter write never shortens
	locks := func(at time.Time) int64 {
		c, err := q.CountActiveLoginLocks(ctx, sqlcgen.CountActiveLoginLocksParams{
			UserID: user.ID, Scopes: []string{"ip:1.2.3.0/24", "all"}, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if locks(now.Add(30*time.Second)) != 1 || locks(now.Add(2*time.Minute)) != 0 {
		t.Fatal("lock must hold to the longest write and be gone after")
	}
	// The first attempt after expiry is the probe and clears the lock; a second
	// concurrent-looking one is not.
	if p := att(now.Add(2 * time.Minute)); !p.Probe {
		t.Fatal("first attempt after expiry must be the probe")
	}
	if p := att(now.Add(2 * time.Minute)); p.Probe {
		t.Fatal("only one attempt may be the probe")
	}

	// Parallel attempts get distinct, gap-free counts (atomic increment).
	setLock(now, true) // reset count
	var wg sync.WaitGroup
	seen := make(chan int32, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := q.RecordLoginAttempt(ctx, sqlcgen.RecordLoginAttemptParams{UserID: user.ID, Scope: "ip:1.2.3.0/24", Now: now.Add(3 * time.Minute), WindowSeconds: 900})
			if err == nil {
				seen <- r.FailCount
			}
		}()
	}
	wg.Wait()
	close(seen)
	uniq := map[int32]bool{}
	for c := range seen {
		uniq[c] = true
	}
	if len(uniq) != 20 {
		t.Fatalf("parallel attempts must get 20 distinct counts, got %d", len(uniq))
	}

	// Success memory.
	since := now.Add(-time.Hour)
	if err := q.RecordLoginSuccess(ctx, sqlcgen.RecordLoginSuccessParams{UserID: user.ID, Scope: "ok:ip:1.2.3.0/24", Now: now}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := q.HasRecentLoginSuccess(ctx, sqlcgen.HasRecentLoginSuccessParams{UserID: user.ID, Scope: "ok:ip:1.2.3.0/24", Since: since}); !ok {
		t.Fatal("recent success not remembered")
	}
	cleared, err := q.ClearLoginThrottles(ctx, user.ID)
	if err != nil || cleared < 1 || locks(now) != 0 {
		t.Fatalf("admin clear = %d, %v", cleared, err)
	}
}
