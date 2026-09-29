//go:build integration

package peertubeimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestPeerTubeImportMissingCaptionKeepsVideoAndRepairsOnRerun(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	srcDir := t.TempDir()
	seedSourceMedia(t, srcDir)
	captionPath := filepath.Join(srcDir, "captions", "v1-en.vtt")
	if err := os.Remove(captionPath); err != nil {
		t.Fatal(err)
	}
	srcMedia, _ := storage.NewLocal(srcDir)
	destMedia, _ := storage.NewLocal(t.TempDir())
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, SrcMedia: srcMedia, DestMedia: destMedia})
	run := func() *Report {
		t.Helper()
		r, err := imp.Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := run()
	if got := first.Entities[KindVideo]; got.Imported != 2 || got.Failed != 0 {
		t.Fatalf("video counts with an absent caption = %+v, want 2 imported and none failed", got)
	}
	if got := first.Entities[KindCaption]; got.Imported != 0 || got.Failed != 1 {
		t.Fatalf("caption counts = %+v, want one retryable failure", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT status FROM peertube_import_ledger WHERE entity_kind='caption' AND source_id='1'`); len(got) != 1 || got[0] != "failed" {
		t.Fatalf("caption ledger = %v, want failed", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT state FROM streaming_playlists`); len(got) != 1 || got[0] != "ready" {
		t.Fatalf("HLS after caption failure = %v, want ready", got)
	}
	videoIDs := scanStrings(t, ctx, dest, `SELECT id::text FROM videos ORDER BY id`)
	sum := sha256.Sum256(sourceVideoBytes)
	if got := scanStrings(t, ctx, dest, `SELECT sha256 FROM video_files WHERE kind='original'`); len(got) != 1 || got[0] != hex.EncodeToString(sum[:]) {
		t.Fatalf("original SHA-256 = %v, want the source digest", got)
	}
	caption := []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nrecovered\n")
	if err := os.WriteFile(captionPath, caption, 0o600); err != nil {
		t.Fatal(err)
	}
	repaired := run()
	if got := repaired.Entities[KindCaption]; got.Imported != 1 || got.Failed != 0 {
		t.Fatalf("repaired caption counts = %+v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT id::text FROM videos ORDER BY id`); strings.Join(got, ",") != strings.Join(videoIDs, ",") {
		t.Fatal("caption repair replaced video rows")
	}
	keys := scanStrings(t, ctx, dest, `SELECT storage_key FROM captions`)
	if len(keys) != 1 {
		t.Fatalf("caption keys = %v", keys)
	}
	rc, err := destMedia.Open(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(body) != string(caption) {
		t.Fatalf("repaired caption bytes mismatch: %v", err)
	}
	mustExec(t, ctx, dest, `DELETE FROM captions`)
	if got := run().Entities[KindCaption]; got.Imported != 0 || got.Failed != 0 {
		t.Fatalf("creator-deleted caption was restored: %+v", got)
	}
}

type concurrentCaptionSource struct {
	storage.Backend
	active atomic.Int32
	peak   atomic.Int32
}

func (s *concurrentCaptionSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasPrefix(key, "captions/") {
		n := s.active.Add(1)
		defer s.active.Add(-1)
		for old := s.peak.Load(); n > old && !s.peak.CompareAndSwap(old, n); old = s.peak.Load() {
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Backend.Open(ctx, key)
}

func TestPeerTubeImportCopyCaptionsUsesBoundedConcurrency(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	srcDir := t.TempDir()
	seedSourceMedia(t, srcDir)
	for i, lang := range []string{"fr", "de", "es", "it", "pt", "nl", "pl"} {
		name := "v1-" + lang + ".vtt"
		if err := os.WriteFile(filepath.Join(srcDir, "captions", name), []byte("WEBVTT\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mustExec(t, ctx, src, `INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES ($1,$2,$3,1)`, i+3, lang, name)
	}
	srcMedia, _ := storage.NewLocal(srcDir)
	destMedia, _ := storage.NewLocal(t.TempDir())
	observed := &concurrentCaptionSource{Backend: srcMedia}
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, SrcMedia: observed, DestMedia: destMedia})
	r, err := imp.Run(ctx, 800, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Entities[KindCaption]; got.Imported != 8 || got.Skipped != 0 || got.Failed != 0 {
		t.Fatalf("caption counts = %+v", got)
	}
	if peak := observed.peak.Load(); peak < 2 || peak > 4 {
		t.Fatalf("concurrent caption opens = %d, want 2–4", peak)
	}
}
