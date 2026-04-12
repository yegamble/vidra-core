package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaths_AvatarsAndWebVideos(t *testing.T) {
	root := "./storage"
	p := NewPaths(root)

	// Avatars
	assert.Equal(t, filepath.Join(root, "avatars"), p.AvatarsDir())
	assert.Equal(t, filepath.Join(root, "avatars", "file123.png"), p.AvatarFilePath("file123", ".png"))
	assert.Equal(t, filepath.Join(root, "avatars", "file123.webp"), p.AvatarWebPPath("file123"))

	// Web videos
	assert.Equal(t, filepath.Join(root, "web-videos"), p.WebVideosDir())
	assert.Equal(t, filepath.Join(root, "web-videos", "vid001.mp4"), p.WebVideoFilePath("vid001", ".mp4"))
}

func TestPaths_UploadTempDirs(t *testing.T) {
	root := "./storage"
	p := NewPaths(root)

	sid := "sess-abc"
	assert.Equal(t, filepath.Join(root, "cache", "uploads", sid), p.UploadTempDir(sid))
	assert.Equal(t, filepath.Join(root, "cache", "uploads", sid, "chunks"), p.UploadTempChunksDir(sid))
}

func TestPaths_ThumbnailsAndPreviews(t *testing.T) {
	root := "./storage"
	p := NewPaths(root)
	vid := "video-xyz"

	assert.Equal(t, filepath.Join(root, "thumbnails"), p.ThumbnailsDir())
	assert.Equal(t, filepath.Join(root, "thumbnails", vid+"_thumb.jpg"), p.ThumbnailPath(vid))

	assert.Equal(t, filepath.Join(root, "previews"), p.PreviewsDir())
	assert.Equal(t, filepath.Join(root, "previews", vid+"_preview.webp"), p.PreviewPath(vid))
}

func TestPaths_CaptionsPaths(t *testing.T) {
	root := "./storage"
	p := NewPaths(root)
	vid := "video-caption-1"

	assert.Equal(t, filepath.Join(root, "captions"), p.CaptionsRootDir())
	assert.Equal(t, filepath.Join(root, "captions", vid), p.VideoCaptionsDir(vid))
	assert.Equal(t, filepath.Join(root, "captions", vid, "en.vtt"), p.CaptionFilePath(vid, "en", "vtt"))
}

func TestPaths_HLSPathsAndRel(t *testing.T) {
	root := "./storage"
	p := NewPaths(root)
	vid := "v123"

	// Base and per-video HLS directories
	assert.Equal(t, filepath.Join(root, "streaming-playlists", "hls"), p.HLSRootDir())
	assert.Equal(t, filepath.Join(root, "streaming-playlists", "hls", vid), p.HLSVideoDir(vid))

	// Rel path inside HLS root (master playlist)
	local := filepath.Join(p.HLSVideoDir(vid), "master.m3u8")
	rel, ok := p.HLSRelPath(local)
	require.True(t, ok)
	assert.Equal(t, vid+"/master.m3u8", rel)

	// Rel path for nested variant
	local2 := filepath.Join(p.HLSRootDir(), vid, "720p", "stream.m3u8")
	rel2, ok2 := p.HLSRelPath(local2)
	require.True(t, ok2)
	assert.Equal(t, vid+"/720p/stream.m3u8", rel2)

	// Outside HLS root should not be accepted
	outside := filepath.Clean(filepath.Join(p.Root, "..", "not-hls", "file.m3u8"))
	_, ok3 := p.HLSRelPath(outside)
	require.False(t, ok3)
}

func TestPaths_WithAbsoluteRoot(t *testing.T) {
	absRoot := t.TempDir()
	p := NewPaths(absRoot)

	// Basic dirs resolve under absolute root
	if got, want := p.AvatarsDir(), filepath.Join(absRoot, "avatars"); got != want {
		t.Fatalf("avatars dir mismatch: got %q want %q", got, want)
	}
	if got, want := p.HLSRootDir(), filepath.Join(absRoot, "streaming-playlists", "hls"); got != want {
		t.Fatalf("hls root mismatch: got %q want %q", got, want)
	}

	// HLSRelPath with absolute local path inside the HLS root
	vid := "vabs"
	local := filepath.Join(p.HLSVideoDir(vid), "master.m3u8")
	rel, ok := p.HLSRelPath(local)
	require.True(t, ok)
	assert.Equal(t, vid+"/master.m3u8", rel)
}

func TestPaths_Negative_ExtensionsStrangeButNoSeparators(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	// Empty extension
	got := p.AvatarFilePath("file", "")
	assert.Equal(t, filepath.Join(root, "avatars", "file"), got)
	// Very long extension (no path separators)
	long := "." + string(make([]byte, 64))
	got2 := p.AvatarFilePath("file", long)
	assert.Equal(t, filepath.Join(root, "avatars", "file"+long), got2)
	// Whitespace extension
	got3 := p.AvatarFilePath("file", "   ")
	assert.Equal(t, filepath.Join(root, "avatars", "file   "), got3)
}

// Benchmarks
func BenchmarkPaths_AvatarFilePath(b *testing.B) {
	p := NewPaths("/var/lib/app/storage")
	for i := 0; i < b.N; i++ {
		_ = p.AvatarFilePath("abcdef123456", ".png")
	}
}

func BenchmarkPaths_HLSRelPath(b *testing.B) {
	p := NewPaths("/var/lib/app/storage")
	local := filepath.Join(p.HLSVideoDir("v1"), "720p", "stream.m3u8")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.HLSRelPath(local)
	}
}

func TestThumbnailPathWithExt(t *testing.T) {
	p := NewPaths("/storage")
	assert.Equal(t, filepath.Join("/storage", "thumbnails", "vid1_thumb.png"), p.ThumbnailPathWithExt("vid1", "png"))
	assert.Equal(t, filepath.Join("/storage", "thumbnails", "vid1_thumb.webp"), p.ThumbnailPathWithExt("vid1", ".webp"))
	assert.Equal(t, filepath.Join("/storage", "thumbnails", "vid1_thumb.jpg"), p.ThumbnailPathWithExt("vid1", "jpg"))
}

func TestIsValidThumbnailMIME(t *testing.T) {
	assert.True(t, IsValidThumbnailMIME("image/jpeg"))
	assert.True(t, IsValidThumbnailMIME("image/png"))
	assert.True(t, IsValidThumbnailMIME("image/webp"))
	assert.False(t, IsValidThumbnailMIME("image/gif"))
	assert.False(t, IsValidThumbnailMIME("text/plain"))
}

func TestThumbnailExtForMIME(t *testing.T) {
	assert.Equal(t, "jpg", ThumbnailExtForMIME("image/jpeg"))
	assert.Equal(t, "png", ThumbnailExtForMIME("image/png"))
	assert.Equal(t, "webp", ThumbnailExtForMIME("image/webp"))
	assert.Equal(t, "jpg", ThumbnailExtForMIME("unknown"))
}

func BenchmarkPaths_UploadTempChunksDir(b *testing.B) {
	p := NewPaths("/var/lib/app/storage")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.UploadTempChunksDir("session-abcdef")
	}
}
