package peertubearchive

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/playersettings"
	"github.com/vidra/vidra-core/internal/playlist"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Per-import volume caps; over-cap entries count as skipped.
const (
	maxChannels  = 50 // owned channels after the import, existing ones included
	maxPlaylists = 100
	ownPageSize  = 200 // page size when listing the caller's own playlists
)

// Channels, Playlists and PlayerSettings are the EXISTING services, taken as
// interfaces. The importer owns no insertion path of its own: every row goes
// through the same Create the HTTP API uses, so the per-user channel cap,
// handle-reservation and uniqueness rules apply unchanged.
type Channels interface {
	Create(ctx context.Context, ownerID uuid.UUID, in channel.CreateInput) (sqlcgen.Channel, error)
	GetByHandle(ctx context.Context, handle string) (sqlcgen.Channel, error)
	ListOwn(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error)
}
type Playlists interface {
	Create(ctx context.Context, ownerID uuid.UUID, in playlist.CreateInput) (sqlcgen.Playlist, error)
	ListOwn(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]sqlcgen.ListPlaylistsByOwnerRow, int64, error)
}
type PlayerSettings interface {
	Update(ctx context.Context, userID uuid.UUID, patch playersettings.Patch) (playersettings.Settings, error)
}

// Importer applies an Archive to one user's own account.
type Importer struct {
	Channels       Channels
	Playlists      Playlists
	PlayerSettings PlayerSettings // optional
	// Lock, when set, serialises imports for one user (e.g. a Postgres advisory
	// lock) so two concurrent identical imports cannot both pass the
	// already-exists checks. It returns the release func.
	Lock func(ctx context.Context, userID uuid.UUID) (func(), error)
}

// Summary is what an import did. SkippedSections counts archive entries of
// sections slice 1 does not import (videos, likes, comments, ...).
type Summary struct {
	ChannelsCreated      int            `json:"channels_created"`
	ChannelsExisting     int            `json:"channels_existing"`
	ChannelsSkipped      int            `json:"channels_skipped"`
	PlaylistsCreated     int            `json:"playlists_created"`
	PlaylistsExisting    int            `json:"playlists_existing"`
	PlaylistsSkipped     int            `json:"playlists_skipped"`
	PlaylistItemsSkipped int            `json:"playlist_items_skipped"`
	SettingsApplied      int            `json:"settings_applied"`
	SkippedSections      map[string]int `json:"skipped_sections"`
}

// Import is additive and idempotent: a channel handle you already own and a
// playlist title you already have are counted "existing", never duplicated.
func (im *Importer) Import(ctx context.Context, userID uuid.UUID, a *Archive) (Summary, error) {
	sum := Summary{SkippedSections: map[string]int{}}
	if im.Lock != nil {
		unlock, err := im.Lock(ctx, userID)
		if err != nil {
			return sum, err
		}
		defer unlock()
	}
	for k, v := range a.Skipped {
		sum.SkippedSections[k] = v
	}

	owned := map[string]bool{}
	if len(a.Channels) > 0 {
		mine, err := im.Channels.ListOwn(ctx, userID)
		if err != nil {
			return sum, err
		}
		for _, ch := range mine {
			owned[strings.ToLower(ch.Handle)] = true
		}
	}
	for _, c := range a.Channels {
		handle := strings.TrimSpace(c.Name)
		if !channel.ValidateChannelHandle(handle) {
			sum.ChannelsSkipped++
			continue
		}
		if owned[strings.ToLower(handle)] {
			sum.ChannelsExisting++
			continue
		}
		// The ceiling holds even when the instance sets no per-user cap.
		if len(owned) >= maxChannels {
			sum.ChannelsSkipped++
			continue
		}
		display := truncate(strings.TrimSpace(c.DisplayName), 50)
		if display == "" {
			display = handle
		}
		_, err := im.Channels.Create(ctx, userID, channel.CreateInput{
			Handle: handle, DisplayName: display, Description: truncate(c.Description, 1000),
		})
		switch {
		case err == nil:
			owned[strings.ToLower(handle)] = true
			sum.ChannelsCreated++
		case errors.Is(err, channel.ErrConflict):
			// Taken: ours (a re-import) or someone else's (never touched).
			if ex, gerr := im.Channels.GetByHandle(ctx, handle); gerr == nil && ex.OwnerID == userID {
				sum.ChannelsExisting++
			} else {
				sum.ChannelsSkipped++
			}
		case errors.Is(err, channel.ErrHandleReserved), errors.Is(err, channel.ErrMaxReached):
			sum.ChannelsSkipped++
		default:
			return sum, err
		}
	}

	if len(a.Playlists) > 0 {
		have := map[string]bool{}
		// Every page, so a title past the first page is still recognised.
		for offset := int32(0); ; offset += ownPageSize {
			rows, _, err := im.Playlists.ListOwn(ctx, userID, ownPageSize, offset)
			if err != nil {
				return sum, err
			}
			for _, r := range rows {
				have[strings.ToLower(r.Title)] = true
			}
			if len(rows) < ownPageSize {
				break
			}
		}
		for i, p := range a.Playlists {
			title := strings.TrimSpace(truncate(p.DisplayName, 200))
			if p.Type == 2 { // watch-later: vidra's analogue holds videos, not a playlist
				sum.SkippedSections["watch_later_playlists"]++
				continue
			}
			if i >= maxPlaylists || title == "" {
				sum.PlaylistsSkipped++
				continue
			}
			if have[strings.ToLower(title)] {
				sum.PlaylistsExisting++
				continue
			}
			if _, err := im.Playlists.Create(ctx, userID, playlist.CreateInput{
				Title: title, Description: truncate(p.Description, 5000), Visibility: visibility(p.Privacy),
			}); err != nil {
				return sum, err
			}
			have[strings.ToLower(title)] = true
			sum.PlaylistsCreated++
			// Items reference videos by URL; resolving them is slice 2.
			sum.PlaylistItemsSkipped += p.Elements
		}
	}

	if s := a.Settings; s != nil && s.AutoPlayNextVideo != nil && im.PlayerSettings != nil {
		if _, err := im.PlayerSettings.Update(ctx, userID, playersettings.Patch{AutoplayNext: s.AutoPlayNextVideo}); err != nil {
			return sum, err
		}
		sum.SettingsApplied++
	}
	return sum, nil
}

// visibility maps PeerTube's privacy enum; anything unknown fails safe.
func visibility(privacy int) string {
	switch privacy {
	case 1:
		return "public"
	case 2:
		return "unlisted"
	}
	return "private"
}

// truncate bounds s to max bytes on a rune boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
