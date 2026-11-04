package blobverify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type hlsObservedBackend struct {
	storage.Backend // deliberately hides ObjectLister
	exists, opens   map[string]int
}

func (b hlsObservedBackend) Exists(ctx context.Context, key string) (bool, error) {
	b.exists[key]++
	return b.Backend.Exists(ctx, key)
}
func (b hlsObservedBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	b.opens[key]++
	return b.Backend.Open(ctx, key)
}

func TestDeepVerifyReusesRecordedHashesWithoutLosingMediaRole(t *testing.T) {
	vid := uuid.New()
	prefix := "streaming-playlists/hls/source/"
	root, variant, shared := prefix+"master.m3u8", prefix+"video.m3u8", prefix+"shared.mp4"
	repo := &fakeRepo{videoIDs: []uuid.UUID{vid}, playlists: []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: vid, MasterKey: root, Format: media.HLSFormatTS}}, videoFiles: []sqlcgen.ListAllVideoFileHashesRow{{StorageKey: shared, Sha256: digestOf("same bytes")}}}
	blobs := hlsObservedBackend{Backend: newStore(t, map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nvideo.m3u8\n", variant: "#EXTM3U\n#EXT-X-MAP:URI=\"shared.mp4\"\n#EXTINF:4,\nshared.mp4\n", shared: "same bytes"}), exists: map[string]int{}, opens: map[string]int{}}
	rep := verify(t, repo, blobs, Options{Deep: true, Hash: true})
	if !rep.Consistent() || rep.Checked != 3 || rep.Verified != 1 || rep.Unhashed != 2 || len(rep.Trees) != 1 || !rep.Trees[0].Complete || rep.Trees[0].ObservedMedia != 1 || rep.DeepUnsupported {
		t.Fatalf("deep classes/closure wrong: %+v", rep)
	}
	for _, key := range []string{root, variant, shared} {
		if blobs.exists[key] != 1 || blobs.opens[key] != 1 {
			t.Errorf("object inspected twice: %q HEAD=%d GET=%d", key, blobs.exists[key], blobs.opens[key])
		}
	}
}

func TestDeepVerifyStreamsAllMissingMembersBeyondSummarySample(t *testing.T) {
	vid := uuid.New()
	prefix := "streaming-playlists/" + vid.String() + "/r2/"
	root := prefix + "master.m3u8"
	repo := &fakeRepo{playlists: []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: vid, MasterKey: root, Format: media.HLSFormatTS}}}
	variant := "#EXTM3U\n"
	for i := 0; i < 30; i++ {
		variant += fmt.Sprintf("#EXTINF:4,\nseg_%d.ts\n", i)
	}
	var findings []Finding
	rep := verify(t, repo, newStore(t, map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n720p/playlist.m3u8\n", prefix + "720p/playlist.m3u8": variant, prefix + "unrelated.mp4": "not a dependency"}), Options{Deep: true, FindingSink: func(f Finding) error { findings = append(findings, f); return nil }})
	if rep.Consistent() || rep.Missing != 30 || rep.Incomplete != 1 || rep.Checked != 32 || len(rep.MissingKeys) != SampleLimit || len(findings) != 30 || len(rep.Trees) != 1 || rep.Trees[0].Complete || rep.Trees[0].Nodes != 32 {
		t.Fatalf("missing members hidden or doubled: %+v findings=%d", rep, len(findings))
	}
	for _, f := range findings {
		if f.Root != root || f.VideoID != vid.String() || f.Reason != "missing" || f.Unresolved || !strings.HasPrefix(f.Key, prefix+"720p/") {
			t.Fatalf("missing provenance wrong: %+v", f)
		}
	}
}

func TestDeepVerifyEvidenceFailurePreventsSuccessfulReport(t *testing.T) {
	vid := uuid.New()
	root := "streaming-playlists/" + vid.String() + "/master.m3u8"
	repo := &fakeRepo{playlists: []sqlcgen.ListStreamingPlaylistRefsRow{{VideoID: vid, MasterKey: root, Format: media.HLSFormatTS}}}
	stop := errors.New("evidence unavailable")
	rep, err := Verify(context.Background(), repo, newStore(t, map[string]string{root: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n720p/playlist.m3u8\n"}), Options{Deep: true, FindingSink: func(Finding) error { return stop }})
	if !errors.Is(err, stop) || rep.DeepVerificationVersion != 0 {
		t.Fatalf("evidence failure ignored: %v", err)
	}
}

func TestDeepVerificationVersionIncludesEmptyCatalogue(t *testing.T) {
	for version, deep := range map[int]bool{0: false, 1: true} {
		rep := verify(t, &fakeRepo{}, newStore(t, nil), Options{Deep: deep})
		if rep.DeepVerificationVersion != version {
			t.Fatalf("deep=%t version=%d; want %d", deep, rep.DeepVerificationVersion, version)
		}
	}
}
