//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vidra/vidra-core/internal/storage"
)

// referenceRerunFixture imports the stock fixture in reference mode, with two
// extra videos the source has NO playlist for when the first run reads them.
// Video 3 is STILL TRANSCODING: a row, state TO_TRANSCODE (2), and nothing to
// play yet — on a live instance, every video uploaded shortly before a scheduled
// run. run(authoritative) performs one more run in the given mode.
func referenceRerunFixture(t *testing.T, ctx context.Context, base string) (src, dest *pgxpool.Pool, run func(authoritative bool) *Report) {
	t.Helper()
	src, _ = newScratchDB(t, ctx, base)
	dest, _ = newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,duration,views) VALUES
		(3,'33333333-3333-3333-3333-333333333333',1,'Still Transcoding','',1,2,30,0),
		(4,'44444444-4444-4444-4444-444444444444',1,'Transcoding Here','',1,1,30,0)`)
	sharedDir := t.TempDir()
	seedSourceMedia(t, sharedDir)
	shared, err := storage.NewLocal(sharedDir)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	run = func(authoritative bool) *Report {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(src), Options{
			Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: shared, SourceAuthoritative: authoritative,
		})
		if version == 0 {
			if version, err = imp.Preflight(ctx); err != nil {
				t.Fatalf("preflight: %v", err)
			}
		}
		report, err := imp.Run(ctx, version, nil)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return report
	}
	run(false)
	return src, dest, run
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
	src, dest, run := referenceRerunFixture(t, ctx, base)

	// The source finishes every transcode and publishes video 3 — so it now offers
	// a playlist for all four videos. Only ONE of them is a gap:
	mustExec(t, ctx, src, `UPDATE "video" SET state=1 WHERE id=3`)
	mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES
		(2,2,'v2-master.m3u8'),(3,3,'v3-master.m3u8'),(4,4,'v4-master.m3u8')`)
	// 1 — the import GAVE it a playlist and it has since been deleted here, which
	//     only transcode.Invalidate does, on purpose. A missing row is not a gap.
	mustExec(t, ctx, dest, `DELETE FROM streaming_playlists WHERE video_id=(SELECT id FROM videos WHERE title='First Video')`)
	// 2 — never had one, but its file was REPLACED here (media.OriginalVideoKey's
	//     shape): the source's playlist is the superseded content. This is also the
	//     only evidence a catalogue imported by an older release carries.
	mustExec(t, ctx, dest, `INSERT INTO video_files (video_id, kind, storage_key, size_bytes)
		SELECT id, 'original', 'web-videos/' || id::text || '.r1.mp4', 1 FROM videos WHERE title='Second Video'`)
	// 4 — Vidra's own transcode holds the row.
	mustExec(t, ctx, dest, `INSERT INTO streaming_playlists (video_id, state) SELECT id, 'pending' FROM videos WHERE title='Transcoding Here'`)

	report := run(false)
	if got := report.Entities[KindHLSPlaylist].Imported; got != 1 {
		t.Errorf("re-run carried %d HLS playlists, want exactly the late one", got)
	}
	got := scanStrings(t, ctx, dest, `
		SELECT v.title || ' = ' || v.state || ' ' || COALESCE(sp.state || ':' || sp.master_key, 'none')
		FROM videos v LEFT JOIN streaming_playlists sp ON sp.video_id = v.id ORDER BY v.title`)
	want := []string{
		"First Video = published none",
		"Second Video = published none",
		// Playable, and still the draft the first run wrote: the default run never
		// rewrites what it wrote. The report has to say so.
		"Still Transcoding = draft ready:streaming-playlists/hls/33333333-3333-3333-3333-333333333333/v3-master.m3u8",
		"Transcoding Here = published pending:",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("after the re-run:\n got %q\nwant %q", got, want)
	}
	if notes := strings.Join(report.Conflicts, "\n"); !strings.Contains(notes, "1 video(s) received their HLS playlist on this run but are still drafts") {
		t.Errorf("nothing in the report says the late video is still a draft: %q", report.Conflicts)
	}

	// --source-authoritative is the mode that follows the source's state; it
	// carries nothing twice.
	if got := run(true).Entities[KindHLSPlaylist].Imported; got != 0 {
		t.Errorf("a third run carried %d HLS playlists, want 0", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT state FROM videos WHERE title='Still Transcoding'`); got[0] != "published" {
		t.Errorf("state under --source-authoritative = %q, want published (with its playlist)", got[0])
	}
}
