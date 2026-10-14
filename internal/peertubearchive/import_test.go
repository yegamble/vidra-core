package peertubearchive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/peertubefollows"
	"github.com/vidra/vidra-core/internal/playersettings"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeChannels struct {
	byHandle map[string]sqlcgen.Channel
	max      int
	reserved map[string]bool
}

func (f *fakeChannels) Create(_ context.Context, owner uuid.UUID, in channel.CreateInput) (sqlcgen.Channel, error) {
	if f.reserved[in.Handle] {
		return sqlcgen.Channel{}, channel.ErrHandleReserved
	}
	if _, ok := f.byHandle[in.Handle]; ok {
		return sqlcgen.Channel{}, channel.ErrConflict
	}
	if f.max > 0 && len(f.byHandle) >= f.max {
		return sqlcgen.Channel{}, channel.ErrMaxReached
	}
	c := sqlcgen.Channel{ID: uuid.New(), OwnerID: owner, Handle: in.Handle, DisplayName: in.DisplayName, Description: in.Description}
	f.byHandle[in.Handle] = c
	return c, nil
}
func (f *fakeChannels) GetByHandle(_ context.Context, h string) (sqlcgen.Channel, error) {
	if c, ok := f.byHandle[h]; ok {
		return c, nil
	}
	return sqlcgen.Channel{}, channel.ErrNotFound
}

func (f *fakeChannels) ListOwn(_ context.Context, owner uuid.UUID) ([]sqlcgen.Channel, error) {
	var out []sqlcgen.Channel
	for _, c := range f.byHandle {
		if c.OwnerID == owner {
			out = append(out, c)
		}
	}
	return out, nil
}

type fakePlaylists struct {
	made    []playlist.CreateInput
	failAt  int // 1-based Create call that fails; 0 = never
	calls   int
	preload []string
	added   map[uuid.UUID][]uuid.UUID
}

func (f *fakePlaylists) Create(_ context.Context, _ uuid.UUID, in playlist.CreateInput) (sqlcgen.Playlist, error) {
	if f.calls++; f.calls == f.failAt {
		return sqlcgen.Playlist{}, errors.New("db down")
	}
	f.made = append(f.made, in)
	return sqlcgen.Playlist{ID: uuid.New()}, nil
}
func (f *fakePlaylists) ListOwn(_ context.Context, _ uuid.UUID, limit, offset int32) ([]sqlcgen.ListPlaylistsByOwnerRow, int64, error) {
	var all []sqlcgen.ListPlaylistsByOwnerRow
	for _, t := range f.preload {
		all = append(all, sqlcgen.ListPlaylistsByOwnerRow{Title: t})
	}
	for _, m := range f.made {
		all = append(all, sqlcgen.ListPlaylistsByOwnerRow{Title: m.Title})
	}
	if int(offset) >= len(all) {
		return nil, int64(len(all)), nil
	}
	end := min(int(offset+limit), len(all))
	return all[offset:end], int64(len(all)), nil
}

func (f *fakePlaylists) AddItem(_ context.Context, _, pl, vid uuid.UUID) error {
	if f.added == nil {
		f.added = map[uuid.UUID][]uuid.UUID{}
	}
	f.added[pl] = append(f.added[pl], vid)
	return nil
}

type fakeSettings struct{ got *bool }

func (f *fakeSettings) Update(_ context.Context, _ uuid.UUID, p playersettings.Patch) (playersettings.Settings, error) {
	f.got = p.AutoplayNext
	return playersettings.Settings{}, nil
}

func TestImportMapsAndIsIdempotent(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	ch := &fakeChannels{
		byHandle: map[string]sqlcgen.Channel{"taken_by_other": {OwnerID: other}},
		reserved: map[string]bool{"admin_acct": true},
	}
	pl, st := &fakePlaylists{}, &fakeSettings{}
	im := &Importer{Channels: ch, Playlists: pl, PlayerSettings: st}
	yes := true
	a := &Archive{
		Channels: []Channel{
			{Name: "good_one", DisplayName: "Good", Description: "d"},
			{Name: "taken_by_other", DisplayName: "x"}, // never touch another owner's
			{Name: "admin_acct", DisplayName: "x"},     // reserved by an account
			{Name: "nodisplay", DisplayName: ""},       // falls back to the handle
			{Name: "long_name", DisplayName: strings.Repeat("é", 80)},
		},
		Playlists: []Playlist{
			{DisplayName: "Pub", Privacy: 1, Type: 1, Elements: 3},
			{DisplayName: "Weird", Privacy: 99, Type: 1},
			{DisplayName: "Later", Type: 2, Elements: 5},
			{DisplayName: "  ", Type: 1},
		},
		Settings: &Settings{AutoPlayNextVideo: &yes},
		Skipped:  map[string]int{"videos": 4},
	}
	sum, err := im.Import(context.Background(), me, a)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ChannelsCreated != 3 || sum.ChannelsSkipped != 2 {
		t.Fatalf("channels: %+v", sum)
	}
	if got := ch.byHandle["nodisplay"].DisplayName; got != "nodisplay" {
		t.Fatalf("display fallback: %q", got)
	}
	if got := ch.byHandle["long_name"].DisplayName; len(got) > 50 || !strings.HasPrefix(got, "é") {
		t.Fatalf("display not rune-safe truncated: %q", got)
	}
	if sum.PlaylistsCreated != 2 || sum.PlaylistsSkipped != 1 || sum.PlaylistItemsSkipped != 3 || sum.SkippedSections["watch_later_playlists"] != 1 {
		t.Fatalf("playlists: %+v", sum)
	}
	if pl.made[0].Visibility != "public" || pl.made[1].Visibility != "private" {
		t.Fatalf("visibility: %+v", pl.made)
	}
	if sum.SettingsApplied != 1 || st.got == nil || !*st.got || sum.SkippedSections["videos"] != 4 {
		t.Fatalf("settings/skipped: %+v", sum)
	}

	again, err := im.Import(context.Background(), me, a)
	if err != nil {
		t.Fatal(err)
	}
	if again.ChannelsCreated != 0 || again.ChannelsExisting != 3 || again.PlaylistsCreated != 0 || again.PlaylistsExisting != 2 {
		t.Fatalf("re-import not idempotent: %+v", again)
	}
}

func TestImportChannelCapStopsCreation(t *testing.T) {
	ch := &fakeChannels{byHandle: map[string]sqlcgen.Channel{}, max: 1}
	im := &Importer{Channels: ch, Playlists: &fakePlaylists{}}
	sum, err := im.Import(context.Background(), uuid.New(), &Archive{
		Channels: []Channel{{Name: "first_c", DisplayName: "a"}, {Name: "second_c", DisplayName: "b"}},
	})
	if err != nil || sum.ChannelsCreated != 1 || sum.ChannelsSkipped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestImportChannelCeilingCountsOwnedChannels(t *testing.T) {
	me := uuid.New()
	ch := &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}
	for i := 0; i < 49; i++ {
		h := fmt.Sprintf("own_%02d", i)
		ch.byHandle[h] = sqlcgen.Channel{OwnerID: me, Handle: h}
	}
	im := &Importer{Channels: ch, Playlists: &fakePlaylists{}}
	sum, err := im.Import(context.Background(), me, &Archive{Channels: []Channel{
		{Name: "own_00", DisplayName: "a"}, {Name: "new_one", DisplayName: "b"}, {Name: "new_two", DisplayName: "c"},
	}})
	if err != nil || sum.ChannelsExisting != 1 || sum.ChannelsCreated != 1 || sum.ChannelsSkipped != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestImportRecognisesPlaylistsPastTheFirstPage(t *testing.T) {
	pl := &fakePlaylists{}
	for i := 0; i < 450; i++ {
		pl.preload = append(pl.preload, fmt.Sprintf("list %d", i))
	}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: pl}
	sum, err := im.Import(context.Background(), uuid.New(), &Archive{Playlists: []Playlist{{DisplayName: "List 440", Type: 1}}})
	if err != nil || sum.PlaylistsExisting != 1 || sum.PlaylistsCreated != 0 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestImportHoldsTheLockAndReturnsPartialSummary(t *testing.T) {
	var locked, unlocked int
	pl := &fakePlaylists{failAt: 2}
	im := &Importer{
		Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: pl,
		Lock: func(context.Context, uuid.UUID) (func(), error) { locked++; return func() { unlocked++ }, nil },
	}
	sum, err := im.Import(context.Background(), uuid.New(), &Archive{Playlists: []Playlist{
		{DisplayName: "one", Type: 1}, {DisplayName: "two", Type: 1},
	}})
	if err == nil || sum.PlaylistsCreated != 1 {
		t.Fatalf("want error and partial summary, got %+v %v", sum, err)
	}
	if locked != 1 || unlocked != 1 {
		t.Fatalf("lock %d unlock %d", locked, unlocked)
	}
	im.Lock = func(context.Context, uuid.UUID) (func(), error) { return nil, errors.New("busy") }
	if _, err := im.Import(context.Background(), uuid.New(), &Archive{}); err == nil {
		t.Fatal("lock failure must abort the import")
	}
}

func TestMapHandle(t *testing.T) {
	cases := map[string]string{
		"my-channel": "my_channel", "a.b.c": "a_b_c", "ok_name": "ok_name",
		"é-x": "__x", "a-": "a__", "日本語": "___",
		strings.Repeat("a-", 30): strings.Repeat("a_", 15),
	}
	for in, want := range cases {
		got, ok := MapHandle(in)
		if !ok || got != want || !channel.ValidateChannelHandle(got) {
			t.Errorf("MapHandle(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := MapHandle("  "); ok {
		t.Error("empty name must not map")
	}
}

func TestImportMapsHandlesCollisionsAndPlaylistChannel(t *testing.T) {
	me, other := uuid.New(), uuid.New()
	ch := &fakeChannels{byHandle: map[string]sqlcgen.Channel{"my_chan": {OwnerID: other}}}
	pl := &fakePlaylists{}
	im := &Importer{Channels: ch, Playlists: pl}
	a := &Archive{
		Channels:  []Channel{{Name: "my-chan"}, {Name: "my.chan"}, {Name: "plain_one"}},
		Playlists: []Playlist{{DisplayName: "P", Type: 1, ChannelName: "my-chan"}, {DisplayName: "Q", Type: 1, ChannelName: "unknown"}},
	}
	sum, err := im.Import(context.Background(), me, a)
	if err != nil {
		t.Fatal(err)
	}
	if sum.ChannelsCreated != 3 || len(sum.ChannelsMapped) != 2 ||
		sum.ChannelsMapped[0] != (ChannelMapping{"my-chan", "my_chan_2"}) ||
		sum.ChannelsMapped[1] != (ChannelMapping{"my.chan", "my_chan_3"}) {
		t.Fatalf("mapping: %+v", sum)
	}
	if pl.made[0].ChannelID == nil || *pl.made[0].ChannelID != ch.byHandle["my_chan_2"].ID || pl.made[1].ChannelID != nil {
		t.Fatalf("playlist channel: %+v", pl.made)
	}
	again, err := im.Import(context.Background(), me, a)
	if err != nil {
		t.Fatal(err)
	}
	if again.ChannelsCreated != 0 || again.ChannelsExisting != 3 || len(ch.byHandle) != 4 {
		t.Fatalf("re-import: %+v", again)
	}
}

type fakeFollows struct {
	got []string
	out peertubefollows.Queued
}

func (f *fakeFollows) Enqueue(_ context.Context, _ uuid.UUID, t []string) (peertubefollows.Queued, error) {
	f.got = t
	return f.out, nil
}

func TestImportQueuesFollows(t *testing.T) {
	id := uuid.New()
	f := &fakeFollows{out: peertubefollows.Queued{JobID: &id, Queued: 2}}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: &fakePlaylists{}, Follows: f}
	a := &Archive{Following: []string{"a@x.example", "b@y.example", "b@y.example"}}
	sum, err := im.Import(context.Background(), uuid.New(), a)
	if err != nil {
		t.Fatal(err)
	}
	if sum.FollowsQueued != 2 || sum.FollowsJobID == nil || *sum.FollowsJobID != id.String() || sum.SkippedSections["following"] != 1 {
		t.Fatalf("%+v", sum)
	}
	// No queue wired: the handles are counted as skipped, nothing is lost silently.
	bare := &Importer{Channels: im.Channels, Playlists: im.Playlists}
	sum, _ = bare.Import(context.Background(), uuid.New(), a)
	if sum.FollowsJobID != nil || sum.SkippedSections["following"] != 3 {
		t.Fatalf("%+v", sum)
	}
}

type fakeVideos struct {
	byLegacy map[uuid.UUID]uuid.UUID
	byCode   map[string]uuid.UUID
	rows     map[uuid.UUID]sqlcgen.GetVideoByIDRow
}

func (f *fakeVideos) IDByLegacyUUID(_ context.Context, l uuid.UUID) (uuid.UUID, error) {
	if id, ok := f.byLegacy[l]; ok {
		return id, nil
	}
	return uuid.Nil, errors.New("nf")
}
func (f *fakeVideos) IDByShortCode(_ context.Context, c string) (uuid.UUID, error) {
	if id, ok := f.byCode[c]; ok {
		return id, nil
	}
	return uuid.Nil, errors.New("nf")
}
func (f *fakeVideos) GetByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if r, ok := f.rows[id]; ok {
		return r, nil
	}
	return sqlcgen.GetVideoByIDRow{}, errors.New("nf")
}

type fakeBlocks map[uuid.UUID]bool

func (f fakeBlocks) IsBlocked(_ context.Context, id uuid.UUID) (bool, error) { return f[id], nil }

func TestImportPlaylistItems(t *testing.T) {
	pub, priv, blocked, imported := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := uuid.New() // the imported video's PeerTube source uuid
	row := func(state, privacy string) sqlcgen.GetVideoByIDRow {
		return sqlcgen.GetVideoByIDRow{State: state, Privacy: privacy}
	}
	vids := &fakeVideos{
		byLegacy: map[uuid.UUID]uuid.UUID{pub: pub, priv: priv, blocked: blocked, src: imported},
		byCode:   map[string]uuid.UUID{"ABCDEFGHJKL": pub},
		rows: map[uuid.UUID]sqlcgen.GetVideoByIDRow{
			pub: row("published", "public"), priv: row("published", "private"),
			blocked: row("published", "public"), imported: row("published", "public"),
		},
	}
	pl := &fakePlaylists{}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: pl, Videos: vids, Blocks: fakeBlocks{blocked: true}}
	items := []string{
		"https://tube.example/videos/watch/" + pub.String(),
		"https://tube.example/videos/watch/" + src.String(), // imported: legacy lookup
		"https://tube.example/videos/watch/" + pub.String(), // duplicate
		"https://tube.example/v/ABCDEFGHJKL",                // short code, same video
		"https://tube.example/videos/watch/" + priv.String(),
		"https://tube.example/videos/watch/" + blocked.String(),
		"https://tube.example/videos/watch/" + uuid.NewString(), // unknown
		"https://tube.example/w/abcdefghijklmnopqrstuv",         // undecoded form
		"javascript:alert(1)",
		"https://tube.example/videos/watch/not-a-uuid",
	}
	a := &Archive{Playlists: []Playlist{{DisplayName: "P", Type: 1, Privacy: 1, Elements: len(items), Items: items}}}
	sum, err := im.Import(context.Background(), uuid.New(), a)
	if err != nil {
		t.Fatal(err)
	}
	if sum.PlaylistItemsAdded != 2 || sum.PlaylistItemsSkipped != 6 ||
		sum.SkippedSections[reasonUnavailable] != 2 || sum.SkippedSections[reasonUnresolved] != 4 {
		t.Fatalf("summary: %+v", sum)
	}
	// Two distinct videos reach AddItem, in export order; the URL repeat and the
	// short-code twin of the same video are folded before the call.
	for _, got := range pl.added {
		if len(got) != 2 || got[0] != pub || got[1] != imported {
			t.Fatalf("order/targets: %v", got)
		}
	}
}

func TestImportPlaylistItemsReImportFillsExisting(t *testing.T) {
	v := uuid.New()
	vids := &fakeVideos{byLegacy: map[uuid.UUID]uuid.UUID{v: v}, rows: map[uuid.UUID]sqlcgen.GetVideoByIDRow{v: {State: "published", Privacy: "public"}}}
	pl := &fakePlaylists{preload: []string{"Mine"}}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: pl, Videos: vids}
	a := &Archive{Playlists: []Playlist{{DisplayName: "Mine", Type: 1, Elements: 1, Items: []string{"https://x.example/videos/watch/" + v.String()}}}}
	sum, err := im.Import(context.Background(), uuid.New(), a)
	if err != nil || sum.PlaylistsExisting != 1 || sum.PlaylistItemsAdded != 1 {
		t.Fatalf("%+v %v", sum, err)
	}
}

func TestImportPlaylistItemsBounded(t *testing.T) {
	pl := &fakePlaylists{}
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: pl, Videos: &fakeVideos{}}
	items := make([]string, 1000)
	for i := range items {
		items[i] = "https://x.example/videos/watch/" + uuid.NewString()
	}
	a := &Archive{Playlists: []Playlist{
		{DisplayName: "A", Type: 1, Elements: 1500, Items: items},
		{DisplayName: "B", Type: 1, Elements: 1000, Items: items},
		{DisplayName: "C", Type: 1, Elements: 1000, Items: items},
	}}
	sum, err := im.Import(context.Background(), uuid.New(), a)
	if err != nil {
		t.Fatal(err)
	}
	// 500 unretained + 1000 + 1000 looked up (budget 2000 spent) + 1000 over budget.
	if sum.SkippedSections[reasonOverCap] != 500+1000 || sum.PlaylistItemsSkipped != 3500 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestImportPlaylistItemsWithoutResolver(t *testing.T) {
	im := &Importer{Channels: &fakeChannels{byHandle: map[string]sqlcgen.Channel{}}, Playlists: &fakePlaylists{}}
	sum, _ := im.Import(context.Background(), uuid.New(), &Archive{Playlists: []Playlist{{DisplayName: "A", Type: 1, Elements: 4}}})
	if sum.PlaylistItemsSkipped != 4 || sum.PlaylistItemsAdded != 0 {
		t.Fatalf("%+v", sum)
	}
}
