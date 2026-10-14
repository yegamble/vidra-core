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
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/video"
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

// TestImportPlaylistItemsAgainstRealServices proves item resolution on the real
// legacy-id lookup, the real addable rule, and the real AddItem: an imported
// video (found by its PeerTube source uuid) lands in the playlist, a private
// one does not, and a re-import adds nothing twice.
func TestImportPlaylistItemsAgainstRealServices(t *testing.T) {
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
	n := uuid.NewString()[:8]
	var me, chID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
		"pi-"+n, "pi-"+n+"@example.test").Scan(&me); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, me) }()
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1,$2,'c') RETURNING id`, me, "pi"+n).Scan(&chID); err != nil {
		t.Fatal(err)
	}
	src := uuid.New()
	mk := func(privacy string, peertube *uuid.UUID) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state, peertube_uuid) VALUES ($1,'t',$2,'published',$3) RETURNING id`,
			chID, privacy, peertube).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	local, private := mk("public", nil), mk("private", nil)
	mk("public", &src)
	im := &Importer{
		Channels: channel.NewService(st.Queries()), Playlists: playlist.NewService(st.Queries()),
		Videos: video.NewService(st.Queries(), nil),
	}
	base := "https://old.example/videos/watch/"
	items := []string{base + local.String(), base + src.String(), base + private.String(), base + uuid.NewString()}
	a := &Archive{Playlists: []Playlist{{DisplayName: "P", Type: 1, Privacy: 1, Elements: len(items), Items: items}}}
	sum, err := im.Import(ctx, me, a)
	if err != nil || sum.PlaylistItemsAdded != 2 || sum.PlaylistItemsSkipped != 2 {
		t.Fatalf("first import: %+v %v", sum, err)
	}
	again, err := im.Import(ctx, me, a)
	if err != nil || again.PlaylistsExisting != 1 || again.PlaylistItemsAdded != 2 {
		t.Fatalf("re-import: %+v %v", again, err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM playlist_items pi JOIN playlists p ON p.id=pi.playlist_id WHERE p.owner_id=$1`, me).Scan(&count); err != nil || count != 2 {
		t.Fatalf("items = %d err %v", count, err)
	}
}

// TestImportChannelImagesAgainstRealServices runs an export's avatar through the
// real profile-image service and local storage: it is stored, served metadata
// is right, a hostile sibling is refused, and a re-import does not replace it.
func TestImportChannelImagesAgainstRealServices(t *testing.T) {
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
	n := uuid.NewString()[:8]
	var me uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
		"ci-"+n, "ci-"+n+"@example.test").Scan(&me); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, me) }()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imgs := profileimage.NewService(st.Queries(), blobs)
	chans := channel.NewService(st.Queries())
	im := &Importer{Channels: chans, Playlists: playlist.NewService(st.Queries()), Images: imgs,
		ScanImage: func(context.Context, string, []byte) error { return nil }}
	handle := "ci" + n
	raw := buildZip(t,
		entry{name: "peertube/channels.json", body: `{"channels":[{"name":"` + handle + `","archiveFiles":{"avatar":"video-channels/1/avatar.png","banner":"banner.png"}}]}`},
		entry{name: "files/video-channels/1/avatar.png", body: string(pngBytes(t, 16, 16))},
		entry{name: "files/banner.png", body: "not an image at all"},
	)
	a, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := im.Import(ctx, me, a)
	if err != nil || sum.ChannelImagesSet != 1 || sum.ChannelImagesSkipped != 1 {
		t.Fatalf("import: %+v %v", sum, err)
	}
	ch, _ := chans.GetByHandle(ctx, handle)
	img, err := imgs.ChannelImage(ctx, ch.ID, profileimage.KindAvatar)
	if err != nil || img.ContentType != "image/png" || imgs.HasChannelImage(ctx, ch.ID, profileimage.KindBanner) {
		t.Fatalf("avatar %+v err %v", img, err)
	}
	before := img.UpdatedAt
	if _, err := im.Import(ctx, me, a); err != nil {
		t.Fatal(err)
	}
	img2, _ := imgs.ChannelImage(ctx, ch.ID, profileimage.KindAvatar)
	if !img2.UpdatedAt.Equal(before) {
		t.Fatal("re-import replaced the avatar")
	}
}
