package account

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ArchiveVersion is the vidra account-archive format version. Bump it on any
// breaking shape change; the importer refuses versions it does not know.
const ArchiveVersion = 1

// Archive is the vidra account export: the user's own data as one JSON
// document. It deliberately contains NO password hash, NO tokens/sessions, and
// no other credential material — the export-shape test asserts every key
// against the observability sensitive-key denylist.
//
// Media files are NOT bundled in v1: each video entry instead carries
// original_download_url, the (auth-gated) API URL of its original file.
//
// The same shape is what POST /api/v1/me/import accepts; only the safe subsets
// documented on ImportSummary are re-created.
type Archive struct {
	Meta              ArchiveMeta       `json:"vidra_export"`
	Profile           ArchiveProfile    `json:"profile"`
	NotificationPrefs []ArchivePref     `json:"notification_prefs"`
	Channels          []ArchiveChannel  `json:"channels"`
	Videos            []ArchiveVideo    `json:"videos"`
	Playlists         []ArchivePlaylist `json:"playlists"`
	Comments          []ArchiveComment  `json:"comments"`
	Follows           []ArchiveFollow   `json:"follows"`
	SavedVideos       []ArchiveSaved    `json:"saved_videos"`
	WatchHistory      []ArchiveHistory  `json:"watch_history"`
}

// ArchiveMeta identifies the archive format and origin.
type ArchiveMeta struct {
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Instance    string    `json:"instance,omitempty"`
}

// ArchiveProfile is the account's own profile data (never the password hash).
type ArchiveProfile struct {
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	DisplayName   string    `json:"display_name"`
	Bio           string    `json:"bio"`
	Unlisted      bool      `json:"unlisted"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
}

// ArchivePref is one notification-delivery preference.
type ArchivePref struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
}

// ArchiveChannel is one owned channel.
type ArchiveChannel struct {
	ID          string    `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// ArchiveVideo is one owned video's metadata (taxonomy + tags included).
// Media is not bundled; OriginalDownloadURL is where the original can be
// fetched while the account (and video) still exist.
type ArchiveVideo struct {
	ID                  string     `json:"id"`
	ChannelHandle       string     `json:"channel_handle"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	Privacy             string     `json:"privacy"`
	State               string     `json:"state"`
	Category            *string    `json:"category"`
	Language            *string    `json:"language"`
	License             *string    `json:"license"`
	Tags                []string   `json:"tags"`
	PublishAt           *time.Time `json:"publish_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	OriginalDownloadURL string     `json:"original_download_url"`
}

// ArchivePlaylist is one playlist with its item video ids in order.
type ArchivePlaylist struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	VideoIDs    []string  `json:"video_ids"`
	CreatedAt   time.Time `json:"created_at"`
}

// ArchiveComment is one of the account's own comments.
type ArchiveComment struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ArchiveFollow is one followed (local) channel.
type ArchiveFollow struct {
	ChannelID     string    `json:"channel_id"`
	ChannelHandle string    `json:"channel_handle"`
	FollowedAt    time.Time `json:"followed_at"`
}

// ArchiveSaved is one watch-later entry.
type ArchiveSaved struct {
	VideoID string    `json:"video_id"`
	SavedAt time.Time `json:"saved_at"`
}

// ArchiveHistory is one watch-history entry with the resume position.
type ArchiveHistory struct {
	VideoID         string    `json:"video_id"`
	PositionSeconds int32     `json:"position_seconds"`
	WatchedAt       time.Time `json:"watched_at"`
}

// buildArchive assembles the export archive for one user from the repository.
func (s *Service) buildArchive(ctx context.Context, user sqlcgen.User) (Archive, error) {
	a := Archive{
		Meta: ArchiveMeta{
			Version:     ArchiveVersion,
			GeneratedAt: s.now().UTC(),
			Instance:    s.baseURL,
		},
		Profile: ArchiveProfile{
			Username:      user.Username,
			Email:         user.Email,
			DisplayName:   user.DisplayName,
			Bio:           user.Bio,
			Unlisted:      user.Unlisted,
			EmailVerified: user.EmailVerified,
			CreatedAt:     user.CreatedAt,
		},
		// Initialise every section to an empty (not null) list so archives are
		// shape-stable regardless of how much data the account has.
		NotificationPrefs: []ArchivePref{},
		Channels:          []ArchiveChannel{},
		Videos:            []ArchiveVideo{},
		Playlists:         []ArchivePlaylist{},
		Comments:          []ArchiveComment{},
		Follows:           []ArchiveFollow{},
		SavedVideos:       []ArchiveSaved{},
		WatchHistory:      []ArchiveHistory{},
	}

	prefs, err := s.repo.ListNotificationPrefs(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export prefs: %w", err)
	}
	for _, p := range prefs {
		a.NotificationPrefs = append(a.NotificationPrefs, ArchivePref{Type: p.Type, Enabled: p.Enabled})
	}

	channels, err := s.repo.ListChannelsByOwner(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export channels: %w", err)
	}
	for _, ch := range channels {
		a.Channels = append(a.Channels, ArchiveChannel{
			ID:          ch.ID.String(),
			Handle:      ch.Handle,
			DisplayName: ch.DisplayName,
			Description: ch.Description,
			CreatedAt:   ch.CreatedAt,
		})
	}

	videos, err := s.repo.ListVideosByOwnerForExport(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export videos: %w", err)
	}
	for _, v := range videos {
		tags, err := s.repo.ListVideoTags(ctx, v.ID)
		if err != nil {
			return Archive{}, fmt.Errorf("account: export video tags: %w", err)
		}
		if tags == nil {
			tags = []string{}
		}
		a.Videos = append(a.Videos, ArchiveVideo{
			ID:            v.ID.String(),
			ChannelHandle: v.ChannelHandle,
			Title:         v.Title,
			Description:   v.Description,
			Privacy:       v.Privacy,
			State:         v.State,
			Category:      v.Category,
			Language:      v.Language,
			License:       v.License,
			Tags:          tags,
			PublishAt:     timePtr(v.PublishAt),
			CreatedAt:     v.CreatedAt,
			UpdatedAt:     v.UpdatedAt,
			// Media is not bundled in v1 — the original stays fetchable at its
			// (owner-gated) download endpoint while the account exists.
			OriginalDownloadURL: s.baseURL + "/api/v1/videos/" + v.ID.String() + "/original",
		})
	}

	playlists, err := s.repo.ListPlaylistsByOwner(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export playlists: %w", err)
	}
	for _, pl := range playlists {
		ids, err := s.repo.ListPlaylistItemVideoIDs(ctx, pl.ID)
		if err != nil {
			return Archive{}, fmt.Errorf("account: export playlist items: %w", err)
		}
		videoIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			videoIDs = append(videoIDs, id.String())
		}
		a.Playlists = append(a.Playlists, ArchivePlaylist{
			ID:          pl.ID.String(),
			Title:       pl.Title,
			Description: pl.Description,
			Visibility:  pl.Visibility,
			VideoIDs:    videoIDs,
			CreatedAt:   pl.CreatedAt,
		})
	}

	comments, err := s.repo.ListCommentsByUserForExport(ctx, pgtype.UUID{Bytes: user.ID, Valid: true})
	if err != nil {
		return Archive{}, fmt.Errorf("account: export comments: %w", err)
	}
	for _, c := range comments {
		a.Comments = append(a.Comments, ArchiveComment{
			ID:        c.ID.String(),
			VideoID:   c.VideoID.String(),
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	follows, err := s.repo.ListFollowedChannelsByUser(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export follows: %w", err)
	}
	for _, f := range follows {
		a.Follows = append(a.Follows, ArchiveFollow{
			ChannelID:     f.ChannelID.String(),
			ChannelHandle: f.Handle,
			FollowedAt:    f.CreatedAt,
		})
	}

	saved, err := s.repo.ListSavedVideosByUserForExport(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export saved: %w", err)
	}
	for _, sv := range saved {
		a.SavedVideos = append(a.SavedVideos, ArchiveSaved{VideoID: sv.VideoID.String(), SavedAt: sv.CreatedAt})
	}

	history, err := s.repo.ListWatchHistoryByUserForExport(ctx, user.ID)
	if err != nil {
		return Archive{}, fmt.Errorf("account: export history: %w", err)
	}
	for _, h := range history {
		a.WatchHistory = append(a.WatchHistory, ArchiveHistory{
			VideoID:         h.VideoID.String(),
			PositionSeconds: h.PositionSeconds,
			WatchedAt:       h.UpdatedAt,
		})
	}

	return a, nil
}

// timePtr renders a nullable pgtype.Timestamptz as *time.Time.
func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
