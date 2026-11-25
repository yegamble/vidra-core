//go:build integration

package video_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/video"
)

// This video spent years as a draft. Only its publication places it ahead of
// newer uploads on the public channel and discovery pages.
func TestNewestFeedUsesPublicationTime(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	suffix := seedReadPath(t, st, readPathShape{Owners: 1, Videos: 30})
	var id, channel uuid.UUID
	created := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	published := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.Pool.QueryRow(ctx, `UPDATE videos SET created_at=$2,peertube_published_at=$3
 WHERE id=(SELECT v.id FROM videos v JOIN channels c ON v.channel_id=c.id WHERE c.handle='rpc'||$1||'_1' AND v.state='published' AND v.privacy='public' AND NOT EXISTS(SELECT 1 FROM video_blocks b WHERE b.video_id=v.id) LIMIT 1) RETURNING id,channel_id`, suffix, created, published).Scan(&id, &channel)
	if err != nil {
		t.Fatal(err)
	}
	svc := video.NewService(st.Queries(), nil)
	feed, _, err := svc.ListPublic(ctx, "recent", "local", video.FeedFilter{}, uuid.Nil, false, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err := svc.ListPublicByChannel(ctx, channel, uuid.Nil, false, false, "-published_at", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, items := range map[string][]video.FeedItem{"feed": feed, "channel": ch} {
		if len(items) != 1 || items[0].Video.ID != id {
			t.Errorf("%s did not return the newly published import first", name)
			continue
		}
		v := items[0].Video
		if !v.CreatedAt.Equal(created) {
			t.Errorf("%s lost the creation timestamp", name)
		}
	}
	after := published.Add(-time.Hour)
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET title='uniquepubfixture' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	filtered, total, err := svc.SearchPublic(ctx, "uniquepubfixture", video.SearchFilter{PublishedAfter: &after}, video.SearchSortNewest, uuid.Nil, false, 1, 0)
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].Video.ID != id {
		t.Fatalf("date-filtered search returned %d rows, total %d, error %v", len(filtered), total, err)
	}

}
