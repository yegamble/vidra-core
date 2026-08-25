package media

import "github.com/google/uuid"

// VideoThumbnailKey is the storage key for a video's poster image. PeerTube-
// aligned one-dir-per-kind layout (see .ralph/specs/storage-layout.md), so
// posters live under thumbnails/.
//
// The suffix is ALWAYS .jpg, even for a PNG or WebP poster: exactly one object
// per video is the point of a deterministic key, and a key that varied with the
// format would leave the previous format's object behind on every replacement.
// The real format travels on video_files.content_type instead.
//
// It is exported, and lives here rather than in the video service, because the
// PeerTube import needs to recognise this key SHAPE: a kind='thumbnail' row
// pointing anywhere else was written by an older release of the importer, and
// that is what makes it safe to replace (see entities_videoimages.go). One
// definition, so the two sides cannot drift apart.
func VideoThumbnailKey(videoID uuid.UUID) string {
	return "thumbnails/" + videoID.String() + ".jpg"
}

// PlaylistThumbnailKey is the storage key for a playlist's uploaded cover image.
// PeerTube-aligned one-dir-per-kind layout (see .ralph/specs/storage-layout.md):
// playlist-thumbnails/<playlist_id>.<ext>. ext is the bare extension without a
// leading dot (e.g. "jpg").
func PlaylistThumbnailKey(playlistID uuid.UUID, ext string) string {
	return "playlist-thumbnails/" + playlistID.String() + "." + ext
}
