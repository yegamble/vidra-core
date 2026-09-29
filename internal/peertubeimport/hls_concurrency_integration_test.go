//go:build integration

package peertubeimport

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestHLSCopyPreservesConcurrentReadyPlaylist(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, db)
	owner, channel := uuid.New(), uuid.New()
	mustExec(t, ctx, db, `INSERT INTO users(id,username,email,password_hash) VALUES($1,'hls-worker','hls-worker@example.invalid','fixture')`, owner)
	mustExec(t, ctx, db, `INSERT INTO channels(id,owner_id,handle,display_name) VALUES($1,$2,'hls-channel','HLS worker')`, channel, owner)
	for _, scenario := range []string{"native-wins", "repair-pending", "repair-failed", "media-fails"} {
		t.Run(scenario, func(t *testing.T) {
			id := uuid.New()
			mustExec(t, ctx, db, `INSERT INTO videos(id,channel_id,title) VALUES($1,$2,'HLS fixture')`, id, channel)
			q := sqlcgen.New(db)
			if scenario == "repair-pending" || scenario == "repair-failed" {
				state := strings.TrimPrefix(scenario, "repair-")
				mustExec(t, ctx, db, `INSERT INTO streaming_playlists(video_id,master_key,state) VALUES($1,'incomplete.m3u8',$2)`, id, state)
			}
			src, _ := storage.NewLocal(t.TempDir())
			dst, _ := storage.NewLocal(t.TempDir())
			target := hlsCopyTarget{sourceID: id.String(), videoID: id, prefix: sourceHLSDir(id.String()), master: "master.m3u8"}
			if _, err := src.Put(ctx, target.prefix+"/master.m3u8", strings.NewReader("#EXTM3U\nclip.mp4\n")); err != nil {
				t.Fatal(err)
			}
			im := &Importer{dest: db, q: q, srcMedia: src, destMedia: dst, copyMediaServer: copyFunc(func(ctx context.Context, _ storage.Backend, _, _ string, _ int64) (int64, error) {
				if scenario == "media-fails" {
					return 0, errors.New("fixture media failure")
				}
				if scenario == "native-wins" {
					// The initial ready check has already happened; Vidra's own
					// transcode completes while the source tree is being copied.
					_, err := q.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{VideoID: id, MasterKey: "native/master.m3u8", State: "ready", Format: "cmaf"})
					return 5, err
				}
				return 5, nil
			})}
			counts, err := runHLSCopyWorkers(ctx, []hlsCopyTarget{target}, im.copyHLSTarget)
			if err != nil {
				t.Fatal(err)
			}
			var ledgerStatus string
			var ledgerCount int
			if err := db.QueryRow(ctx, `SELECT count(*), COALESCE(min(status),'') FROM peertube_import_ledger WHERE entity_kind='hls_playlist' AND source_id=$1`, id.String()).Scan(&ledgerCount, &ledgerStatus); err != nil {
				t.Fatal(err)
			}
			if scenario == "media-fails" {
				ready, err := q.ImportVideoHasReadyPlaylist(ctx, id)
				if err != nil || ready || counts.Failed != 1 || ledgerStatus != "failed" {
					t.Fatalf("media failure published or lost ledger: ready=%v counts=%+v ledger=%s error=%v", ready, counts, ledgerStatus, err)
				}
				return
			}
			var master, format string
			if err := db.QueryRow(ctx, `SELECT master_key,format FROM streaming_playlists WHERE video_id=$1`, id).Scan(&master, &format); err != nil {
				t.Fatal(err)
			}
			if scenario == "native-wins" {
				if master != "native/master.m3u8" || format != "cmaf" || counts.Skipped != 1 || counts.Imported != 0 || ledgerCount != 0 {
					t.Fatalf("concurrent native tree overwritten/claimed: master=%s format=%s counts=%+v ledger=%d", master, format, counts, ledgerCount)
				}
			} else if master != target.prefix+"/master.m3u8" || format != "hls-ts" || counts.Imported != 1 || ledgerStatus != "done" {
				t.Fatalf("incomplete tree not repaired: master=%s format=%s counts=%+v ledger=%s", master, format, counts, ledgerStatus)
			}
		})
	}
}
