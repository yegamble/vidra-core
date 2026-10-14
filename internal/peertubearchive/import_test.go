package peertubearchive

import (
	"context"
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

type fakePlaylists struct{ made []playlist.CreateInput }

func (f *fakePlaylists) Create(_ context.Context, _ uuid.UUID, in playlist.CreateInput) (sqlcgen.Playlist, error) {
	f.made = append(f.made, in)
	return sqlcgen.Playlist{}, nil
}
func (f *fakePlaylists) ListOwn(context.Context, uuid.UUID, int32, int32) ([]sqlcgen.ListPlaylistsByOwnerRow, int64, error) {
	var rows []sqlcgen.ListPlaylistsByOwnerRow
	for _, m := range f.made {
		rows = append(rows, sqlcgen.ListPlaylistsByOwnerRow{Title: m.Title})
	}
	return rows, int64(len(rows)), nil
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
