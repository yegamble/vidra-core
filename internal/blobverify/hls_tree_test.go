package blobverify

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/mediagc"
)

func graphFixture(t *testing.T, pl mediagc.PlaylistRef, objects map[string]string, limits ...hlsLimits) (hlsTreeResult, []Finding, map[string]int) {
	t.Helper()
	blobs, probes := newStore(t, objects), map[string]int{}
	var findings []Finding
	w := hlsTreeWalker{blobs: blobs, limits: defaultHLSLimits,
		probe: func(key string) outcome {
			probes[key]++
			return inspect(context.Background(), blobs, target{key: key}, false)
		},
		finding: func(f Finding) error { findings = append(findings, f); return nil },
	}
	if len(limits) != 0 {
		w.limits = limits[0]
	}
	result, err := w.walk(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	return result, findings, probes
}

func TestHLSTreeStopsAtBudgetBeforeChildInspection(t *testing.T) {
	root := "streaming-playlists/video/r2/master.m3u8"
	for _, limit := range []string{"nodes", "depth", "playlist_bytes", "manifest_bytes"} {
		cap := defaultHLSLimits
		switch limit {
		case "nodes":
			cap.nodes = 1
		case "depth":
			cap.depth = 0
		case "playlist_bytes":
			cap.playlistBytes = 4
		case "manifest_bytes":
			cap.manifestBytes = 8
		}
		result, findings, probes := graphFixture(t, mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}, map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n720p/playlist.m3u8\n"}, cap)
		if result.complete || result.usage.exhausted != limit || result.usage.nodes != 1 || len(probes) != 1 || len(findings) != 1 || findings[0].Reason != "limit_"+limit {
			t.Fatalf("limit allowed child work: %+v, %+v, %v", result, findings, probes)
		}
	}
}

func TestHLSTreeWalksNativeTSAndCMAFAndImportedByteRangeRoles(t *testing.T) {
	for _, tc := range []struct {
		format, root, master, variant string
		media                         int
	}{
		{media.HLSFormatTS, "streaming-playlists/video/r2/master.m3u8", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n720p/playlist.m3u8\n", "#EXTM3U\n#EXTINF:4,\nseg_0.ts\n", 1},
		{media.HLSFormatCMAF, "streaming-playlists/video/r2/master.m3u8", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\ncmaf/media_0.m3u8\n", "#EXTM3U\n#EXT-X-MAP:URI=\"init-0.mp4\"\n#EXTINF:4,\nchunk-0-0.m4s\n", 1},
		{media.HLSFormatTS, "streaming-playlists/hls/source/master.m3u8", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"audio.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n", "#EXTM3U\n#EXT-X-MAP:URI=\"shared.mp4\",BYTERANGE=\"8@0\"\n#EXTINF:4,\n#EXT-X-BYTERANGE:16@8\nshared.mp4\n", 1},
	} {
		prefix := path.Dir(tc.root) + "/"
		objects := map[string]string{tc.root: tc.master, prefix + "720p/playlist.m3u8": tc.variant, prefix + "720p/seg_0.ts": "segment", prefix + "cmaf/media_0.m3u8": tc.variant, prefix + "cmaf/init-0.mp4": "init", prefix + "cmaf/chunk-0-0.m4s": "segment", prefix + "video.m3u8": tc.variant, prefix + "audio.m3u8": tc.variant, prefix + "shared.mp4": "init and media"}
		result, findings, probes := graphFixture(t, mediagc.PlaylistRef{MasterKey: tc.root, Format: tc.format}, objects)
		if !result.complete || result.media != tc.media || len(findings) != 0 {
			t.Fatalf("graph incomplete: %+v, %+v", result, findings)
		}
		for key, count := range probes {
			if count != 1 {
				t.Errorf("duplicate storage probe: %q = %d", key, count)
			}
		}
		if tc.format == media.HLSFormatTS && strings.Contains(tc.root, "/hls/") && (probes[prefix+"shared.mp4"] != 1 || result.usage.nodes != 4) {
			t.Fatalf("MAP/MEDIA union or shared-object dedup lost: %+v, %v", result, probes)
		}
	}
}

func TestHLSTreeMissingPlaylistLeavesUnknownSubtreeAndChecksOtherSiblings(t *testing.T) {
	root := "streaming-playlists/hls/source/master.m3u8"
	prefix := path.Dir(root) + "/"
	objects := map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nmissing.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\nother.m3u8\n", prefix + "other.m3u8": "#EXTM3U\n#EXTINF:4,\nmissing.mp4\n", prefix + "unrelated.mp4": "not a dependency"}
	result, findings, probes := graphFixture(t, mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}, objects)
	if result.complete || len(findings) != 2 || !findings[0].Unresolved || findings[1].Unresolved || probes[prefix+"missing.mp4"] != 1 || probes[prefix+"unrelated.mp4"] != 0 {
		t.Fatalf("missing closure misreported: %+v, %+v, %v", result, findings, probes)
	}
}

func TestHLSTreeRejectsEmptyMapOnlyAndKeyOnlyClosures(t *testing.T) {
	root := "streaming-playlists/hls/source/master.m3u8"
	for _, tag := range []string{"", "#EXT-X-MAP:URI=\"object.mp4\"\n", "#EXT-X-KEY:METHOD=AES-128,URI=\"object.mp4\"\n", "#EXT-X-SESSION-DATA:DATA-ID=\"metadata\",URI=\"object.mp4\"\n"} {
		result, findings, _ := graphFixture(t, mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}, map[string]string{root: "#EXTM3U\n" + tag, path.Dir(root) + "/object.mp4": "bytes"})
		if result.complete || len(findings) != 1 || findings[0].Reason != "no_media" {
			t.Fatalf("non-media closure passed: %+v, %+v", result, findings)
		}
	}
}

func TestHLSTreeRejectsCyclesAndUnsafeReferencesWithoutForeignReads(t *testing.T) {
	root := "streaming-playlists/hls/source/master.m3u8"
	for _, uri := range []string{"master.m3u8", "../foreign.m3u8", "%2e%2e/foreign.m3u8", "https://example.test/private?token=hidden"} {
		result, findings, probes := graphFixture(t, mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}, map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n" + uri + "\n"})
		if result.complete || len(findings) != 1 || len(probes) != 1 || strings.Contains(findings[0].Key, "token") {
			t.Fatalf("unsafe graph was followed: %+v, %+v, %v", result, findings, probes)
		}
	}
}

func TestHLSTreePropagatesEvidenceFailureAndCancellation(t *testing.T) {
	root := "streaming-playlists/hls/source/master.m3u8"
	pl := mediagc.PlaylistRef{MasterKey: root, Format: media.HLSFormatTS}
	blobs := newStore(t, map[string]string{root: "#EXTM3U\n"})
	stop := errors.New("evidence disk failed")
	w := hlsTreeWalker{blobs: blobs, limits: defaultHLSLimits, probe: func(key string) outcome { return inspect(context.Background(), blobs, target{key: key}, false) }, finding: func(Finding) error { return stop }}
	if result, err := w.walk(context.Background(), pl); !errors.Is(err, stop) || result.complete {
		t.Fatalf("writer failure passed: %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.walk(ctx, pl); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
