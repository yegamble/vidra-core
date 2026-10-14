package peertubearchive

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/peertubefollows"
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

// FollowQueue queues remote-follow targets for one user.
type FollowQueue interface {
	Enqueue(ctx context.Context, userID uuid.UUID, targets []string) (peertubefollows.Queued, error)
}

// Importer applies an Archive to one user's own account.
type Importer struct {
	Channels       Channels
	Playlists      Playlists
	PlayerSettings PlayerSettings // optional
	// Follows, when set, queues the export's following.json handles for a
	// background follow (see the peertubefollows package).
	Follows FollowQueue
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
	// ChannelsMapped lists every channel whose PeerTube name was rewritten into
	// vidra's handle alphabet (original -> handle actually used).
	ChannelsMapped []ChannelMapping `json:"channels_mapped"`
	// FollowsQueued targets will be followed in the background; poll
	// GET /me/import/peertube/follows/{follows_job_id}. Handles not queued
	// (duplicate, over the cap, malformed) are counted in
	// skipped_sections.following; follows_skipped_federation counts those held
	// back because federation is disabled.
	FollowsQueued            int     `json:"follows_queued"`
	FollowsSkippedFederation int     `json:"follows_skipped_federation"`
	FollowsJobID             *string `json:"follows_job_id"`
}

// ChannelMapping records one PeerTube channel name and the vidra handle it became.
type ChannelMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

const (
	maxHandleLen    = 30 // mirrors channel.handleRe {3,30}
	minHandleLen    = 3
	maxHandleSuffix = 20 // collision attempts: _2 ... _21
)

// MapHandle is the deterministic PeerTube-name -> vidra-handle mapping: every
// rune outside [A-Za-z0-9_] (so '-', '.', and any non-ASCII rune) becomes one
// '_', the result is cut to 30 characters, and anything shorter than 3 is padded
// with '_'. A name that is already valid is returned unchanged. ok is false only
// for an empty name. The result is always re-checked with
// channel.ValidateChannelHandle; creation still goes through channel.Service.
func MapHandle(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if channel.ValidateChannelHandle(name) {
		return name, true
	}
	var b strings.Builder
	for _, r := range name {
		if r < utf8.RuneSelf && (r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	h := b.String()
	if len(h) > maxHandleLen {
		h = h[:maxHandleLen]
	}
	for len(h) < minHandleLen {
		h += "_"
	}
	return h, channel.ValidateChannelHandle(h)
}

// suffixed returns base with "_<n>" appended, cutting base to keep 30 chars.
func suffixed(base string, n int) string {
	suf := "_" + strconv.Itoa(n)
	if len(base)+len(suf) > maxHandleLen {
		base = base[:maxHandleLen-len(suf)]
	}
	return base + suf
}

// Import is additive and idempotent: a channel handle you already own and a
// playlist title you already have are counted "existing", never duplicated.
func (im *Importer) Import(ctx context.Context, userID uuid.UUID, a *Archive) (Summary, error) {
	sum := Summary{SkippedSections: map[string]int{}, ChannelsMapped: []ChannelMapping{}}
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
	claimed := map[string]string{} // lower(handle) -> the archive name that took it
	ids := map[string]uuid.UUID{}  // lower(PeerTube name) -> the channel it resolved to
	if len(a.Channels) > 0 {
		mine, err := im.Channels.ListOwn(ctx, userID)
		if err != nil {
			return sum, err
		}
		for _, ch := range mine {
			owned[strings.ToLower(ch.Handle)] = true
			ids[strings.ToLower(ch.Handle)] = ch.ID
		}
	}
	for _, c := range a.Channels {
		name := strings.TrimSpace(c.Name)
		handle, ok := MapHandle(name)
		if !ok {
			sum.ChannelsSkipped++
			continue
		}
		mapped := handle != name
		display := truncate(strings.TrimSpace(c.DisplayName), 50)
		if display == "" {
			display = name
		}
		base := handle
	attempt:
		for n := 1; ; n++ {
			if n > 1 {
				handle = suffixed(base, n)
			}
			if lh := strings.ToLower(handle); owned[lh] {
				// Ours already. Another name of THIS archive may have claimed it
				// (two names mapping to one handle): then try the next suffix.
				if claimed[lh] == "" {
					claimed[lh] = name
					ids[strings.ToLower(name)] = ids[lh]
					sum.ChannelsExisting++
					break attempt
				}
				if !mapped || n > maxHandleSuffix {
					sum.ChannelsSkipped++
					break attempt
				}
				continue
			}
			// The ceiling holds even when the instance sets no per-user cap.
			if len(owned) >= maxChannels {
				sum.ChannelsSkipped++
				break attempt
			}
			created, err := im.Channels.Create(ctx, userID, channel.CreateInput{
				Handle: handle, DisplayName: display, Description: truncate(c.Description, 1000),
			})
			switch {
			case err == nil:
				owned[strings.ToLower(handle)] = true
				ids[strings.ToLower(handle)] = created.ID
				claimed[strings.ToLower(handle)] = name
				ids[strings.ToLower(name)] = created.ID
				sum.ChannelsCreated++
				if mapped || n > 1 {
					sum.ChannelsMapped = append(sum.ChannelsMapped, ChannelMapping{From: name, To: handle})
				}
				break attempt
			case errors.Is(err, channel.ErrConflict):
				// Taken: ours (a re-import) or someone else's (never touched).
				ex, gerr := im.Channels.GetByHandle(ctx, handle)
				if gerr == nil && ex.OwnerID == userID {
					owned[strings.ToLower(handle)] = true
					ids[strings.ToLower(handle)] = ex.ID
					claimed[strings.ToLower(handle)] = name
					ids[strings.ToLower(name)] = ex.ID
					sum.ChannelsExisting++
					break attempt
				}
				// A rewritten name may take the next free suffix; an exact valid
				// handle held by someone else is never renamed around.
				if !mapped || n > maxHandleSuffix {
					sum.ChannelsSkipped++
					break attempt
				}
			case errors.Is(err, channel.ErrHandleReserved), errors.Is(err, channel.ErrMaxReached):
				sum.ChannelsSkipped++
				break attempt
			default:
				return sum, err
			}
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
			in := playlist.CreateInput{
				Title: title, Description: truncate(p.Description, 5000), Visibility: visibility(p.Privacy),
			}
			// A playlist follows its channel to the mapped handle's row.
			if id, ok := ids[strings.ToLower(p.ChannelName)]; ok && id != uuid.Nil {
				in.ChannelID = &id
			}
			if _, err := im.Playlists.Create(ctx, userID, in); err != nil {
				return sum, err
			}
			have[strings.ToLower(title)] = true
			sum.PlaylistsCreated++
			// Items reference videos by URL; resolving them is slice 2.
			sum.PlaylistItemsSkipped += p.Elements
		}
	}

	if len(a.Following) > 0 && im.Follows != nil {
		q, err := im.Follows.Enqueue(ctx, userID, a.Following)
		if err != nil {
			return sum, err
		}
		sum.FollowsQueued, sum.FollowsSkippedFederation = q.Queued, q.SkippedFederation
		if q.JobID != nil {
			id := q.JobID.String()
			sum.FollowsJobID = &id
		}
		if rest := len(a.Following) - q.Queued - q.SkippedFederation; rest > 0 {
			sum.SkippedSections["following"] += rest
		}
	} else if len(a.Following) > 0 {
		sum.SkippedSections["following"] += len(a.Following)
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
