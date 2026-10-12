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

// 0160: 'internal' is a storable privacy, and every anonymous, federated and
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

// The member half: a signed-in viewer (a non-NULL viewer_id) finds an internal
// video on the home/recent/trending feed, its channel page and — following the
// channel — the subscriptions feed, with totals that agree with the pages. An
// anonymous viewer (NULL) finds it on none of them. Search stays public-only
// for everyone: vidra-search suppresses the tier, and the local fallback must
// not answer differently from it.
func TestInternalVideosListToSignedInViewersOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	seedUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+suffix, name+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, member := seedUser("intown-"), seedUser("intmem-")
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Members') RETURNING id`,
		owner, "intlist-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'internal', 'published') RETURNING id`,
		channelID, "memberfeed"+suffix).Scan(&videoID); err != nil {
		t.Fatalf("seed internal video: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO channel_follows (follower_id, channel_id) VALUES ($1, $2)`, member, channelID); err != nil {
		t.Fatalf("follow: %v", err)
	}

	has := func(ids []uuid.UUID) bool {
		for _, id := range ids {
			if id == videoID {
				return true
			}
		}
		return false
	}
	surfaces := func(viewer pgtype.UUID) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{ViewerID: viewer, Sort: "recent", ResultLimit: 1000})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		ids := make([]uuid.UUID, 0, len(feed))
		for _, r := range feed {
			ids = append(ids, r.ID)
		}
		out["feed"] = has(ids)
		total, err := q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{ViewerID: viewer})
		if err != nil {
			t.Fatalf("CountPublicVideosSorted: %v", err)
		}
		if int(total) != len(feed) {
			t.Errorf("feed total %d != page %d (viewer valid=%v)", total, len(feed), viewer.Valid)
		}
		chRows, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{ChannelID: channelID, ViewerID: viewer, ResultLimit: 50})
		if err != nil {
			t.Fatalf("ListPublicVideosByChannel: %v", err)
		}
		out["channel page"] = len(chRows) == 1 && chRows[0].ID == videoID
		chTotal, err := q.CountPublicVideosByChannelVisible(ctx, sqlcgen.CountPublicVideosByChannelVisibleParams{ChannelID: channelID, ViewerID: viewer})
		if err != nil {
			t.Fatalf("CountPublicVideosByChannelVisible: %v", err)
		}
		out["channel total"] = chTotal == 1
		hits, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{Query: "memberfeed" + suffix, ViewerID: viewer, Sort: "relevance", ResultLimit: 50})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		out["search"] = len(hits) > 0
		return out
	}

	for surface, seen := range surfaces(pgtype.UUID{}) {
		if seen {
			t.Errorf("anonymous viewer finds the internal video on the %s", surface)
		}
	}
	signedIn := surfaces(pgtype.UUID{Bytes: member, Valid: true})
	for _, surface := range []string{"feed", "channel page", "channel total"} {
		if !signedIn[surface] {
			t.Errorf("signed-in viewer does not find the internal video on the %s", surface)
		}
	}
	if signedIn["search"] {
		t.Error("search lists the internal video; it must stay public-only like vidra-search")
	}

	subs, err := q.ListSubscriptionVideos(ctx, sqlcgen.ListSubscriptionVideosParams{FollowerID: member, ResultLimit: 50})
	if err != nil {
		t.Fatalf("ListSubscriptionVideos: %v", err)
	}
	subTotal, err := q.CountSubscriptionVideos(ctx, member)
	if err != nil {
		t.Fatalf("CountSubscriptionVideos: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != videoID || subTotal != 1 {
		t.Errorf("subscriptions = %d rows (total %d), want the internal video once", len(subs), subTotal)
	}
}

// F2 (T18 security review): the viewer guard — `v.privacy = 'internal' AND
// viewer_id IS NOT NULL` — is pinned per query, list AND count, both ways. Each
// of the four browse statements is asked about the SAME internal row by an
// anonymous caller (ViewerID unset → SQL NULL) and by a signed-in one, and must
// answer exactly 0 and exactly 1. The global feed is scoped to this fixture by a
// unique tag so other packages' rows (integration packages share the database
// and run concurrently) cannot move an exact count.
func TestInternalViewerGuardOnEveryBrowseQueryOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	ids := make([]uuid.UUID, 2)
	for i, name := range []string{"guardown-", "guardmem-"} {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+suffix, name+suffix+"@example.test").Scan(&ids[i]); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		id := ids[i]
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	}
	owner, member := ids[0], ids[1]
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Guard') RETURNING id`,
		owner, "guard-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'guarded', 'internal', 'published') RETURNING id`,
		channelID).Scan(&videoID); err != nil {
		t.Fatalf("seed internal video: %v", err)
	}
	tag := "t18guard" + suffix
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_tags (video_id, tag) VALUES ($1, $2)`, videoID, tag); err != nil {
		t.Fatalf("tag: %v", err)
	}

	for _, tc := range []struct {
		name   string
		viewer pgtype.UUID
		want   int
	}{
		{"anonymous", pgtype.UUID{}, 0},
		{"signed in", pgtype.UUID{Bytes: member, Valid: true}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			only := func(got []uuid.UUID) bool {
				return len(got) == tc.want && (tc.want == 0 || got[0] == videoID)
			}
			feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
				ViewerID: tc.viewer, Tag: &tag, Sort: "recent", ResultLimit: 50,
			})
			if err != nil {
				t.Fatalf("ListPublicVideosSorted: %v", err)
			}
			feedIDs := make([]uuid.UUID, 0, len(feed))
			for _, r := range feed {
				feedIDs = append(feedIDs, r.ID)
			}
			if !only(feedIDs) {
				t.Errorf("ListPublicVideosSorted = %v, want %d row(s) of the internal video", feedIDs, tc.want)
			}
			if n, err := q.CountPublicVideosSorted(ctx, sqlcgen.CountPublicVideosSortedParams{ViewerID: tc.viewer, Tag: &tag}); err != nil || n != int64(tc.want) {
				t.Errorf("CountPublicVideosSorted = %d, %v; want %d", n, err, tc.want)
			}
			ch, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
				ChannelID: channelID, ViewerID: tc.viewer, ResultLimit: 50,
			})
			if err != nil {
				t.Fatalf("ListPublicVideosByChannel: %v", err)
			}
			chIDs := make([]uuid.UUID, 0, len(ch))
			for _, r := range ch {
				chIDs = append(chIDs, r.ID)
			}
			if !only(chIDs) {
				t.Errorf("ListPublicVideosByChannel = %v, want %d row(s) of the internal video", chIDs, tc.want)
			}
			if n, err := q.CountPublicVideosByChannelVisible(ctx, sqlcgen.CountPublicVideosByChannelVisibleParams{ChannelID: channelID, ViewerID: tc.viewer}); err != nil || n != int64(tc.want) {
				t.Errorf("CountPublicVideosByChannelVisible = %d, %v; want %d", n, err, tc.want)
			}
		})
	}
}
