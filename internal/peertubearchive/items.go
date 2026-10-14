package peertubearchive

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxItemsPerImport bounds the lookups one request performs for playlist
// items (each costs up to three point reads); items past it are skipped, not
// queued, so the synchronous request stays bounded.
const maxItemsPerImport = 2000

// Videos resolves an exported element to a video row already in the database.
// It never touches the network. *video.Service satisfies it.
type Videos interface {
	IDByLegacyUUID(ctx context.Context, legacy uuid.UUID) (uuid.UUID, error)
	IDByShortCode(ctx context.Context, code string) (uuid.UUID, error)
	GetByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
}

// Blocks reports a moderator block on a video. *moderation.Service satisfies it.
type Blocks interface {
	IsBlocked(ctx context.Context, videoID uuid.UUID) (bool, error)
}

// Reasons an element was not added; reported under skipped_sections.
const (
	// reasonNotAddable is ONE reason for "no such video" and "held but not
	// public+published, or blocked", so the summary cannot be used to probe
	// whether a non-public video exists (POST /playlists/{id}/items answers one 404).
	reasonNotAddable = "playlist_items_not_addable"
	reasonOverCap    = "playlist_items_over_cap"
)

// parseVideoRef extracts the identifier a video URL carries: a uuid from
// /videos/watch/{uuid} or /videos/{uuid} (the form PeerTube exports, which is
// also this instance's own legacy form and an imported video's source uuid), or
// an 11-character short code from /v/{code}. The host is NOT checked: the uuid
// form is resolved by identifier alone, exactly as GET /videos/resolve does.
// /w/{shortUUID} needs PeerTube's own base58 ordering and is not decoded.
func parseVideoRef(raw string) (id uuid.UUID, code string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return uuid.Nil, "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "videos" && parts[1] == "watch":
		id, err = uuid.Parse(parts[2])
	case len(parts) == 2 && parts[0] == "videos":
		id, err = uuid.Parse(parts[1])
	case len(parts) == 2 && parts[0] == "v":
		return uuid.Nil, parts[1], true
	default:
		return uuid.Nil, "", false
	}
	return id, "", err == nil
}

// resolveItem returns the addable video id for one exported element, or the
// skip reason. Addable mirrors POST /playlists/{id}/items: public, published,
// not moderator-blocked (no staff exemption: this is the owner's import).
func (im *Importer) resolveItem(ctx context.Context, raw string) (uuid.UUID, string, error) {
	id, code, ok := parseVideoRef(raw)
	if !ok {
		return uuid.Nil, reasonNotAddable, nil
	}
	var vid uuid.UUID
	var err error
	if code != "" {
		vid, err = im.Videos.IDByShortCode(ctx, code)
	} else {
		vid, err = im.Videos.IDByLegacyUUID(ctx, id)
	}
	if err != nil || vid == uuid.Nil {
		// A miss. A cached remote_videos row is deliberately not consulted:
		// playlist_items.video_id references videos(id) only.
		return uuid.Nil, reasonNotAddable, nil
	}
	v, err := im.Videos.GetByID(ctx, vid)
	if err != nil || v.State != "published" || v.Privacy != "public" {
		return uuid.Nil, reasonNotAddable, nil
	}
	if im.Blocks != nil {
		blocked, err := im.Blocks.IsBlocked(ctx, vid)
		if err != nil {
			return uuid.Nil, "", err
		}
		if blocked {
			return uuid.Nil, reasonNotAddable, nil
		}
	}
	return vid, "", nil
}

// addItems appends the playlist's resolvable videos through the playlist
// service (ownership checked there; AddItem is idempotent, so a re-import
// re-confirms rather than duplicates). budget is the import-wide lookup allowance.
func (im *Importer) addItems(ctx context.Context, userID, playlistID uuid.UUID, p Playlist, budget *int, sum *Summary) error {
	seen := map[uuid.UUID]bool{}
	for _, raw := range p.Items {
		if *budget <= 0 {
			sum.PlaylistItemsSkipped++
			sum.SkippedSections[reasonOverCap]++
			continue
		}
		*budget--
		vid, reason, err := im.resolveItem(ctx, raw)
		if err != nil {
			return err
		}
		if reason != "" {
			sum.PlaylistItemsSkipped++
			sum.SkippedSections[reason]++
			continue
		}
		if seen[vid] { // the same video listed twice: one item
			continue
		}
		seen[vid] = true
		if err := im.Playlists.AddItem(ctx, userID, playlistID, vid); err != nil {
			return err
		}
		sum.PlaylistItemsAdded++
	}
	// Elements this reader did not retain (past the per-playlist cap, or an
	// over-long URL) were never looked at.
	if lost := p.Elements - len(p.Items); lost > 0 {
		sum.PlaylistItemsSkipped += lost
		sum.SkippedSections[reasonOverCap] += lost
	}
	return nil
}

// importItems adds a playlist's items, or counts them all skipped when no
// resolver is wired.
func (im *Importer) importItems(ctx context.Context, userID, playlistID uuid.UUID, p Playlist, budget *int, sum *Summary) error {
	if p.Elements == 0 {
		return nil
	}
	if im.Videos == nil {
		sum.PlaylistItemsSkipped += p.Elements
		sum.SkippedSections[reasonNotAddable] += p.Elements
		return nil
	}
	return im.addItems(ctx, userID, playlistID, p, budget, sum)
}
