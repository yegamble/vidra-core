//go:build integration

package peertubeimport

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
func referenceRerunFixture(t *testing.T, ctx context.Context, base string) (src, dest *pgxpool.Pool, run func(authoritative bool) *Report, first *Report) {
	t.Helper()
	src, _ = newScratchDB(t, ctx, base)
	dest, _ = newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,duration,views) VALUES
		(3,'33333333-3333-3333-3333-333333333333',1,'Still Transcoding','',1,2,30,0),
		(4,'44444444-4444-4444-4444-444444444444',1,'Transcoding Here','',1,1,30,0),
		(5,'55555555-5555-5555-5555-555555555555',1,'Copy Failed Once','',1,1,30,0)`)
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
	first = run(false)
	return src, dest, run, first
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
	src, dest, run, _ := referenceRerunFixture(t, ctx, base)

	// The source finishes every transcode and publishes video 3 — so it now offers
	// a playlist for all four videos. Only ONE of them is a gap:
	mustExec(t, ctx, src, `UPDATE "video" SET state=1 WHERE id=3`)
	mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES
		(2,2,'v2-master.m3u8'),(3,3,'v3-master.m3u8'),(4,4,'v4-master.m3u8'),(5,5,'v5-master.m3u8')`)
	// 1 — the import GAVE it a playlist and it has since been deleted here, which
	//     only transcode.Invalidate does, on purpose. A missing row is not a gap.
	mustExec(t, ctx, dest, `DELETE FROM streaming_playlists WHERE video_id=(SELECT id FROM videos WHERE title='First Video')`)
	// 2 — never had one, but its file was REPLACED here (media.OriginalVideoKey's
	//     shape): the source's playlist is the superseded content. This is also the
	//     only evidence a catalogue imported by an older release carries.
	mustExec(t, ctx, dest, `INSERT INTO video_files (video_id, kind, storage_key, size_bytes)
		SELECT id, 'original', 'web-videos/' || id::text || '.r1.mp4', 1 FROM videos WHERE title='Second Video'`)
	// 5 — an earlier COPY-mode run failed this tree and the operator has since
	//     switched to reference mode. A 'failed' row is not a playlist: still owed.
	mustExec(t, ctx, dest, `INSERT INTO peertube_import_ledger (entity_kind, source_id, status, note)
		VALUES ('hls_playlist','55555555-5555-5555-5555-555555555555','failed','HLS copy incomplete; rerun required')`)
	// 4 — Vidra's own transcode holds the row.
	mustExec(t, ctx, dest, `INSERT INTO streaming_playlists (video_id, state) SELECT id, 'pending' FROM videos WHERE title='Transcoding Here'`)

	report := run(false)
	if got := report.Entities[KindHLSPlaylist].Imported; got != 2 {
		t.Errorf("re-run carried %d HLS playlists, want exactly the two that are owed", got)
	}
	got := scanStrings(t, ctx, dest, `
		SELECT v.title || ' = ' || v.state || ' ' || COALESCE(sp.state || ':' || sp.master_key, 'none')
		FROM videos v LEFT JOIN streaming_playlists sp ON sp.video_id = v.id ORDER BY v.title`)
	want := []string{
		"Copy Failed Once = published ready:streaming-playlists/hls/55555555-5555-5555-5555-555555555555/v5-master.m3u8",
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

// Captions were written only inside importOneVideo, so one added on the source
// after a video's first import never arrived — in either mode.
func TestPeerTubeImportRerunCarriesLateCaptions(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, dest, run, first := referenceRerunFixture(t, ctx, base)

	// The track importOneVideo carried is recorded in the VIDEO's transaction (an
	// empty note; the pass's own record says "already here"), so a run interrupted
	// before the caption pass still knows it was handed over — and a clean first
	// migration counts it once, not imported AND skipped.
	if got := scanStrings(t, ctx, dest, `SELECT status || ':' || note FROM peertube_import_ledger WHERE entity_kind='caption'`); len(got) != 1 || got[0] != "done:" {
		t.Errorf("caption ledger after the first run = %q, want the inline record [done:]", got)
	}
	if got := first.Entities[KindCaption]; got.Imported != 1 || got.Skipped != 0 {
		t.Errorf("first-run caption counts = %+v, want 1 imported / 0 skipped", got)
	}
	// So a creator's deletion sticks even if the caption pass has never run.
	mustExec(t, ctx, src, `INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES (2,'de','v1-de.vtt',1)`)
	run(false)
	mustExec(t, ctx, dest, `DELETE FROM captions WHERE language='de'`)

	// A catalogue migrated by an OLDER release has its captions and no caption
	// ledger rows — and one of them has since been replaced here by the creator.
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind='caption' AND source_id='1'`)
	mustExec(t, ctx, dest, `UPDATE captions SET storage_key='captions/replaced-on-vidra.vtt' WHERE language='en'`)
	mustExec(t, ctx, src, `INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES (3,'fr','v1-fr.vtt',1)`)

	if got := run(false).Entities[KindCaption].Imported; got != 1 {
		t.Errorf("re-run carried %d captions, want exactly the late one", got)
	}
	captions := func() []string {
		return scanStrings(t, ctx, dest, `SELECT language || '=' || storage_key FROM captions ORDER BY language`)
	}
	if got := captions(); len(got) != 2 || got[0] != "en=captions/replaced-on-vidra.vtt" || got[1] != "fr=captions/v1-fr.vtt" {
		t.Errorf("captions = %v, want the late 'fr' carried and the creator's 'en' left alone", got)
	}

	// The creator deletes the carried track here. It stays deleted: the ledger row
	// is what says this source caption has already been handed over once.
	mustExec(t, ctx, dest, `DELETE FROM captions WHERE language='fr'`)
	if got := run(true).Entities[KindCaption].Imported; got != 0 {
		t.Errorf("a third run carried %d captions, want 0", got)
	}
	if got := captions(); len(got) != 1 {
		t.Errorf("captions after the creator deleted 'fr' = %v, want it to stay deleted", got)
	}
}

// The copy-mode half: the late track's BYTES are carried, to a key of its own.
// captions/<video>/<lang>.vtt is where a caption uploaded HERE lives
// (video.captionKey), and a copy that raced a creator's upload to that key would
// replace their object underneath their row.
func TestPeerTubeImportCopyRerunCarriesLateCaptionBytes(t *testing.T) {
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
	srcMedia, _ := storage.NewLocal(srcDir)
	destMedia, _ := storage.NewLocal(t.TempDir())
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, SrcMedia: srcMedia, DestMedia: destMedia})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}

	french := []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nsalut\n")
	if err := os.WriteFile(filepath.Join(srcDir, "captions", "v1-fr.vtt"), french, 0o640); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, src, `INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES (3,'fr','v1-fr.vtt',1)`)
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := report.Entities[KindCaption]; got.Imported != 1 || got.Failed != 0 {
		t.Fatalf("re-run captions = %+v, want exactly the late one imported", got)
	}
	var key, videoID string
	if err := dest.QueryRow(ctx, `SELECT storage_key, video_id::text FROM captions WHERE language='fr'`).Scan(&key, &videoID); err != nil {
		t.Fatalf("read the late caption: %v", err)
	}
	if want := "captions/" + videoID + "/import-3.vtt"; key != want {
		t.Errorf("late caption key = %q, want %q (never the native captions/<video>/fr.vtt)", key, want)
	}
	rc, err := destMedia.Open(ctx, key)
	if err != nil {
		t.Fatalf("the late caption's object was not copied: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if got, _ := io.ReadAll(rc); string(got) != string(french) {
		t.Errorf("copied caption bytes = %q, want the source's", got)
	}
}
