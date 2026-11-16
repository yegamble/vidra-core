//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// A "videoFile" row with resolution = 0 bound to the video is an audio-only web
// file. Beside HLS video it is an audio rendition, not the uploader's file and
// not a playable video (the seeded row is synthetic; the shape was measured on
// a real source, see Source.VideoFiles); carrying it as the original made
// the download dialog offer a few MB of audio as "Original file", /original
// stream audio only, and a re-transcode from it rebuild the ladder audio-only.
func audioOnlyFixture(t *testing.T, withRealFile, dropHLS bool) (*Report, *Report, func(sql string) []string) {
	t.Helper()
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
	stmt := `DELETE FROM "videoFile" WHERE "videoId"=1;
		INSERT INTO "videoFile" (id,"videoId",resolution,size,extname,filename) VALUES (30,1,0,4,'.mp4','v1-0.mp4');`
	if withRealFile {
		stmt += `INSERT INTO "videoFile" (id,"videoId",resolution,size,extname,filename) VALUES (31,1,720,` +
			strconv.Itoa(len(sourceVideoBytes)) + `,'.mp4','v1-720.mp4');`
	}
	if dropHLS {
		stmt += `DELETE FROM "videoFile" WHERE "videoStreamingPlaylistId" IN (SELECT id FROM "videoStreamingPlaylist" WHERE "videoId"=1);
			DELETE FROM "videoStreamingPlaylist" WHERE "videoId"=1;`
	}
	mustExec(t, ctx, src, stmt)
	srcDir := t.TempDir()
	seedSourceMedia(t, srcDir)
	// The audio file's bytes EXIST in the source store: the fix must hold even
	// when copying it would succeed.
	if err := os.WriteFile(srcDir+"/web-videos/v1-0.mp4", []byte("AUDIO"), 0o640); err != nil {
		t.Fatal(err)
	}
	srcMedia, err := storage.NewLocal(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Policy: PolicySkip, SrcMedia: srcMedia, DestMedia: destMedia}
	plan, err := NewImporter(dest, NewSourceFromPool(src), opts).Plan(ctx, 800)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewImporter(dest, NewSourceFromPool(src), opts).Run(ctx, 800, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan, r, func(sql string) []string { return scanStrings(t, ctx, dest, sql) }
}

func TestPeerTubeImportAudioOnlyWebFileIsNotTheOriginal(t *testing.T) {
	plan, r, q := audioOnlyFixture(t, false, false)
	if got := q(`SELECT name FROM (SELECT original_name AS name FROM video_files WHERE kind='original') s`); len(got) != 0 {
		t.Fatalf("an audio-only web file was carried as an original: %v", got)
	}
	if plan.Entities[KindVideoFile].Planned != 0 {
		t.Errorf("plan counts the audio-only file as an original: %+v", plan.Entities[KindVideoFile])
	}
	if c := r.Entities[KindVideoFile]; c.Failed != 0 || c.Imported != 0 || c.Unsupported != 0 {
		t.Errorf("audio-only web file must be neither imported nor a failure: %+v", c)
	}
	got := q(`SELECT status||'|'||note FROM peertube_import_ledger WHERE entity_kind='video_file' AND source_id='11111111-1111-1111-1111-111111111111'`)
	if len(got) != 1 || !strings.HasPrefix(got[0], "done|") || !strings.Contains(got[0], "audio-only") {
		t.Fatalf("video_file ledger = %v, want one done row saying why", got)
	}
	// HLS for the same video is untouched.
	if got := q(`SELECT state FROM streaming_playlists WHERE video_id=(SELECT id FROM videos WHERE title='First Video')`); strings.Join(got, ",") != "ready" {
		t.Fatalf("HLS playlist for the audio-only-web-file video: %v", got)
	}
}

func TestPeerTubeImportRealWebFileBeatsAudioOnlyOne(t *testing.T) {
	_, r, q := audioOnlyFixture(t, true, false)
	if r.Entities[KindVideoFile].Imported != 1 {
		t.Fatalf("original not carried: %+v", r.Entities[KindVideoFile])
	}
	if got := q(`SELECT original_name FROM video_files WHERE kind='original'`); strings.Join(got, ",") != "v1-720.mp4" {
		t.Fatalf("originals = %v, want the 720p web file", got)
	}
}

// With no HLS video rung the audio file is the only media the video has (a
// genuine audio upload): dropping it would import the video with nothing to play.
func TestPeerTubeImportAudioOnlyWebFileIsKeptWhenItIsTheOnlyMedia(t *testing.T) {
	_, r, q := audioOnlyFixture(t, false, true)
	if r.Entities[KindVideoFile].Imported != 1 {
		t.Fatalf("the only media was not carried: %+v", r.Entities[KindVideoFile])
	}
	if got := q(`SELECT original_name FROM video_files WHERE kind='original'`); strings.Join(got, ",") != "v1-0.mp4" {
		t.Fatalf("originals = %v, want the audio file", got)
	}
}
