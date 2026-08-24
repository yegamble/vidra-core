package peertubeimport

import (
	"fmt"
	"path"
	"strings"
	"syscall"

	"github.com/vidra/vidra-core/internal/storage"
)

// SourceStorageConfig locates the source PeerTube instance's media. Reads are
// path-traversal-guarded (the storage.Backend key rules reject any ".." segment)
// and the backend is used READ-ONLY. For S3 the endpoint is operator-configured
// (trusted, like the primary media store) — not attacker-controlled input — so
// no SSRF surface is opened by reading it; the importer never follows arbitrary
// URLs from the source database.
type SourceStorageConfig struct {
	Backend string // "local" | "s3"
	// Local.
	LocalRoot string
	// S3.
	S3Endpoint       string
	S3Bucket         string
	S3AccessKey      string // SECRET; never logged
	S3SecretKey      string // SECRET; never logged
	S3Region         string
	S3UseSSL         bool
	S3ForcePathStyle bool
}

// PeerTube's default object buckets (subdirectories under the media root). The
// verified 5.x–8.x layout. Documented in docs/peertube-migration.md; operators
// on an older layout mount their tree to match.
const (
	ptWebVideosDir = "web-videos"
	ptThumbnailDir = "thumbnails"
	ptCaptionDir   = "captions"
	ptHLSDir       = "streaming-playlists/hls"
)

// OpenSourceStorage builds a read-only media backend for the source instance.
// It reuses the same traversal-guarded local/S3 backends the primary store uses;
// the importer only ever calls Open on it.
func OpenSourceStorage(cfg SourceStorageConfig) (storage.Backend, error) {
	switch cfg.Backend {
	case "", "local":
		if strings.TrimSpace(cfg.LocalRoot) == "" {
			return nil, fmt.Errorf("peertubeimport: source storage local root is empty")
		}
		return storage.NewLocal(cfg.LocalRoot)
	case "s3":
		return storage.NewS3(storage.S3Config{
			Endpoint:       cfg.S3Endpoint,
			Bucket:         cfg.S3Bucket,
			AccessKey:      cfg.S3AccessKey,
			SecretKey:      cfg.S3SecretKey,
			Region:         cfg.S3Region,
			UseSSL:         cfg.S3UseSSL,
			ForcePathStyle: cfg.S3ForcePathStyle,
		})
	default:
		return nil, fmt.Errorf("peertubeimport: unsupported source storage backend %q", cfg.Backend)
	}
}

// sourceWebVideoKey / sourceThumbnailKey / sourceCaptionKey map a PeerTube stored
// filename to the object key under the source media root. path.Base strips any
// directory the source recorded, so a malicious filename cannot escape its bucket
// (belt-and-braces on top of the backend's own key validation).
func sourceWebVideoKey(filename string) string {
	return ptWebVideosDir + "/" + path.Base(filename)
}
func sourceThumbnailKey(filename string) string {
	return ptThumbnailDir + "/" + path.Base(filename)
}
func sourceCaptionKey(filename string) string {
	return ptCaptionDir + "/" + path.Base(filename)
}
func sourceHLSKey(videoUUID, filename string) string {
	return sourceHLSDir(videoUUID) + "/" + path.Base(filename)
}

// sourceHLSDir is the directory holding a video's whole HLS tree on the source:
// its master playlist, every variant playlist and every segment. PeerTube keeps
// all rungs of a ladder side by side in it rather than in a directory per rung,
// so every imported rendition of a video shares this prefix. It is recorded on
// video_renditions.key_prefix — the honest answer to "where does this rung's
// playlist live", even though it is not the per-rung directory a Vidra-produced
// rendition points at.
func sourceHLSDir(videoUUID string) string {
	return ptHLSDir + "/" + path.Base(videoUUID)
}

// File-type allowlists applied to what is copied from the source. A source row
// pointing at an unexpected extension is skipped rather than blindly copied.
var (
	allowedVideoExt = map[string]bool{
		".mp4": true, ".webm": true, ".mkv": true, ".mov": true, ".m4v": true, ".ogv": true, ".avi": true,
	}
	allowedImageExt = map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
	}
	allowedCaptionExt = map[string]bool{
		".vtt": true, ".srt": true,
	}
)

// extOf returns the lowercased extension (with dot) of a filename.
func extOf(name string) string {
	return strings.ToLower(path.Ext(name))
}

// maxSourceFileBytes bounds a single copied media object (defence against a
// hostile/broken source claiming an absurd size). 16 GiB comfortably exceeds any
// realistic single web-video while capping runaway reads.
const maxSourceFileBytes int64 = 16 << 30

// freeDiskBytes returns the bytes available to a non-root user at path's
// filesystem. Unix-only (darwin + linux, which is all Vidra targets). Used by
// preflight so a local-destination import fails fast when disk is short rather
// than part-way through.
func freeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
