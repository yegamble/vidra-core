//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vidra/vidra-core/internal/storage"
)

// referenceRerunFixture imports the stock fixture in reference mode, with one
// extra video that is STILL TRANSCODING on the source when the first run reads
// it: a row, state TO_TRANSCODE (2), and nothing to play yet. On a live instance
// that is every video uploaded shortly before a scheduled run.
func referenceRerunFixture(t *testing.T, ctx context.Context, base string) (src, dest *pgxpool.Pool, rerun func() *Report) {
	t.Helper()
	src, _ = newScratchDB(t, ctx, base)
	dest, _ = newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,duration,views)
		VALUES (3,'33333333-3333-3333-3333-333333333333',1,'Still Transcoding','',1,2,30,0)`)
	sharedDir := t.TempDir()
	seedSourceMedia(t, sharedDir)
	shared, err := storage.NewLocal(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: shared})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	rerun = func() *Report {
		t.Helper()
		report, err := imp.Run(ctx, version, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return report
	}
	rerun()
	return src, dest, rerun
}

// A video's ledger row is terminal after its first import, so the playlist the
// source finished AFTER that run had no way in: importOneVideo never ran again,
// and the backfill pass was copy-mode only. The video stayed unplayable for good
// — and under --source-authoritative its state still followed the source to
// 'published'.
func TestPeerTubeImportReferenceRerunCarriesLateHLS(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, dest, rerun := referenceRerunFixture(t, ctx, base)

	// The source finishes the transcode. Meanwhile video 1 was re-transcoded HERE,
	// and video 2 has a Vidra transcode in flight: both rows are Vidra's.
	mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES (3,3,'v3-master.m3u8')`)
	mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES (4,2,'v2-master.m3u8')`)
	mustExec(t, ctx, dest, `UPDATE streaming_playlists SET master_key='hls/vidra-transcode/master.m3u8'
		WHERE video_id=(SELECT id FROM videos WHERE title='First Video')`)
	mustExec(t, ctx, dest, `INSERT INTO streaming_playlists (video_id, state)
		SELECT id, 'pending' FROM videos WHERE title='Second Video'`)

	report := rerun()
	if got := report.Entities[KindHLSPlaylist].Imported; got != 1 {
		t.Errorf("re-run carried %d HLS playlists, want exactly the late one", got)
	}
	keys := map[string]string{}
	rows, err := dest.Query(ctx, `SELECT v.title, sp.state || ':' || sp.master_key FROM streaming_playlists sp JOIN videos v ON v.id = sp.video_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, key string
		if err := rows.Scan(&title, &key); err != nil {
			t.Fatal(err)
		}
		keys[title] = key
	}
	for title, want := range map[string]string{
		"Still Transcoding": "ready:streaming-playlists/hls/33333333-3333-3333-3333-333333333333/v3-master.m3u8",
		"First Video":       "ready:hls/vidra-transcode/master.m3u8", // a ready playlist is never replaced
		"Second Video":      "pending:",                              // nor is a row Vidra's own pipeline holds
	} {
		if keys[title] != want {
			t.Errorf("%s: playlist = %q, want %q", title, keys[title], want)
		}
	}
	if got := rerun().Entities[KindHLSPlaylist].Imported; got != 0 {
		t.Errorf("a third run carried %d HLS playlists, want 0", got)
	}
}
