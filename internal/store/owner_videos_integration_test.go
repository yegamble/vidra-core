//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestListOwnerVideosFiltersOnRealPG proves the studio "My videos" SQL where
// only SQL can be proven: owner scoping across channels, every filter alone and
// combined, LIKE-escaped search, and that LIST and COUNT agree (a page that
// promised a total it could not serve would be a pagination lie).
func TestListOwnerVideosFiltersOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	mkChan := func(owner uuid.UUID, h string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, $2) RETURNING id`,
			owner, h+"-"+suffix).Scan(&id); err != nil {
			t.Fatalf("seed channel: %v", err)
		}
		return id
	}
	mkVid := func(ch uuid.UUID, title, privacy, state string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, $3, $4)`,
			ch, title, privacy, state); err != nil {
			t.Fatalf("seed video: %v", err)
		}
	}
	ada, bob := mkUser("ada"), mkUser("bob")
	a1, a2, b1 := mkChan(ada, "a1"), mkChan(ada, "a2"), mkChan(bob, "b1")
	mkVid(a1, "Holiday vlog", "public", "published")
	mkVid(a1, "Holiday draft", "private", "draft")
	mkVid(a2, "Holiday two", "internal", "published")
	mkVid(a2, "100% sure_thing", "unlisted", "processing")
	mkVid(b1, "Holiday bob", "public", "published")

	pg := func(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	sp := func(s string) *string { return &s }
	run := func(name string, owner uuid.UUID, qs, privacy, state *string, ch pgtype.UUID, want int) {
		t.Helper()
		rows, err := q.ListOwnerVideos(ctx, sqlcgen.ListOwnerVideosParams{
			OwnerID: owner, Q: qs, Privacy: privacy, State: state, ChannelID: ch, Sort: "-published_at", ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("%s: list: %v", name, err)
		}
		n, err := q.CountOwnerVideos(ctx, sqlcgen.CountOwnerVideosParams{OwnerID: owner, Q: qs, Privacy: privacy, State: state, ChannelID: ch})
		if err != nil {
			t.Fatalf("%s: count: %v", name, err)
		}
		if len(rows) != want || n != int64(want) {
			t.Errorf("%s: list=%d count=%d, want %d", name, len(rows), n, want)
		}
	}
	none := pgtype.UUID{}
	run("all of ada's, across channels", ada, nil, nil, nil, none, 4)
	run("q case-insensitive", ada, sp("HOLIDAY"), nil, nil, none, 3)
	run("q + privacy", ada, sp("holiday"), sp("public"), nil, none, 1)
	run("state", ada, nil, nil, sp("published"), none, 2)
	run("internal privacy", ada, nil, sp("internal"), nil, none, 1)
	run("channel", ada, nil, nil, nil, pg(a2), 2)
	run("q + state + channel", ada, sp("holiday"), nil, sp("published"), pg(a2), 1)
	run("escaped % is literal", ada, sp(`100\%`), nil, nil, none, 1)
	run("unescaped % would match all", ada, sp(`%`), nil, nil, none, 4) // raw wildcard: the HANDLER-side service escapes it
	run("escaped _ is literal", ada, sp(`sure\_thing`), nil, nil, none, 1)
	run("foreign channel_id matches nothing", ada, nil, nil, nil, pg(b1), 0)
	run("bob sees only bob", bob, sp("holiday"), nil, nil, none, 1)
}
