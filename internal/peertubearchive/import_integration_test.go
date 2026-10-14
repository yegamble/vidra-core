//go:build integration

package peertubearchive

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store"
)

// TestImportMappedHandlesAgainstRealServices drives the importer through the
// REAL channel and playlist services on Postgres: a rewritten handle must pass
// the service's own rules, a collision must take the next suffix, and the
// playlist must be attributed to the mapped channel.
func TestImportMappedHandlesAgainstRealServices(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sfx := uuid.NewString()[:8]
	var me, other uuid.UUID
	for _, u := range []*uuid.UUID{&me, &other} {
		n := uuid.NewString()[:8]
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
			"pt-"+n, "pt-"+n+"@example.test").Scan(u); err != nil {
			t.Fatal(err)
		}
		defer func(id uuid.UUID) { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) }(*u)
	}
	chans := channel.NewService(st.Queries())
	base := "mc" + sfx // 10 chars
	if _, err := chans.Create(ctx, other, channel.CreateInput{Handle: base + "_x", DisplayName: "o"}); err != nil {
		t.Fatal(err)
	}
	im := &Importer{Channels: chans, Playlists: playlist.NewService(st.Queries())}
	a := &Archive{
		Channels:  []Channel{{Name: base + "-x", DisplayName: "Mine"}},
		Playlists: []Playlist{{DisplayName: "P", Type: 1, Privacy: 1, ChannelName: base + "-x"}},
	}
	sum, err := im.Import(ctx, me, a)
	if err != nil {
		t.Fatal(err)
	}
	want := base + "_x_2"
	if sum.ChannelsCreated != 1 || len(sum.ChannelsMapped) != 1 || sum.ChannelsMapped[0].To != want {
		t.Fatalf("summary: %+v", sum)
	}
	mine, _ := chans.GetByHandle(ctx, want)
	var chID *uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT channel_id FROM playlists WHERE owner_id=$1`, me).Scan(&chID); err != nil || chID == nil || *chID != mine.ID {
		t.Fatalf("playlist channel = %v err %v want %v", chID, err, mine.ID)
	}
	again, err := im.Import(ctx, me, a)
	if err != nil || again.ChannelsCreated != 0 || again.ChannelsExisting != 1 || again.PlaylistsExisting != 1 {
		t.Fatalf("re-import: %+v %v", again, err)
	}
}
