package storage

import (
	"path/filepath"
	"strings"
)

// Paths provides helper methods for constructing well-known
// filesystem locations used by the application. It is intentionally
// small and only implements the pieces required by the tests.
type Paths struct {
    Root string
}

// NewPaths returns a Paths helper rooted at the provided directory.
func NewPaths(root string) Paths { return Paths{Root: root} }

// UploadTempDir returns the directory for temporary upload data of a session.
func (p Paths) UploadTempDir(sessionID string) string { return filepath.Join(p.Root, "cache", "uploads", sessionID) }

// UploadTempChunksDir returns the directory where individual chunk files are stored.
func (p Paths) UploadTempChunksDir(sessionID string) string { return filepath.Join(p.UploadTempDir(sessionID), "chunks") }

// WebVideosDir returns the directory that stores completed uploaded videos.
func (p Paths) WebVideosDir() string { return filepath.Join(p.Root, "web-videos") }

// HLSRootDir returns the root directory for HLS encoded output.
func (p Paths) HLSRootDir() string { return filepath.Join(p.Root, "streaming-playlists", "hls") }

// HLSVideoDir returns the directory for a given video's HLS assets.
func (p Paths) HLSVideoDir(videoID string) string { return filepath.Join(p.HLSRootDir(), videoID) }

// HLSRelPath converts an absolute local path under the HLS root into
// a relative path suitable for serving over HTTP. It returns false if
// the path is outside of the HLS root.
func (p Paths) HLSRelPath(localPath string) (string, bool) {
    rel, err := filepath.Rel(p.HLSRootDir(), localPath)
    if err != nil { return "", false }
    if strings.HasPrefix(rel, "..") { return "", false }
    return filepath.ToSlash(rel), true
}

// AvatarsDir returns the directory where avatar images are stored.
func (p Paths) AvatarsDir() string { return filepath.Join(p.Root, "avatars") }

// AvatarFilePath returns the path for an uploaded avatar file with the given extension.
func (p Paths) AvatarFilePath(fileID, ext string) string { return filepath.Join(p.AvatarsDir(), fileID+ext) }

// AvatarWebPPath returns the path for a generated WebP version of an avatar.
func (p Paths) AvatarWebPPath(fileID string) string { return filepath.Join(p.AvatarsDir(), fileID+".webp") }

// ThumbnailPath returns the path for a video's thumbnail image.
func (p Paths) ThumbnailsDir() string { return filepath.Join(p.Root, "thumbnails") }
func (p Paths) ThumbnailPath(videoID string) string {
    return filepath.Join(p.ThumbnailsDir(), videoID+"_thumb.jpg")
}

// PreviewPath returns the path for a video's preview animation.
func (p Paths) PreviewsDir() string { return filepath.Join(p.Root, "previews") }
func (p Paths) PreviewPath(videoID string) string {
    return filepath.Join(p.PreviewsDir(), videoID+"_preview.webp")
}

// WebVideoFilePath returns the final assembled upload file for a video.
func (p Paths) WebVideoFilePath(videoID, ext string) string { return filepath.Join(p.WebVideosDir(), videoID+ext) }
