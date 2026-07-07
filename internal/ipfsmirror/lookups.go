package ipfsmirror

import (
	"context"
	"errors"

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
