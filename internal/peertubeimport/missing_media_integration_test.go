//go:build integration

package peertubeimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestPeerTubeMissingArtworkRemainsRetryable(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	var restored atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !restored.Load() {
			if strings.Contains(r.URL.Path, "/thumbnails/") {
				w.WriteHeader(404)
				return
			}
			if strings.Contains(r.URL.Path, "/storyboards/") {
				w.WriteHeader(503)
				return
			}
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	}))
	defer srv.Close()
	mustExec(t, ctx, src, `UPDATE actor SET url=$1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, srv.URL)
	media, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: media})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatal(err)
	}
	thumb, board := first.Entities[KindThumbnail], first.Entities[KindStoryboard]
	if thumb.Failed != 1 || thumb.MissingSource != 1 || board.Failed != 1 || board.MissingSource != 0 {
		t.Fatalf("404 must be missing source; 503 must remain transient: thumbnail=%+v storyboard=%+v", thumb, board)
	}
	restored.Store(true)
	second, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{KindThumbnail, KindStoryboard} {
		c := second.Entities[kind]
		if c.Imported != 1 || c.Failed != 0 || c.MissingSource != 0 {
			t.Errorf("restored %s: %+v", kind, c)
		}
	}
	if second.Entities[KindVideo].Imported != 0 {
		t.Fatal("retry recreated existing videos")
	}
}
