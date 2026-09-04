package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedReadyHLSRowOnly marks videoID's playlist ready and records one rendition
// WITHOUT writing a single object, reproducing the drift that beta hit: the
// database advertises a playable tree (detail responses hand out hls_url) while
// the bytes it names are absent from the bucket. Every server-side signal stays
// green and only the client sees the failure.
func seedReadyHLSRowOnly(t *testing.T, repo *transcodeFakeRepo, videoID string) {
	t.Helper()
	id := uuid.MustParse(videoID)
	prefix := "streaming-playlists/" + videoID
	repo.playlists[id] = sqlcgen.StreamingPlaylist{VideoID: id, MasterKey: prefix + "/master.m3u8", State: "ready"}
	repo.renditions[id] = []sqlcgen.VideoRendition{
		{ID: uuid.New(), VideoID: id, Height: 240, Width: 320, KeyPrefix: prefix + "/240p"},
	}
}

// TestHLSMissingObjectIs404AndNotCached fences the failure that makes a missing
// object PERMANENT for a viewer. The versioned ?v= URL is served with
// "max-age=31536000, immutable", and that header was applied to the response
// before the object was opened — so the 404 inherited it and every browser that
// saw one pinned the failure for a year. Repairing the storage side then fixed
// nothing for anyone who had already loaded the page.
func TestHLSMissingObjectIs404AndNotCached(t *testing.T) {
	srv, _, tcRepo, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLSRowOnly(t, tcRepo, id)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	for _, tc := range []struct{ name, path string }{
		{"master", "/hls/master.m3u8?v=" + version},
		{"variant", "/hls/240p/playlist.m3u8?v=" + version},
		{"segment", "/hls/240p/seg_00000.ts?v=" + version},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getHLS(srv, "/api/v1/videos/"+id+tc.path, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s = %d, want 404; body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != delivery.CacheNoStore {
				t.Errorf("%s Cache-Control = %q, want %q — a 404 must never be cached",
					tc.name, got, delivery.CacheNoStore)
			}
		})
	}
}

// TestCaptionMissingObjectIs404 fences the 500 that beta emits on every watch
// page whose caption row outlived its object: OpenCaption returns the storage
// backend's ErrNotFound unwrapped, which the handler did not recognise, so a
// missing file surfaced as an internal server error instead of a 404.
func TestCaptionMissingObjectIs404(t *testing.T) {
	srv, blobs, _, _ := videoServerEnv(t, testConfig())
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := uploadCaption(srv, id, "en", "English", sampleVTT, tok, true); rec.Code != http.StatusCreated {
		t.Fatalf("upload caption = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// The row survives; the object does not — exactly the beta state.
	if err := blobs.Delete(context.Background(), "captions/"+id+"/en.vtt"); err != nil {
		t.Fatalf("delete caption object: %v", err)
	}

	rec := downloadCaption(srv, id, "en")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("caption download = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != delivery.CacheNoStore {
		t.Errorf("caption Cache-Control = %q, want %q", got, delivery.CacheNoStore)
	}
}

// storage.ErrNotFound is what both paths above hinge on; keep the reference so
// a rename of the sentinel breaks this file too.
var _ = storage.ErrNotFound
