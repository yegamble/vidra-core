//go:build integration

package peertubeimport

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type pausedProgressSource struct {
	storage.Backend
	key     string
	release <-chan struct{}
}

func (s pausedProgressSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == s.key {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Backend.Open(ctx, key)
}

// The same GetRun used by the admin UI must advance before the slow final
// tree finishes, for both successful copies and recoverable copy failures.
func TestImportProgressVisibleDuringHLSCopy(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "copied", true: "failed"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			src, _ := newScratchDB(t, ctx, base)
			dest, _ := newScratchDB(t, ctx, base)
			applyMigrations(t, ctx, dest)
			seedPeerTube(t, ctx, src, "fixture-password-hash", secretPrivKeyAlice)
			mustExec(t, ctx, src, `DELETE FROM "actorImage"`)
			mustExec(t, ctx, src, `INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES (2,2,'slow.m3u8')`)
			root := t.TempDir()
			seedSourceMedia(t, root)
			media, _ := storage.NewLocal(root)
			dst, _ := storage.NewLocal(t.TempDir())
			var second string
			if err := src.QueryRow(ctx, `SELECT uuid::text FROM video WHERE id=2`).Scan(&second); err != nil {
				t.Fatal(err)
			}
			slowKey := sourceHLSDir(second) + "/slow.m3u8"
			if _, err := media.Put(ctx, slowKey, strings.NewReader("#EXTM3U\n#EXT-X-ENDLIST\n")); err != nil {
				t.Fatal(err)
			}
			if fail {
				if err := media.Delete(ctx, sourceHLSDir("11111111-1111-1111-1111-111111111111")+"/v1-init.mp4"); err != nil {
					t.Fatal(err)
				}
			}
			release := make(chan struct{})
			svc := NewService(sqlcgen.New(dest), WithImporterFactory(func(_ context.Context, p RunParams) (*Importer, func(), error) {
				return NewImporter(dest, NewSourceFromPool(src), Options{Policy: p.Policy, MediaMode: p.MediaMode, SrcMedia: pausedProgressSource{media, slowKey, release}, DestMedia: dst}), func() {}, nil
			}))
			run, err := svc.CreateRun(ctx, Launch{Mode: "run", Policy: PolicyFail, MediaMode: MediaModeCopy}, uuid.Nil)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var workerErr error
			go func() { _, workerErr = svc.DrainDueRuns(ctx, 1); close(done) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(4 * time.Second)
			for {
				got, err := svc.GetRun(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Report != nil {
					c := got.Report.Entities[KindHLSPlaylist]
					if c.Imported+c.Failed == 1 {
						if got.State != "running" || fail && c.Failed != 1 || !fail && c.Imported != 1 {
							t.Fatalf("unexpected live progress: state=%s counts=%+v", got.State, c)
						}
						close(release)
						<-done
						final, err := svc.GetRun(ctx, run.ID)
						if err != nil || workerErr != nil || final.State != "done" {
							t.Fatalf("completion: state=%s read=%v worker=%v", final.State, err, workerErr)
						}
						finalCounts := final.Report.Entities[KindHLSPlaylist]
						if finalCounts.Imported+finalCounts.Failed != 2 || finalCounts.Failed != c.Failed {
							t.Fatalf("double-counted or lost live outcomes: %+v", finalCounts)
						}
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("GetRun stayed stale while a second tree was still copying: %+v", got.Report)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
