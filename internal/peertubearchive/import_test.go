package peertubearchive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
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
}

func (f *fakePlaylists) Create(_ context.Context, _ uuid.UUID, in playlist.CreateInput) (sqlcgen.Playlist, error) {
	if f.calls++; f.calls == f.failAt {
		return sqlcgen.Playlist{}, errors.New("db down")
	}
	f.made = append(f.made, in)
	return sqlcgen.Playlist{}, nil
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
			{Name: "bad-dash", DisplayName: "x"},       // handle alphabet
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
	if sum.ChannelsCreated != 3 || sum.ChannelsSkipped != 3 {
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
