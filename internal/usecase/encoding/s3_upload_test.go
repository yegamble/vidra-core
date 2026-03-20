package encoding

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/storage"
)

// mockStorageBackend is a simple in-memory StorageBackend for testing.
type mockStorageBackend struct {
	uploaded map[string][]byte
	baseURL  string
}

func newMockStorageBackend(baseURL string) *mockStorageBackend {
	return &mockStorageBackend{
		uploaded: make(map[string][]byte),
		baseURL:  baseURL,
	}
}

func (m *mockStorageBackend) Upload(_ context.Context, key string, data io.Reader, _ string) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.uploaded[key] = b
	return nil
}

func (m *mockStorageBackend) UploadFile(_ context.Context, key string, localPath string, _ string) error {
	data, err := os.ReadFile(localPath) //nolint:gosec
	if err != nil {
		return err
	}
	m.uploaded[key] = data
	return nil
}

func (m *mockStorageBackend) Download(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockStorageBackend) GetURL(key string) string {
	return m.baseURL + "/" + key
}

func (m *mockStorageBackend) GetSignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return m.GetURL(key), nil
}

func (m *mockStorageBackend) Delete(_ context.Context, _ string) error { return nil }

func (m *mockStorageBackend) Exists(_ context.Context, _ string) (bool, error) { return false, nil }

func (m *mockStorageBackend) Copy(_ context.Context, _, _ string) error { return nil }

func (m *mockStorageBackend) GetMetadata(_ context.Context, _ string) (*storage.FileMetadata, error) {
	return nil, nil
}

// buildFakeHLSTree creates a minimal HLS directory structure for testing.
func buildFakeHLSTree(t *testing.T, videoID string) (hlsDir string, sourceFile string) {
	t.Helper()
	base := t.TempDir()
	hlsDir = filepath.Join(base, "streaming-playlists", "hls", videoID)

	// Create master playlist
	require.NoError(t, os.MkdirAll(hlsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte("#EXTM3U\n720p/stream.m3u8\n"), 0o600))

	// Create 720p sub-directory with stream playlist and a segment
	resDir := filepath.Join(hlsDir, "720p")
	require.NoError(t, os.MkdirAll(resDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "stream.m3u8"), []byte("#EXTM3U\nsegment_00000.ts\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "segment_00000.ts"), []byte("ts-data"), 0o600))

	// Create source file
	sourceFile = filepath.Join(base, "source.mp4")
	require.NoError(t, os.WriteFile(sourceFile, []byte("source-video-data"), 0o600))

	return hlsDir, sourceFile
}

func TestWithS3Backend_SetsField(t *testing.T) {
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}
	svc := NewService(NewMockEncodingRepository(), NewMockVideoRepository(), nil, t.TempDir(), cfg, nil, nil, nil)
	concrete := svc.(*service)

	assert.Nil(t, concrete.s3Backend, "s3Backend should be nil by default")

	backend := newMockStorageBackend("https://s3.example.com")
	concrete.WithS3Backend(backend)

	assert.NotNil(t, concrete.s3Backend, "s3Backend should be set after WithS3Backend")
}

func TestUploadHLSToS3_UploadsFilesAndReturnsURLs(t *testing.T) {
	videoID := "vid-s3-upload-test"
	hlsDir, sourceFile := buildFakeHLSTree(t, videoID)

	backend := newMockStorageBackend("https://s3.example.com/bucket")
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}
	svc := &service{cfg: cfg, s3Backend: backend}

	s3URLs, err := svc.uploadHLSToS3(context.Background(), videoID, sourceFile, hlsDir, "", "", []string{"720p"})
	require.NoError(t, err)

	// Verify master playlist URL is in s3URLs
	assert.Contains(t, s3URLs, "master")
	assert.Equal(t, "https://s3.example.com/bucket/videos/"+videoID+"/hls/master.m3u8", s3URLs["master"])

	// Verify quality playlist URL is in s3URLs
	assert.Contains(t, s3URLs, "720p")
	assert.Equal(t, "https://s3.example.com/bucket/videos/"+videoID+"/hls/720p/stream.m3u8", s3URLs["720p"])

	// Verify source URL is in s3URLs
	assert.Contains(t, s3URLs, "source")
	assert.Contains(t, s3URLs["source"], videoID+"/source")

	// Verify files were actually uploaded
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/hls/master.m3u8")
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/hls/720p/stream.m3u8")
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/hls/720p/segment_00000.ts")
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/source.mp4")
}

func TestUploadHLSToS3_NoBackend_ReturnsEmpty(t *testing.T) {
	videoID := "vid-no-s3"
	hlsDir, sourceFile := buildFakeHLSTree(t, videoID)

	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}
	svc := &service{cfg: cfg, s3Backend: nil}

	s3URLs, err := svc.uploadHLSToS3(context.Background(), videoID, sourceFile, hlsDir, "", "", []string{"720p"})
	require.NoError(t, err)
	assert.Empty(t, s3URLs)
}

func TestUploadHLSToS3_UploadsThumbAndPreview(t *testing.T) {
	videoID := "vid-thumb-preview"
	hlsDir, sourceFile := buildFakeHLSTree(t, videoID)

	// Create fake thumbnail and preview files
	base := t.TempDir()
	thumbPath := filepath.Join(base, videoID+"_thumb.jpg")
	previewPath := filepath.Join(base, videoID+"_preview.webp")
	require.NoError(t, os.WriteFile(thumbPath, []byte("jpeg-data"), 0o600))
	require.NoError(t, os.WriteFile(previewPath, []byte("webp-data"), 0o600))

	backend := newMockStorageBackend("https://s3.example.com/bucket")
	cfg := &config.Config{FFMPEGPath: "ffmpeg", HLSSegmentDuration: 4}
	svc := &service{cfg: cfg, s3Backend: backend}

	s3URLs, err := svc.uploadHLSToS3(context.Background(), videoID, sourceFile, hlsDir, thumbPath, previewPath, []string{"720p"})
	require.NoError(t, err)

	// Verify thumbnail and preview URLs are in s3URLs
	assert.Contains(t, s3URLs, "thumbnail")
	assert.Contains(t, s3URLs["thumbnail"], videoID+"/thumbnail.jpg")
	assert.Contains(t, s3URLs, "preview")
	assert.Contains(t, s3URLs["preview"], videoID+"/preview.webp")

	// Verify files were actually uploaded to the backend
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/thumbnail.jpg")
	assert.Contains(t, backend.uploaded, "videos/"+videoID+"/preview.webp")
}
