package video

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"vidra-core/internal/domain"
)

func TestBuildVideoFilesResponse_NoFiles(t *testing.T) {
	v := &domain.Video{ID: "v1"}
	files, playlists := BuildVideoFilesResponse(v)
	assert.NotNil(t, files)
	assert.NotNil(t, playlists)
	assert.Empty(t, files)
	assert.Empty(t, playlists)
}

func TestBuildVideoFilesResponse_SourceOnly(t *testing.T) {
	v := &domain.Video{
		ID:          "v1",
		OutputPaths: map[string]string{"source": "/storage/web-videos/v1.mp4"},
	}
	files, playlists := BuildVideoFilesResponse(v)
	assert.Equal(t, 1, len(files))
	assert.Equal(t, 0, files[0].Resolution.ID) // source = original, ID=0
	assert.Contains(t, files[0].FileUrl, "v1.mp4")
	assert.Empty(t, playlists)
}

func TestBuildVideoFilesResponse_HLSResolutions(t *testing.T) {
	v := &domain.Video{
		ID: "v1",
		OutputPaths: map[string]string{
			"master": "/storage/hls/v1/master.m3u8",
			"720p":   "/storage/hls/v1/720p/stream.m3u8",
			"480p":   "/storage/hls/v1/480p/stream.m3u8",
		},
	}
	files, playlists := BuildVideoFilesResponse(v)
	assert.Empty(t, files) // No source file
	assert.Equal(t, 1, len(playlists))
	assert.Equal(t, 1, playlists[0].Type) // HLS
	assert.Contains(t, playlists[0].PlaylistUrl, "master.m3u8")
	assert.Equal(t, 2, len(playlists[0].Files))
}

func TestBuildVideoFilesResponse_S3URLsPreferred(t *testing.T) {
	v := &domain.Video{
		ID: "v1",
		OutputPaths: map[string]string{
			"master": "/storage/hls/v1/master.m3u8",
			"720p":   "/storage/hls/v1/720p/stream.m3u8",
		},
		S3URLs: map[string]string{
			"master": "https://s3.example.com/videos/v1/hls/master.m3u8",
			"720p":   "https://s3.example.com/videos/v1/hls/720p/stream.m3u8",
		},
	}
	_, playlists := BuildVideoFilesResponse(v)
	assert.Equal(t, 1, len(playlists))
	assert.Contains(t, playlists[0].PlaylistUrl, "s3.example.com")
	assert.Contains(t, playlists[0].Files[0].FileUrl, "s3.example.com")
}

func TestBuildVideoFilesResponse_SourceAndHLS(t *testing.T) {
	v := &domain.Video{
		ID: "v1",
		OutputPaths: map[string]string{
			"source": "/storage/web-videos/v1.mp4",
			"master": "/storage/hls/v1/master.m3u8",
			"720p":   "/storage/hls/v1/720p/stream.m3u8",
			"480p":   "/storage/hls/v1/480p/stream.m3u8",
			"240p":   "/storage/hls/v1/240p/stream.m3u8",
		},
	}
	files, playlists := BuildVideoFilesResponse(v)
	assert.Equal(t, 1, len(files))           // source as web video
	assert.Equal(t, 1, len(playlists))       // one HLS playlist
	assert.Equal(t, 3, len(playlists[0].Files)) // 720p, 480p, 240p
}
