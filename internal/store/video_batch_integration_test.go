//go:build integration

// Integration tests for the video-product completeness batch (product-decisions
// §8/§16/§17/§18): free-form tags + feed filters, scheduled publish, view-day
// rollups, and the unlisted-owner discovery exclusion. Same harness contract as
// integration_test.go (live PostgreSQL via DATABASE_URL, migrations applied).
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedOwnerChannel inserts a user + channel pair for a test and registers
// cleanup via the users ON DELETE CASCADE.
func seedOwnerChannel(ctx context.Context, t *testing.T, st *Store, prefix string) (userID, channelID uuid.UUID) {
	t.Helper()
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		prefix+"-"+suffix, prefix+"-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) })
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'T') RETURNING id`,
		userID, prefix+"_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return userID, channelID
}

// seedPublishedVideo inserts a public, published video on the channel.
func seedPublishedVideo(ctx context.Context, t *testing.T, st *Store, channelID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published') RETURNING id`,
		channelID, title,
	).Scan(&id); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	return id
}

// TestVideoTagsPersistAndFilter proves the video_tags queries against a real
// PostgreSQL: insert/list/replace round-trip, the feed's ?tag/?category
// filters, and search matching a video by tag.
func TestVideoTagsPersistAndFilter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "tags")
	tagged := seedPublishedVideo(ctx, t, st, channelID, "tagged video")
	plain := seedPublishedVideo(ctx, t, st, channelID, "plain video")
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET category = '1' WHERE id = $1`, tagged); err != nil {
		t.Fatalf("set category: %v", err)
	}

	// Unique tag so the shared dev database cannot collide with this test.
	uniqueTag := "itag-" + uuid.NewString()[:8]
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{
		VideoID: tagged, Tags: []string{uniqueTag, "zebra"},
	}); err != nil {
		t.Fatalf("InsertVideoTags: %v", err)
	}
	got, err := q.ListVideoTags(ctx, tagged)
	if err != nil || len(got) != 2 || got[0] != uniqueTag || got[1] != "zebra" {
		t.Fatalf("ListVideoTags = (%v, %v), want sorted [%s zebra]", got, err, uniqueTag)
	}

	// Replace semantics: delete + reinsert leaves exactly the new set.
	if err := q.DeleteVideoTags(ctx, tagged); err != nil {
		t.Fatalf("DeleteVideoTags: %v", err)
	}
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{
		VideoID: tagged, Tags: []string{uniqueTag},
	}); err != nil {
		t.Fatalf("re-InsertVideoTags: %v", err)
	}
	if got, _ := q.ListVideoTags(ctx, tagged); len(got) != 1 || got[0] != uniqueTag {
		t.Fatalf("tags after replace = %v, want [%s]", got, uniqueTag)
	}

	// Feed tag filter: only the tagged video (unique tag isolates the run).
	tag := uniqueTag
	rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		Sort: "recent", Tag: &tag, ResultLimit: 10,
	})
	if err != nil {
		t.Fatalf("ListPublicVideosSorted(tag): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != tagged {
		t.Fatalf("tag-filtered feed = %v, want just the tagged video", rows)
	}

	// Tag + category must both hold: category '1' matches, '2' does not.
	catYes, catNo := "1", "2"
	if rows, _ = q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		Sort: "recent", Tag: &tag, Category: &catYes, ResultLimit: 10,
	}); len(rows) != 1 {
		t.Fatalf("tag+matching-category = %d rows, want 1", len(rows))
	}
	if rows, _ = q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
		Sort: "recent", Tag: &tag, Category: &catNo, ResultLimit: 10,
	}); len(rows) != 0 {
		t.Fatalf("tag+wrong-category = %d rows, want 0", len(rows))
	}

	// Search matches by tag even when the title does not contain the query.
	query := uniqueTag
	found, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
		Query: &query, ViewerID: pgtype.UUID{}, ResultLimit: 10,
	})
	if err != nil {
		t.Fatalf("SearchPublicVideos: %v", err)
	}
	if len(found) != 1 || found[0].ID != tagged {
		t.Fatalf("search by tag = %v, want the tagged video", found)
	}
	_ = plain // present to prove filters exclude it

	// Deleting the video cascades its tags away.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM videos WHERE id = $1`, tagged); err != nil {
		t.Fatalf("delete video: %v", err)
	}
	if got, _ := q.ListVideoTags(ctx, tagged); len(got) != 0 {
		t.Fatalf("tags after video delete = %v, want none (cascade)", got)
	}
}

// TestScheduledPublishPersists proves the §17 storage pieces against a real
// PostgreSQL: publish_at persists through CreateVideo/UpdateVideo, the widened
// state CHECK accepts 'scheduled', and ListDueScheduledVideos returns exactly
// the due scheduled videos (with their original's storage key).
func TestScheduledPublishPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	_, channelID := seedOwnerChannel(ctx, t, st, "sched")

	// CreateVideo persists publish_at.
	future := time.Now().Add(1 * time.Hour).UTC()
	created, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: channelID, Title: "premiere", Privacy: "public",
		PublishAt: pgtype.Timestamptz{Time: future, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	if !created.PublishAt.Valid || !created.PublishAt.Time.Equal(future) {
		t.Fatalf("created publish_at = %+v, want %v", created.PublishAt, future)
	}

	// The widened CHECK accepts the scheduled state; an original file joins in.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: created.ID, State: "scheduled"}); err != nil {
		t.Fatalf("SetVideoState scheduled: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO video_files (video_id, kind, storage_key, content_type, original_name, size_bytes)
		 VALUES ($1, 'original', $2, 'video/mp4', 'clip.mp4', 4)`,
		created.ID, "web-videos/"+created.ID.String()+".mp4",
	); err != nil {
		t.Fatalf("seed original: %v", err)
	}

	// Future-scheduled: not due.
	due, err := q.ListDueScheduledVideos(ctx, 50)
	if err != nil {
		t.Fatalf("ListDueScheduledVideos: %v", err)
	}
	for _, r := range due {
		if r.ID == created.ID {
			t.Fatalf("future-scheduled video reported due")
		}
	}

	// Rewind via UpdateVideo's COALESCE path, then it must be due with its key.
	past := time.Now().Add(-1 * time.Minute).UTC()
	if _, err := q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID: created.ID, PublishAt: pgtype.Timestamptz{Time: past, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateVideo publish_at: %v", err)
	}
	due, err = q.ListDueScheduledVideos(ctx, 50)
	if err != nil {
		t.Fatalf("ListDueScheduledVideos (due): %v", err)
	}
	found := false
	for _, r := range due {
		if r.ID == created.ID {
			found = true
			if r.StorageKey != "web-videos/"+created.ID.String()+".mp4" {
				t.Fatalf("due storage key = %q", r.StorageKey)
			}
		}
	}
	if !found {
		t.Fatalf("due-scheduled video missing from ListDueScheduledVideos")
	}

	// Published videos leave the due scan even with a past publish_at.
	if _, err := q.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: created.ID, State: "published"}); err != nil {
		t.Fatalf("SetVideoState published: %v", err)
	}
	due, _ = q.ListDueScheduledVideos(ctx, 50)
	for _, r := range due {
		if r.ID == created.ID {
			t.Fatalf("published video still reported due")
		}
	}
}

// TestVideoViewDayRollupAndStats proves the §8 stats queries against a real
// PostgreSQL: the per-day upsert accumulates, the video/channel series filter
// by cutoff, and the engagement-totals one-shots count ratings + comments.
func TestVideoViewDayRollupAndStats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID := seedOwnerChannel(ctx, t, st, "stats")
	v1 := seedPublishedVideo(ctx, t, st, channelID, "one")
	v2 := seedPublishedVideo(ctx, t, st, channelID, "two")

	// Today's rollup accumulates via the upsert.
	for i := 0; i < 3; i++ {
		if err := q.IncrementVideoViewDay(ctx, v1); err != nil {
			t.Fatalf("IncrementVideoViewDay: %v", err)
		}
	}
	if err := q.IncrementVideoViewDay(ctx, v2); err != nil {
		t.Fatalf("IncrementVideoViewDay v2: %v", err)
	}
	// An old row outside the window must not surface in the series.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO video_view_days (video_id, day, views) VALUES ($1, (now() AT TIME ZONE 'utc')::date - 60, 9)`, v1,
	); err != nil {
		t.Fatalf("seed old day: %v", err)
	}

	since := pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -29), Valid: true}
	days, err := q.ListVideoViewDays(ctx, sqlcgen.ListVideoViewDaysParams{VideoID: v1, Since: since})
	if err != nil {
		t.Fatalf("ListVideoViewDays: %v", err)
	}
	if len(days) != 1 || days[0].Views != 3 {
		t.Fatalf("video series = %+v, want one day with 3 views", days)
	}
	chDays, err := q.ListChannelViewDays(ctx, sqlcgen.ListChannelViewDaysParams{ChannelID: channelID, Since: since})
	if err != nil {
		t.Fatalf("ListChannelViewDays: %v", err)
	}
	if len(chDays) != 1 || chDays[0].Views != 4 {
		t.Fatalf("channel series = %+v, want one day with 4 aggregated views", chDays)
	}

	// Engagement totals: total views counter + a like + a comment.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO video_view_counts (video_id, views) VALUES ($1, 3)`, v1); err != nil {
		t.Fatalf("seed view count: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO video_ratings (video_id, user_id, rating) VALUES ($1, $2, 'like')`, v1, userID); err != nil {
		t.Fatalf("seed rating: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, 'hi')`, v1, userID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	vt, err := q.GetVideoEngagementTotals(ctx, v1)
	if err != nil {
		t.Fatalf("GetVideoEngagementTotals: %v", err)
	}
	if vt.Views != 3 || vt.Likes != 1 || vt.Dislikes != 0 || vt.Comments != 1 {
		t.Fatalf("video totals = %+v", vt)
	}
	ct, err := q.GetChannelEngagementTotals(ctx, channelID)
	if err != nil {
		t.Fatalf("GetChannelEngagementTotals: %v", err)
	}
	if ct.Views != 3 || ct.Likes != 1 || ct.Comments != 1 || ct.Videos != 2 {
		t.Fatalf("channel totals = %+v", ct)
	}

	// A totals-free video reports clean zeros (COALESCE paths).
	if vt2, err := q.GetVideoEngagementTotals(ctx, v2); err != nil || vt2.Views != 0 || vt2.Likes != 0 || vt2.Comments != 0 {
		t.Fatalf("empty video totals = (%+v, %v), want zeros", vt2, err)
	}
}

// TestUnlistedOwnerExcludedFromDiscovery proves the §16 exclusion against a
// real PostgreSQL: flipping users.unlisted (via the UpdateUserProfile COALESCE
// path) removes the owner's videos from the feed and search queries, while the
// direct channel list query keeps returning them.
func TestUnlistedOwnerExcludedFromDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID := seedOwnerChannel(ctx, t, st, "unl")
	// Unique title so feed/search assertions are isolated on a shared dev DB.
	title := "unlisted-probe-" + uuid.NewString()[:8]
	videoID := seedPublishedVideo(ctx, t, st, channelID, title)

	inSearch := func() bool {
		rows, err := q.SearchPublicVideos(ctx, sqlcgen.SearchPublicVideosParams{
			Query: &title, ViewerID: pgtype.UUID{}, ResultLimit: 10,
		})
		if err != nil {
			t.Fatalf("SearchPublicVideos: %v", err)
		}
		for _, r := range rows {
			if r.ID == videoID {
				return true
			}
		}
		return false
	}
	inFeed := func() bool {
		// The recent feed could be paginated past our row on a busy dev DB, so
		// probe with a search-adjacent filter: the feed shares the unlisted
		// NOT EXISTS with search; use a large page ordered recent.
		rows, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{
			Sort: "recent", ResultLimit: 500,
		})
		if err != nil {
			t.Fatalf("ListPublicVideosSorted: %v", err)
		}
		for _, r := range rows {
			if r.ID == videoID {
				return true
			}
		}
		return false
	}

	if !inFeed() || !inSearch() {
		t.Fatalf("baseline: video missing from discovery (feed=%v search=%v)", inFeed(), inSearch())
	}

	// Flip unlisted through the profile-update query (the PATCH /auth/me path).
	unlisted := true
	if _, err := q.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{ID: userID, Unlisted: &unlisted}); err != nil {
		t.Fatalf("UpdateUserProfile unlisted: %v", err)
	}
	if inFeed() || inSearch() {
		t.Fatalf("unlisted owner still discoverable (feed=%v search=%v)", inFeed(), inSearch())
	}

	// Direct channel list still serves the video.
	direct, err := q.ListPublicVideosByChannel(ctx, channelID)
	if err != nil || len(direct) != 1 || direct[0].ID != videoID {
		t.Fatalf("direct channel list while unlisted = (%v, %v), want the video", direct, err)
	}

	// Relisting restores discovery; an unrelated profile edit keeps the flag.
	relisted := false
	if _, err := q.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{ID: userID, Unlisted: &relisted}); err != nil {
		t.Fatalf("UpdateUserProfile relist: %v", err)
	}
	name := "Unrelated"
	if u, err := q.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{ID: userID, DisplayName: &name}); err != nil || u.Unlisted {
		t.Fatalf("unrelated edit flipped unlisted: (%+v, %v)", u, err)
	}
	if !inFeed() || !inSearch() {
		t.Fatalf("relisted owner not discoverable again")
	}
}
