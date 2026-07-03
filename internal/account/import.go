package account

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/notification"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Import limits: an archive may come from anywhere, so per-section volumes are
// bounded defensively (over-limit entries count as skipped).
const (
	importMaxPlaylists        = 100
	importMaxItemsPerPlaylist = 500
	importMaxFollows          = 1000
	importMaxTitleLen         = 200
	importMaxDescriptionLen   = 5000
)

// ImportSummary reports what POST /api/v1/me/import re-created and what it
// skipped. Only SAFE subsets of an archive are applied (P4 import foundation):
// profile fields, playlists (items matched by video id when the video exists
// locally), follows of local channels (matched by handle), and notification
// preferences. Everything else in the archive — channels, videos, comments,
// saved videos, watch history — is reported under SkippedSections.
type ImportSummary struct {
	ProfileApplied bool `json:"profile_applied"`

	PlaylistsCreated     int `json:"playlists_created"`
	PlaylistItemsAdded   int `json:"playlist_items_added"`
	PlaylistItemsSkipped int `json:"playlist_items_skipped"`

	FollowsCreated int `json:"follows_created"`
	FollowsSkipped int `json:"follows_skipped"`

	NotificationPrefsApplied int `json:"notification_prefs_applied"`
	NotificationPrefsSkipped int `json:"notification_prefs_skipped"`

	// SkippedSections counts archive entries that are NOT importable in v1,
	// keyed by section name (channels, videos, comments, saved_videos,
	// watch_history, playlists).
	SkippedSections map[string]int `json:"skipped_sections"`
}

// ParseArchive decodes raw JSON into an Archive and validates the format
// marker. ErrInvalidArchive when it is not a vidra account archive (or a
// version this build does not know).
func ParseArchive(raw []byte) (Archive, error) {
	var a Archive
	if err := json.Unmarshal(raw, &a); err != nil {
		return Archive{}, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if a.Meta.Version != ArchiveVersion {
		return Archive{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidArchive, a.Meta.Version)
	}
	return a, nil
}

// ImportArchive re-creates the safe subsets of archive for userID and reports
// everything else as skipped. It is additive and idempotent-ish: re-importing
// the same archive re-applies profile/prefs (no-ops), re-follows (no-ops via
// the idempotent follow), and creates duplicate playlists (playlists have no
// natural key — the caller decides whether to re-import).
func (s *Service) ImportArchive(ctx context.Context, userID uuid.UUID, archive Archive) (ImportSummary, error) {
	sum := ImportSummary{SkippedSections: map[string]int{}}

	// Profile fields (display_name, bio). Username/email/role are identity and
	// never imported.
	displayName := truncate(archive.Profile.DisplayName, importMaxTitleLen)
	bio := truncate(archive.Profile.Bio, importMaxDescriptionLen)
	if displayName != "" || bio != "" {
		var dn, b *string
		if displayName != "" {
			dn = &displayName
		}
		if bio != "" {
			b = &bio
		}
		if _, err := s.repo.UpdateUserProfile(ctx, sqlcgen.UpdateUserProfileParams{
			ID: userID, DisplayName: dn, Bio: b,
		}); err != nil {
			return sum, fmt.Errorf("account: import profile: %w", err)
		}
		sum.ProfileApplied = true
	}

	// Notification preferences (known types only).
	known := map[string]bool{}
	for _, t := range notification.KnownTypes() {
		known[t] = true
	}
	for _, p := range archive.NotificationPrefs {
		if !known[p.Type] {
			sum.NotificationPrefsSkipped++
			continue
		}
		if err := s.repo.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{
			UserID: userID, Type: p.Type, Enabled: p.Enabled,
		}); err != nil {
			return sum, fmt.Errorf("account: import prefs: %w", err)
		}
		sum.NotificationPrefsApplied++
	}

	// Playlists: re-created under the importing user; items matched by video id
	// when the video exists locally, otherwise counted skipped.
	for i, pl := range archive.Playlists {
		if i >= importMaxPlaylists {
			sum.SkippedSections["playlists"]++
			continue
		}
		title := strings.TrimSpace(truncate(pl.Title, importMaxTitleLen))
		if title == "" {
			sum.SkippedSections["playlists"]++
			continue
		}
		visibility := pl.Visibility
		switch visibility {
		case "public", "unlisted", "private":
		default:
			visibility = "private" // unknown visibility fails safe
		}
		created, err := s.repo.CreatePlaylist(ctx, sqlcgen.CreatePlaylistParams{
			OwnerID:     userID,
			Title:       title,
			Description: truncate(pl.Description, importMaxDescriptionLen),
			Visibility:  visibility,
		})
		if err != nil {
			return sum, fmt.Errorf("account: import playlist: %w", err)
		}
		sum.PlaylistsCreated++
		for j, rawID := range pl.VideoIDs {
			if j >= importMaxItemsPerPlaylist {
				sum.PlaylistItemsSkipped++
				continue
			}
			vid, err := uuid.Parse(rawID)
			if err != nil {
				sum.PlaylistItemsSkipped++
				continue
			}
			if _, err := s.repo.GetVideoByID(ctx, vid); err != nil {
				sum.PlaylistItemsSkipped++ // video not present locally
				continue
			}
			if err := s.repo.AddPlaylistItem(ctx, sqlcgen.AddPlaylistItemParams{
				PlaylistID: created.ID, VideoID: vid,
			}); err != nil {
				return sum, fmt.Errorf("account: import playlist item: %w", err)
			}
			sum.PlaylistItemsAdded++
		}
	}

	// Follows of LOCAL channels, matched by handle. Remote/unknown handles are
	// skipped (re-following remote channels needs the federation flow).
	for i, f := range archive.Follows {
		if i >= importMaxFollows || strings.TrimSpace(f.ChannelHandle) == "" || strings.Contains(f.ChannelHandle, "@") {
			sum.FollowsSkipped++
			continue
		}
		ch, err := s.repo.GetChannelByHandle(ctx, f.ChannelHandle)
		if err != nil {
			sum.FollowsSkipped++
			continue
		}
		if _, err := s.repo.FollowChannel(ctx, sqlcgen.FollowChannelParams{
			FollowerID: userID, ChannelID: ch.ID,
		}); err != nil {
			return sum, fmt.Errorf("account: import follow: %w", err)
		}
		sum.FollowsCreated++
	}

	// Everything else is out of scope for the v1 import foundation.
	sum.SkippedSections["channels"] += len(archive.Channels)
	sum.SkippedSections["videos"] += len(archive.Videos)
	sum.SkippedSections["comments"] += len(archive.Comments)
	sum.SkippedSections["saved_videos"] += len(archive.SavedVideos)
	sum.SkippedSections["watch_history"] += len(archive.WatchHistory)

	return sum, nil
}

// truncate bounds a string to max bytes (import payloads are untrusted).
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
