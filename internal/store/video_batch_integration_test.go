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
