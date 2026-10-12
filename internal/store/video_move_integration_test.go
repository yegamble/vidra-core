//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestUpdateVideoMovesChannel proves the COALESCE(narg('channel_id'), channel_id)
// assignment against real PostgreSQL: a set id moves the video AND the owner the
// GetVideoByID JOIN derives follows it; an unset id (every other caller of
// UpdateVideo) leaves the channel alone; a channel that does not exist is
// refused by the foreign key rather than orphaning the video.
func TestUpdateVideoMovesChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	ownerA, chA := seedOwnerChannel(ctx, t, st, "mvA")
	ownerB, chB := seedOwnerChannel(ctx, t, st, "mvB")
	vid := seedPublishedVideo(ctx, t, st, chA, "movable")

	title := "renamed"
	if _, err := q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{ID: vid, Title: &title}); err != nil {
		t.Fatalf("UpdateVideo (no channel): %v", err)
	}
	row, err := q.GetVideoByID(ctx, vid)
	if err != nil || row.ChannelID != chA || row.OwnerID != ownerA {
		t.Fatalf("unset channel_id moved the video: ch=%s owner=%s err=%v", row.ChannelID, row.OwnerID, err)
	}

	moved, err := q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID: vid, ChannelID: pgtype.UUID{Bytes: chB, Valid: true},
	})
	if err != nil || moved.ChannelID != chB {
		t.Fatalf("move: ch=%s err=%v", moved.ChannelID, err)
	}
	row, err = q.GetVideoByID(ctx, vid)
	if err != nil || row.ChannelID != chB || row.OwnerID != ownerB || row.Title != "renamed" {
		t.Fatalf("after move: ch=%s owner=%s title=%q err=%v", row.ChannelID, row.OwnerID, row.Title, err)
	}

	if _, err := q.UpdateVideo(ctx, sqlcgen.UpdateVideoParams{
		ID: vid, ChannelID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}); err == nil {
		t.Fatal("move to a nonexistent channel succeeded")
	}
}
