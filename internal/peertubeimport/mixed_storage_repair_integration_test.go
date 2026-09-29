//go:build integration

package peertubeimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

type observedMigrationStore struct {
	storage.Backend
	mu     sync.Mutex
	reads  map[string]int
	writes map[string]int
}

func (s *observedMigrationStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	s.reads[key]++
	s.mu.Unlock()
	return s.Backend.Open(ctx, key)
}

func (s *observedMigrationStore) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	s.mu.Lock()
	s.writes[key]++
	s.mu.Unlock()
	return s.Backend.Put(ctx, key, r)
}

// Two disjoint local stores model object storage and staged source files. This
// tests ledger/backend switching, not a provider's S3 transport or copy API.
func TestPeerTubeImportThreePassMixedStorageRepair(t *testing.T) {
	for _, tc := range []struct {
		name       string
		replaceIDs bool
	}{{"same artwork IDs", false}, {"new artwork IDs", true}} {
		t.Run(tc.name, func(t *testing.T) { testThreePassMixedStorageRepair(t, tc.replaceIDs) })
	}
}

func testThreePassMixedStorageRepair(t *testing.T, replaceArtworkIDs bool) {
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
	var imageRequests atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		imageRequests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer imageServer.Close()
	mustExec(t, ctx, src, `UPDATE actor SET url=$1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, imageServer.URL)
	mustExec(t, ctx, src, `INSERT INTO video (id,uuid,"channelId",name,privacy,state,duration,views) VALUES
		(3,'33333333-3333-3333-3333-333333333333',1,'Local Public',1,2,30,0),
		(4,'44444444-4444-4444-4444-444444444444',1,'Local Private',3,7,30,0)`)
	mustExec(t, ctx, src, `INSERT INTO "videoFile" (id,"videoId",resolution,size,extname,filename) VALUES
		(10,3,720,12,'.mp4','v3.mp4'),(11,4,720,13,'.mp4','v4.mp4')`)
	mustExec(t, ctx, src, `INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES
		(3,'en','v3.vtt',3),(4,'en','v4.vtt',4)`)
	mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES (4,4,'v4-master.m3u8')`)
	objectDir, localDir := t.TempDir(), t.TempDir()
	seedSourceMedia(t, objectDir)
	write := func(root, key, body string) {
		t.Helper()
		p := filepath.Join(root, key)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(localDir, "web-videos/v3.mp4", "local-public")
	write(localDir, "web-videos/private/v4.mp4", "local-private")
	write(objectDir, "captions/v3.vtt", "WEBVTT\nsource-three\n")
	write(objectDir, "captions/v4.vtt", "WEBVTT\nsource-four\n")
	const lateHLS = "streaming-playlists/hls/44444444-4444-4444-4444-444444444444/"
	write(objectDir, lateHLS+"v4-master.m3u8", "#EXTM3U\n#EXTINF:30,\nv4.mp4\n#EXT-X-ENDLIST\n")
	write(objectDir, lateHLS+"v4.mp4", "source-hls-four")
	observe := func(root string) *observedMigrationStore {
		t.Helper()
		b, err := storage.NewLocal(root)
		if err != nil {
			t.Fatal(err)
		}
		return &observedMigrationStore{Backend: b, reads: map[string]int{}, writes: map[string]int{}}
	}
	objects, local, destination := observe(objectDir), observe(localDir), observe(t.TempDir())
	run := func(source storage.Backend) *Report {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicyFail, SrcMedia: source, DestMedia: destination})
		r, err := imp.Run(ctx, 800, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := run(objects)
	if got := first.Entities[KindVideo]; got.Imported != 2 || got.Failed != 2 {
		t.Fatalf("object-only video counts = %+v", got)
	}
	existingIDs := scanStrings(t, ctx, dest, `SELECT id::text FROM videos ORDER BY id`)
	firstImageRequests := imageRequests.Load()
	mustExec(t, ctx, dest, `UPDATE videos SET title='Creator edit' WHERE title='First Video'`)
	mustExec(t, ctx, dest, `DELETE FROM user_images WHERE user_id=(SELECT id FROM users WHERE username='alice') AND kind='avatar'`)
	mustExec(t, ctx, dest, `UPDATE user_images SET size_bytes=4242,updated_at=now() WHERE user_id=(SELECT id FROM users WHERE username='bob') AND kind='avatar'`)
	mustExec(t, ctx, dest, `DELETE FROM captions`)
	mustExec(t, ctx, dest, `DELETE FROM video_files WHERE kind IN ('storyboard','storyboard_vtt')`)
	second := run(local)
	if got := second.Entities[KindVideo]; got.Imported != 2 || got.Failed != 0 {
		t.Fatalf("local repair video counts = %+v", got)
	}
	if got := second.Entities[KindCaption]; got.Failed != 2 || got.Imported != 0 {
		t.Fatalf("local repair must leave S3-only captions failed: %+v", got)
	}
	if got := second.Entities[KindHLSPlaylist]; got.Failed != 1 || got.Imported != 0 {
		t.Fatalf("local repair must leave S3-only HLS failed: %+v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT state || ':' || privacy FROM videos WHERE title LIKE 'Local %' ORDER BY title`); strings.Join(got, ",") != "draft:private,draft:public" {
		t.Fatalf("source state/privacy changed: %v", got)
	}
	for _, original := range []string{"local-public", "local-private"} {
		sum := sha256.Sum256([]byte(original))
		if got := scanStrings(t, ctx, dest, `SELECT sha256 FROM video_files WHERE sha256=$1`, hex.EncodeToString(sum[:])); len(got) != 1 {
			t.Fatalf("local original digest missing for %q", original)
		}
	}
	mustExec(t, ctx, dest, `INSERT INTO captions(video_id,language,storage_key)
		SELECT id,'en','captions/creator.vtt' FROM videos WHERE title='Local Public'`)
	// A source-side replacement gets a new row ID. A creator's cleared slot
	// must stay cleared even when no ledger row exists for that new source ID.
	if replaceArtworkIDs {
		mustExec(t, ctx, src, `UPDATE "actorImage" SET id=99,filename='alice-avatar-new.png' WHERE id=1`)
		mustExec(t, ctx, src, `UPDATE storyboard SET id=99,filename='v1-storyboard-new.jpg' WHERE id=1`)
		write(objectDir, "storyboards/v1-storyboard-new.jpg", string(jpegBytes))
	}
	allIDs := scanStrings(t, ctx, dest, `SELECT id::text FROM videos ORDER BY id`)
	third := run(objects)
	if got := third.Entities[KindVideo]; got.Imported != 0 || got.Failed != 0 {
		t.Fatalf("final object pass recopied video rows: %+v", got)
	}
	if got := third.Entities[KindCaption]; got.Imported != 1 || got.Failed != 0 {
		t.Fatalf("final captions = %+v", got)
	}
	if got := third.Entities[KindHLSPlaylist]; got.Imported != 1 || got.Failed != 0 {
		t.Fatalf("final HLS = %+v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT id::text FROM videos ORDER BY id`); strings.Join(got, ",") != strings.Join(allIDs, ",") {
		t.Fatal("final pass replaced destination videos")
	}
	for _, id := range existingIDs {
		if !strings.Contains(strings.Join(allIDs, ","), id) {
			t.Fatal("local pass replaced an existing video")
		}
	}
	if got := scanStrings(t, ctx, dest, `SELECT title FROM videos WHERE title='Creator edit'`); len(got) != 1 {
		t.Fatal("creator metadata was overwritten")
	}
	if got := scanStrings(t, ctx, dest, `SELECT storage_key FROM captions WHERE storage_key='captions/creator.vtt'`); len(got) != 1 {
		t.Fatal("creator caption was overwritten")
	}
	if got := scanStrings(t, ctx, dest, `SELECT status FROM peertube_import_ledger WHERE status='failed'`); len(got) != 0 {
		t.Fatalf("unexpected residual failures: %v", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT kind FROM user_images WHERE user_id=(SELECT id FROM users WHERE username='alice') AND kind='avatar'`); len(got) != 0 {
		t.Fatal("creator-cleared avatar was restored")
	}
	if got := scanStrings(t, ctx, dest, `SELECT size_bytes::text FROM user_images WHERE user_id=(SELECT id FROM users WHERE username='bob') AND kind='avatar'`); len(got) != 1 || got[0] != "4242" {
		t.Fatal("creator-owned avatar was overwritten")
	}
	if got := imageRequests.Load(); got != firstImageRequests {
		t.Fatalf("actor artwork retransferred: %d requests became %d", firstImageRequests, got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT kind FROM video_files WHERE kind IN ('storyboard','storyboard_vtt')`); len(got) != 0 {
		t.Fatal("creator-cleared storyboard was restored")
	}
	const firstMaster = "streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-master.m3u8"
	if objects.reads[firstMaster] != 1 || objects.reads["web-videos/v1-720.mp4"] != 1 || objects.reads["captions/v3.vtt"] != 0 {
		t.Fatal("completed media or creator-replaced caption was fetched again")
	}
	if objects.reads["thumbnails/v1-thumb.jpg"] != 1 || objects.reads["storyboards/v1-storyboard.jpg"] != 1 {
		t.Fatal("existing or creator-cleared artwork was fetched again")
	}
	if objects.reads["storyboards/v1-storyboard-new.jpg"] != 0 {
		t.Fatal("new source artwork was fetched for a creator-cleared slot")
	}
	for key, count := range destination.writes {
		if count != 1 {
			t.Fatalf("destination object %q was written %d times", key, count)
		}
	}
}
