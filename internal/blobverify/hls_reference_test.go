package blobverify

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestHLSReferencesResolveOnlyServedRecordedFormat(t *testing.T) {
	root := "streaming-playlists/video/r2/master.m3u8"
	for _, tc := range []struct{ format, master, parent, uri, want string }{
		{media.HLSFormatTS, root, root, "720p/playlist.m3u8", "streaming-playlists/video/r2/720p/playlist.m3u8"},
		{media.HLSFormatTS, root, "streaming-playlists/video/r2/720p/playlist.m3u8", "seg_00001.ts", "streaming-playlists/video/r2/720p/seg_00001.ts"},
		{media.HLSFormatTS, root, "streaming-playlists/video/r2/720p/playlist.m3u8", "../480p/playlist.m3u8", "streaming-playlists/video/r2/480p/playlist.m3u8"},
		{media.HLSFormatCMAF, root, root, "cmaf/media_0.m3u8", "streaming-playlists/video/r2/cmaf/media_0.m3u8"},
		{media.HLSFormatCMAF, root, "streaming-playlists/video/r2/cmaf/media_0.m3u8", "init-0.mp4", "streaming-playlists/video/r2/cmaf/init-0.mp4"},
		{media.HLSFormatCMAF, root, "streaming-playlists/video/r2/cmaf/media_0.m3u8", "chunk-0-00001.m4s", "streaming-playlists/video/r2/cmaf/chunk-0-00001.m4s"},
		{media.HLSFormatTS, "streaming-playlists/hls/source/master.m3u8", "streaming-playlists/hls/source/v1-720.m3u8", "video-720.mp4", "streaming-playlists/hls/source/video-720.mp4"},
	} {
		pl := mediagc.PlaylistRef{MasterKey: tc.master, Format: tc.format}
		got, err := resolveHLSReference(pl, tc.parent, tc.uri)
		if err != nil || got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, %v; want %q", tc.parent, tc.uri, got, err, tc.want)
		}
	}
}

func TestHLSReferencesRejectAmbiguousForeignAndCrossFormatPaths(t *testing.T) {
	root := "streaming-playlists/video/r2/master.m3u8"
	for _, tc := range []struct{ format, master, parent, uri string }{
		{media.HLSFormatTS, root, root, "cmaf/media_0.m3u8"},
		{media.HLSFormatCMAF, root, root, "720p/playlist.m3u8"},
		{"future", root, root, "720p/playlist.m3u8"},
		{"", root, root, "720p/playlist.m3u8"},
		{media.HLSFormatTS, "master.m3u8", "master.m3u8", "720p/playlist.m3u8"},
		{media.HLSFormatTS, "../tree/master.m3u8", "../tree/master.m3u8", "720p/playlist.m3u8"},
		{media.HLSFormatTS, root, "streaming-playlists/video/r1/master.m3u8", "720p/playlist.m3u8"},
		{media.HLSFormatTS, root, "foreign/master.m3u8", "720p/playlist.m3u8"},
	} {
		if key, err := resolveHLSReference(mediagc.PlaylistRef{MasterKey: tc.master, Format: tc.format}, tc.parent, tc.uri); err == nil || key != "" {
			t.Errorf("invalid format/path admitted: %+v -> %q, %v", tc, key, err)
		}
	}
	for _, uri := range []string{"", "/720p/playlist.m3u8", "//host/720p/playlist.m3u8", "https://host/720p/playlist.m3u8", "data:playlist", "../r1/720p/playlist.m3u8", "../../../foreign/720p/playlist.m3u8", "720p\\playlist.m3u8", "720p/%70laylist.m3u8", "720p/%2e%2e/playlist.m3u8", "720p/%252e%252e/playlist.m3u8", "720p/playlist.m3u8?x=1", "720p/playlist.m3u8#part", "720p/playlist.m3u8\n", "720p/video.mp4", "peertube/flat.mp4"} {
		if key, err := resolveHLSReference(mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}, root, uri); err == nil || key != "" {
			t.Errorf("invalid URI admitted: %q -> %q, %v", uri, key, err)
		}
	}
}

func TestPlaylistReferencesKeepRecordedFormat(t *testing.T) {
	id := uuid.New()
	refs, err := mediagc.ListReferences(context.Background(), &fakeRepo{playlists: []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: id, MasterKey: "streaming-playlists/video/master.m3u8", Format: media.HLSFormatCMAF}}})
	if err != nil || len(refs.Playlists) != 1 || refs.Playlists[0].Format != media.HLSFormatCMAF {
		t.Fatalf("playlist format lost: %+v, %v", refs.Playlists, err)
	}
}
