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

// 0159: 'internal' is a storable privacy, and every anonymous, federated and
// search-reconcile read path — all of which allowlist privacy='public' — leaves
// it out. Proven on real Postgres because the predicates are SQL: the same row
// is flipped to public and back so a broken fixture cannot pass as a filter.
func TestInternalPrivacyIsStorableAndOffEveryPublicQueryOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var owner, channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"internal-"+suffix, "internal-"+suffix+"@example.test").Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Internal') RETURNING id`,
		owner, "internalch-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'internal', 'published') RETURNING id`,
		channelID, "membersonly"+suffix).Scan(&videoID); err != nil {
		t.Fatalf("seed internal video (the widened CHECK must accept it): %v", err)
	}
	// The CHECK still refuses a value nobody defined.
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'members' WHERE id = $1`, videoID); err == nil {
		t.Fatal("privacy='members' was accepted; the CHECK must still be closed")
	}

	visible := func() map[string]bool {
		t.Helper()
		out := map[string]bool{}
		outbox, err := q.ListChannelOutboxVideos(ctx, sqlcgen.ListChannelOutboxVideosParams{ChannelID: channelID, Limit: 50})
		if err != nil {
			t.Fatalf("ListChannelOutboxVideos: %v", err)
		}
		out["federation outbox"] = len(outbox) > 0
		n, err := q.CountPublicVideosByChannel(ctx, channelID)
		if err != nil {
			t.Fatalf("CountPublicVideosByChannel: %v", err)
		}
		out["federation count"] = n > 0
		docs, err := q.ListVideoSearchDocsPage(ctx, sqlcgen.ListVideoSearchDocsPageParams{After: uuid.Nil, PageSize: 10000})
		if err != nil {
			t.Fatalf("ListVideoSearchDocsPage: %v", err)
		}
		for _, d := range docs {
			out["search reconcile"] = out["search reconcile"] || d.ID == videoID
		}
		feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{Sort: "recent", ResultLimit: 1000})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		for _, r := range feed {
			out["anonymous feed"] = out["anonymous feed"] || r.ID == videoID
		}
		hits, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
			Query: "membersonly" + suffix, Sort: "relevance", ResultLimit: 50, ViewerID: pgtype.UUID{},
		})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		out["anonymous search"] = len(hits) > 0
		return out
	}

	for surface, seen := range visible() {
		if seen {
			t.Errorf("internal video is on the %s", surface)
		}
	}
	// Control: the same row made public shows up on every one of them.
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'public' WHERE id = $1`, videoID); err != nil {
		t.Fatalf("flip to public: %v", err)
	}
	for surface, seen := range visible() {
		if !seen {
			t.Errorf("control: the public video is missing from the %s — the fixture proves nothing", surface)
		}
	}
}
