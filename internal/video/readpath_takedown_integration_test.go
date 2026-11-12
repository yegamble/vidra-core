//go:build integration

package video_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// TestTakedownLeavesTheCachedAnonymousListsAtOnce runs the cache over the real
// statements with a TTL of an hour, so the only thing that can take a video off
// the next anonymous list is the invalidation its write makes. A change made
// behind the service's back stays cached, which is what shows the lists really
// were served from the cache.
func TestTakedownLeavesTheCachedAnonymousListsAtOnce(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sfx := seedReadPath(t, st, readPathShape{Owners: 40, Videos: 600})
	q := st.Queries()
	svc := video.NewService(q, nil, video.WithAnonymousCache(time.Hour))

	// Four fixture videos an anonymous caller can see, with their owners.
	rows, err := st.Pool.Query(ctx, `SELECT v.id, c.owner_id FROM videos v
		JOIN channels c ON c.id = v.channel_id JOIN users u ON u.id = c.owner_id
		WHERE c.handle LIKE 'rpc' || $1 || '\_%' AND v.privacy = 'public' AND v.state = 'published'
		  AND v.title LIKE '%coffee%' AND NOT u.unlisted
		  AND NOT EXISTS (SELECT 1 FROM video_blocks b WHERE b.video_id = v.id)
		ORDER BY v.created_at DESC, v.id LIMIT 4`, sfx)
	if err != nil {
		t.Fatalf("pick videos: %v", err)
	}
	var ids, owners []uuid.UUID
	for rows.Next() {
		var id, owner uuid.UUID
		if err := rows.Scan(&id, &owner); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids, owners = append(ids, id), append(owners, owner)
	}
	rows.Close()
	if len(ids) != 4 {
		t.Fatalf("fixture has %d visible videos to take down, want 4", len(ids))
	}
	unpublished, blocked, deleted, hidden := ids[0], ids[1], ids[2], ids[3]

	listed := func(sort string, id uuid.UUID) bool {
		t.Helper()
		items, _, err := svc.ListPublic(ctx, sort, "local", video.FeedFilter{}, uuid.Nil, false, 100000, 0)
		if err != nil {
			t.Fatalf("ListPublic(%s): %v", sort, err)
		}
		return slices.ContainsFunc(items, func(it video.FeedItem) bool { return it.Video.ID == id })
	}
	matches := func() int64 {
		t.Helper()
		n, err := svc.CountSearchPublic(ctx, "coffee routine", video.SearchFilter{}, uuid.Nil, false)
		if err != nil {
			t.Fatalf("CountSearchPublic: %v", err)
		}
		return n
	}
	for _, sort := range []string{"recent", "trending"} {
		for _, id := range ids {
			if !listed(sort, id) {
				t.Fatalf("%s: video %s is not on the anonymous list to begin with", sort, id)
			}
		}
	}
	before := matches()

	// Behind the service's back: still listed, because the list is cached.
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET privacy = 'private' WHERE id = $1`, hidden); err != nil {
		t.Fatalf("direct update: %v", err)
	}
	if !listed("recent", hidden) || matches() != before {
		t.Fatalf("a change the service did not make reached the list: it was not served from the cache")
	}

	private := "private"
	if _, err := svc.Update(ctx, owners[0], unpublished, video.UpdateInput{Privacy: &private}); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	for _, sort := range []string{"recent", "trending"} {
		if listed(sort, unpublished) {
			t.Errorf("%s: the unpublished video is still on the anonymous list", sort)
		}
		if listed(sort, hidden) {
			t.Errorf("%s: the list was reloaded but still shows the row made private directly", sort)
		}
	}

	// A moderator block, as the block handler does it: the write, then the call.
	if _, err := q.BlockVideo(ctx, sqlcgen.BlockVideoParams{VideoID: blocked, Reason: "takedown test"}); err != nil {
		t.Fatalf("block: %v", err)
	}
	svc.InvalidateAnonymous()
	if listed("recent", blocked) || listed("trending", blocked) {
		t.Errorf("the blocked video is still on the anonymous list")
	}

	if err := svc.Delete(ctx, owners[2], deleted); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if listed("recent", deleted) {
		t.Errorf("the deleted video is still on the anonymous list")
	}
	if got := matches(); got != before-4 {
		t.Errorf("anonymous search count = %d after four takedowns, want %d", got, before-4)
	}
}
