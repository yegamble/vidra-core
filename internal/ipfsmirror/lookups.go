package ipfsmirror

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// image kind values (mirror profileimage.KindAvatar / KindBanner; duplicated as
// literals here to avoid an import cycle).
const (
	kindAvatar = "avatar"
	kindBanner = "banner"
)

// sqlQueries is the read surface SQLLookups needs; *sqlcgen.Queries satisfies it.
type sqlQueries interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
	GetChannelByID(ctx context.Context, id uuid.UUID) (sqlcgen.Channel, error)
	GetVideoByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error)
	IsVideoBlocked(ctx context.Context, videoID uuid.UUID) (bool, error)
	GetStreamingPlaylist(ctx context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error)
	ListVideoIDsByOwner(ctx context.Context, ownerID uuid.UUID) ([]uuid.UUID, error)
	ListVideoFiles(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.VideoFile, error)
	ListCaptionsByVideo(ctx context.Context, videoID uuid.UUID) ([]sqlcgen.Caption, error)
	GetPlaylistByID(ctx context.Context, id uuid.UUID) (sqlcgen.GetPlaylistByIDRow, error)
	GetUserImage(ctx context.Context, arg sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error)
	GetChannelImage(ctx context.Context, arg sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error)
	ListChannelsByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error)
	// Backfill catalog scans (P19.6): the bulk enumeration the one-shot admin
	// reconcile uses (Catalog interface, backfill.go).
	ListBackfillVideoObjects(ctx context.Context) ([]sqlcgen.ListBackfillVideoObjectsRow, error)
	ListBackfillIdentityImages(ctx context.Context) ([]sqlcgen.ListBackfillIdentityImagesRow, error)
	ListBackfillPlaylistCovers(ctx context.Context) ([]sqlcgen.ListBackfillPlaylistCoversRow, error)
}

// SQLLookups is the production Lookups backed by sqlc queries. A missing row
// (pgx.ErrNoRows) is reported as ok=false, not an error, so a deleted parent is a
// clean skip rather than a failure that blocks the write path.
type SQLLookups struct{ q sqlQueries }

var _ Lookups = (*SQLLookups)(nil)

// NewSQLLookups builds the SQL-backed Lookups (pass db.Queries()).
func NewSQLLookups(q sqlQueries) *SQLLookups { return &SQLLookups{q: q} }

func missing(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func (l *SQLLookups) VideoVisibility(ctx context.Context, videoID uuid.UUID) (privacy, state string, ownerID uuid.UUID, ok bool, err error) {
	v, err := l.q.GetVideoByID(ctx, videoID)
	if err != nil {
		if missing(err) {
			return "", "", uuid.Nil, false, nil
		}
		return "", "", uuid.Nil, false, err
	}
	return v.Privacy, v.State, v.OwnerID, true, nil
}

// VideoBlocked reports whether a moderator block currently stands on the video.
//
// A missing video is NOT blocked rather than an error: the caller has already
// established the video exists (VideoVisibility ran first), and a race that
// deletes it in between is the delete path's business, not the fence's.
func (l *SQLLookups) VideoBlocked(ctx context.Context, videoID uuid.UUID) (bool, error) {
	blocked, err := l.q.IsVideoBlocked(ctx, videoID)
	if err != nil {
		if missing(err) {
			return false, nil
		}
		return false, err
	}
	return blocked, nil
}

// OwnerVideoIDs lists every video (any privacy/state) owned by the user, resolved
// via their channels (videos → channels.owner_id). The unlisted-toggle
// re-evaluation runs the per-video fence on each; a user with no videos is a clean
// empty result, not an error.
func (l *SQLLookups) OwnerVideoIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := l.q.ListVideoIDsByOwner(ctx, userID)
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, err
	}
	return ids, nil
}

// VideoHLSTree returns the promoted transcode generation's directory: the
// directory of streaming_playlists.master_key.
//
// The master key IS the promotion record — transcode.storeResult swapping it to
// the new generation is what makes a re-transcode visible — so its directory is
// by construction the one generation whose master.m3u8 players are being pointed
// at, whichever rN that happens to be and however many other generations are
// still sitting in the object store. The same derivation is what serves HLS
// children (httpapi.serveHLSChild), what the CDN purge lists a tree with
// (httpapi.videoEdgePurgeTree) and what the blob-reference check walks
// (blobverify); reading it out of anywhere else — the transcode counter, the key
// grammar — would be a second opinion that can disagree with what is served.
//
// A PeerTube reference-mode import keeps the source instance's layout
// (streaming-playlists/hls/<source-uuid>/), which this handles for free and the
// stable per-video prefix never could: that tree is what the video actually
// serves, so it is what the mirror should carry.
func (l *SQLLookups) VideoHLSTree(ctx context.Context, videoID uuid.UUID) (string, bool, error) {
	sp, err := l.q.GetStreamingPlaylist(ctx, videoID)
	if err != nil {
		if missing(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if sp.MasterKey == "" || !strings.Contains(sp.MasterKey, "/") {
		// No master ('' after a dead-lettered transcode) or a bare filename:
		// there is no directory to mirror.
		return "", false, nil
	}
	dir := path.Dir(sp.MasterKey)
	if dir == "." || dir == "/" {
		return "", false, nil
	}
	return dir, true, nil
}

func (l *SQLLookups) VideoFiles(ctx context.Context, videoID uuid.UUID) ([]VideoFileRef, error) {
	files, err := l.q.ListVideoFiles(ctx, videoID)
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, err
	}
	refs := make([]VideoFileRef, 0, len(files))
	for _, f := range files {
		refs = append(refs, VideoFileRef{Kind: f.Kind, StorageKey: f.StorageKey})
	}
	return refs, nil
}

func (l *SQLLookups) VideoCaptionKeys(ctx context.Context, videoID uuid.UUID) ([]string, error) {
	caps, err := l.q.ListCaptionsByVideo(ctx, videoID)
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(caps))
	for _, c := range caps {
		keys = append(keys, c.StorageKey)
	}
	return keys, nil
}

func (l *SQLLookups) UserFlags(ctx context.Context, userID uuid.UUID) (active, unlisted bool, ok bool, err error) {
	u, err := l.q.GetUserByID(ctx, userID)
	if err != nil {
		if missing(err) {
			return false, false, false, nil
		}
		return false, false, false, err
	}
	// A soft-deleted account is not active for mirroring even if is_active lags.
	active = u.IsActive && !u.DeletedAt.Valid
	return active, u.Unlisted, true, nil
}

func (l *SQLLookups) ChannelOwner(ctx context.Context, channelID uuid.UUID) (ownerID uuid.UUID, ok bool, err error) {
	ch, err := l.q.GetChannelByID(ctx, channelID)
	if err != nil {
		if missing(err) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return ch.OwnerID, true, nil
}

func (l *SQLLookups) PlaylistCover(ctx context.Context, playlistID uuid.UUID) (visibility, objectKey string, hasCover bool, err error) {
	p, err := l.q.GetPlaylistByID(ctx, playlistID)
	if err != nil {
		if missing(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if p.ThumbnailExt == nil || *p.ThumbnailExt == "" {
		return p.Visibility, "", false, nil
	}
	return p.Visibility, media.PlaylistThumbnailKey(p.ID, *p.ThumbnailExt), true, nil
}

// OwnerImageRefs returns the owner's own avatar/banner plus each of their
// channels' avatar/banner (whichever are set). Missing images are skipped.
func (l *SQLLookups) OwnerImageRefs(ctx context.Context, userID uuid.UUID) ([]ImageRef, error) {
	var refs []ImageRef

	for _, kc := range []struct {
		kind  string
		class MediaClass
	}{{kindAvatar, ClassUserAvatar}, {kindBanner, ClassUserBanner}} {
		img, err := l.q.GetUserImage(ctx, sqlcgen.GetUserImageParams{UserID: userID, Kind: kc.kind})
		if err != nil {
			if missing(err) {
				continue
			}
			return nil, err
		}
		refs = append(refs, ImageRef{Class: kc.class, ObjectKey: img.StorageKey, OwnerUserID: userID})
	}

	channels, err := l.q.ListChannelsByOwner(ctx, userID)
	if err != nil && !missing(err) {
		return nil, err
	}
	for _, ch := range channels {
		for _, kc := range []struct {
			kind  string
			class MediaClass
		}{{kindAvatar, ClassChannelAvatar}, {kindBanner, ClassChannelBanner}} {
			img, err := l.q.GetChannelImage(ctx, sqlcgen.GetChannelImageParams{ChannelID: ch.ID, Kind: kc.kind})
			if err != nil {
				if missing(err) {
					continue
				}
				return nil, err
			}
			refs = append(refs, ImageRef{Class: kc.class, ObjectKey: img.StorageKey, OwnerUserID: userID})
		}
	}
	return refs, nil
}
