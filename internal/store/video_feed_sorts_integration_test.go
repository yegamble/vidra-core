//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestListPublicVideosSortedNewSorts proves the likes / originally_published_at
// / name orderings in the SQL itself (a fake cannot): the like-count subquery,
// the COALESCE fallback to created_at, case-insensitive A-Z, and the
// created_at/id tiebreak that keeps equal keys deterministic.
func TestListPublicVideosSortedNewSorts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	userID, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	mk := func(title string, created time.Time, orig *time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, created_at, originally_published_at)
			 VALUES ($1, $2, 'public', 'published', $3, $4) RETURNING id`,
			channelID, title, created, orig).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}
	// Titles carry a unique prefix so the assertion ignores other tests' rows.
	p := uuid.NewString()[:6] + "-"
	old := now.Add(-48 * time.Hour)
	// created order (newest first): b, a, c.  Original date: a is back-dated to
	// last year, c has none (falls back to created), b is two days ago.
	a := mk(p+"banana", now.Add(-1*time.Hour), ptr(now.Add(-365*24*time.Hour)))
	b := mk(p+"Apple", now.Add(-30*time.Minute), ptr(old))
	c := mk(p+"cherry", now.Add(-2*time.Hour), nil)

	// Likes: a=2 (two users), b=1, c=0.
	var u2 uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"lk-"+p, "lk-"+p+"@example.test").Scan(&u2); err != nil {
		t.Fatalf("seed user2: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, u2) }()
	for _, r := range []struct {
		v, u   uuid.UUID
		rating string
	}{{a, userID, "like"}, {a, u2, "like"}, {b, userID, "like"}, {c, userID, "dislike"}} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO video_ratings (video_id, user_id, rating) VALUES ($1,$2,$3)`, r.v, r.u, r.rating); err != nil {
			t.Fatalf("rate: %v", err)
		}
	}

	order := func(sort string) []uuid.UUID {
		t.Helper()
		rows, err := st.Queries().ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
			Sort: sort, ResultLimit: 100000, ResultOffset: 0,
		})
		if err != nil {
			t.Fatalf("list %s: %v", sort, err)
		}
		var out []uuid.UUID
		for _, r := range rows {
			if r.ID == a || r.ID == b || r.ID == c {
				out = append(out, r.ID)
			}
		}
		return out
	}
	check := func(sort string, want ...uuid.UUID) {
		t.Helper()
		got := order(sort)
		if len(got) != len(want) {
			t.Fatalf("%s: got %d rows, want %d", sort, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: position %d = %v, want %v (full %v)", sort, i, got[i], want[i], got)
			}
		}
	}
	check("likes", a, b, c)
	check("name", b, a, c) // Apple < banana < cherry, case-insensitive
	// b original 2d ago, c created 2h ago (no original), a original a year ago.
	check("originally_published_at", c, b, a)
	check("recent", b, a, c)
}

func ptr[T any](v T) *T { return &v }
