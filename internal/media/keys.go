package media

import "github.com/google/uuid"

// PlaylistThumbnailKey is the storage key for a playlist's uploaded cover image.
// PeerTube-aligned one-dir-per-kind layout (see .ralph/specs/storage-layout.md):
// playlist-thumbnails/<playlist_id>.<ext>. ext is the bare extension without a
// leading dot (e.g. "jpg").
func PlaylistThumbnailKey(playlistID uuid.UUID, ext string) string {
	return "playlist-thumbnails/" + playlistID.String() + "." + ext
}
