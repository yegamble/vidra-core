//go:build integration

package peertubeimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vidra/vidra-core/internal/storage"
)

// Schema subset grounded in PeerTube v8.3.1 (3324876a0b0f846f8b6632470b3d9d53ef74b8b7):
// videoSource keeps upload originals independently of the HLS videoFile rows.
func TestPeerTubeImportKeptOriginalOnHLSOnlySource(t *testing.T) {
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
	mustExec(t, ctx, src, `DELETE FROM "videoFile" WHERE "videoId"=1;
 CREATE TABLE "videoSource" (id serial PRIMARY KEY,"videoId" integer,"keptOriginalFilename" text,"inputFilename" text,resolution integer,size bigint,"createdAt" timestamptz);
 INSERT INTO "videoSource" VALUES (1,1,'old.mp4','old upload.mp4',720,3,now()-interval '1 day'),(2,1,'retained.mp4','Camera upload.mp4',NULL,NULL,now());`)
	before := scanStrings(t, ctx, src, `SELECT row_to_json(s)::text FROM "videoSource" s ORDER BY id`)
	readCfg := src.Config().Copy()
	readCfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	reader, err := pgxpool.NewWithConfig(ctx, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	sourceDir := t.TempDir()
	seedSourceMedia(t, sourceDir)
	sourceMedia, err := storage.NewLocal(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := &observedMigrationStore{Backend: backend, reads: map[string]int{}, writes: map[string]int{}}
	plan, err := NewImporter(dest, NewSourceFromPool(reader), Options{SrcMedia: sourceMedia, DestMedia: media}).Plan(ctx, 800)
	if err != nil || plan.Entities[KindVideoFile].Planned != 1 {
		t.Fatalf("retained-original plan: report=%+v err=%v", plan, err)
	}
	run := func() *Report {
		t.Helper()
		r, err := NewImporter(dest, NewSourceFromPool(reader), Options{Policy: PolicyFail, SrcMedia: sourceMedia, DestMedia: media}).Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// Missing retained bytes must not be disguised as a successful HLS-only
	// migration or replaced with an older upload's original.
	if r := run(); r.Entities[KindVideo].Failed != 1 {
		t.Fatalf("missing retained original must fail its video: %+v", r.Entities[KindVideo])
	}
	if got := scanStrings(t, ctx, dest, `SELECT status FROM peertube_import_ledger WHERE entity_kind='video' AND source_id='11111111-1111-1111-1111-111111111111'`); strings.Join(got, ",") != "failed" {
		t.Fatalf("missing-original ledger=%v", got)
	}
	const original = "unchanged camera upload bytes"
	if _, err := sourceMedia.Put(ctx, "original-video-files/retained.mp4", strings.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	r := run()
	if r.Entities[KindVideoFile].Imported != 1 || r.Entities[KindVideo].Failed != 0 {
		t.Fatalf("original retry report: file=%+v video=%+v", r.Entities[KindVideoFile], r.Entities[KindVideo])
	}
	var key, hash, name string
	var size int64
	if err := dest.QueryRow(ctx, `SELECT storage_key,sha256,original_name,size_bytes FROM video_files WHERE kind='original'`).Scan(&key, &hash, &name, &size); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(original))
	if hash != hex.EncodeToString(wantHash[:]) || size != int64(len(original)) || name != "Camera upload.mp4" {
		t.Fatalf("retained original metadata mismatch: size=%d name=%q", size, name)
	}
	file, err := media.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(body) != original {
		t.Fatal("retained upload bytes changed")
	}
	if got := scanStrings(t, ctx, dest, `SELECT state FROM streaming_playlists WHERE video_id=(SELECT id FROM videos WHERE title='First Video')`); strings.Join(got, ",") != "ready" {
		t.Fatalf("HLS was not preserved beside the original: %v", got)
	}
	writes := media.writes[key]
	if r = run(); r.Entities[KindVideoFile].Imported != 0 || media.writes[key] != writes {
		t.Fatal("completed original was copied again")
	}
	if got := scanStrings(t, ctx, dest, `SELECT storage_key FROM video_files WHERE kind='original'`); strings.Join(got, ",") != key {
		t.Fatal("repeat changed the original's identity")
	}
	if after := scanStrings(t, ctx, src, `SELECT row_to_json(s)::text FROM "videoSource" s ORDER BY id`); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatal("source original records changed")
	}
}

func TestPeerTubeSourceKeptOriginalFallback(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	for _, schema := range []string{"absent table", "absent column", "empty table", "latest not retained"} {
		t.Run(schema, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			src, _ := newScratchDB(t, ctx, base)
			seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
			if schema == "absent column" {
				mustExec(t, ctx, src, `CREATE TABLE "videoSource" (id serial PRIMARY KEY,"videoId" integer)`)
			} else if schema != "absent table" {
				mustExec(t, ctx, src, `CREATE TABLE "videoSource" (id serial PRIMARY KEY,"videoId" integer,"keptOriginalFilename" text,"createdAt" timestamptz)`)
				if schema == "latest not retained" {
					mustExec(t, ctx, src, `INSERT INTO "videoSource" VALUES (1,1,'old.mp4',now()-interval '1 day'),(2,1,NULL,now())`)
				}
			}
			files, err := NewSourceFromPool(src).VideoFiles(ctx, 1)
			if err != nil || len(files) != 2 || files[0].Filename != "v1-720.mp4" {
				t.Fatalf("progressive fallback: files=%+v err=%v", files, err)
			}
		})
	}
}
