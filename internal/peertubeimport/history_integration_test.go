//go:build integration

package peertubeimport

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPeerTubeImportWatchHistory(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, "fixture-hash", secretPrivKeyAlice)
	mustExec(t, ctx, src, `ALTER TABLE "user" ADD COLUMN "videosHistoryEnabled" boolean DEFAULT true; UPDATE "user" SET "videosHistoryEnabled"=false WHERE id=1;
 INSERT INTO "videoChannel" (id,name,"accountId","actorId") VALUES (3,'Remote',5,5);
 INSERT INTO video (id,uuid,"channelId",name,privacy,state) SELECT n,gen_random_uuid(),3,'Remote',1,1 FROM generate_series(3,1002) n;
 CREATE TABLE "userVideoHistory" (id bigint PRIMARY KEY,"userId" integer,"videoId" integer,"currentTime" integer,"createdAt" timestamptz DEFAULT '2020-01-01Z',"updatedAt" timestamptz DEFAULT '2020-01-02Z');
 INSERT INTO "userVideoHistory" (id,"userId","videoId","currentTime") SELECT n,1,n+2,1 FROM generate_series(1,1000) n;
 INSERT INTO "userVideoHistory" (id,"userId","videoId","currentTime") VALUES (1001,1,1,42),(1002,1,2,-5);`)
	imp := NewImporter(dest, NewSourceFromPool(src), Options{})
	plan, err := imp.Plan(ctx, 900)
	if err != nil {
		t.Fatal(err)
	}
	if c := plan.Entities["watch_history"]; c == nil || c.Planned != 2 || c.Unsupported != 1000 {
		t.Fatalf("missing history plan: %+v", c)
	}
	if countRows(t, ctx, dest, "watch_history") != 0 || countRows(t, ctx, dest, "peertube_import_ledger") != 0 {
		t.Fatal("dry run wrote data")
	}
	report := NewReport(false, PolicySkip, false)
	for _, step := range []func(context.Context, *Report) error{imp.importUsers, imp.importChannels, imp.importVideos} {
		if err := step(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ctx, dest, `INSERT INTO watch_history(user_id,video_id,position_seconds) SELECT u.id,v.id,91 FROM users u,videos v WHERE u.username='alice' AND v.title='Second Video'`)
	run := func(imported, skipped int) {
		t.Helper()
		r, err := imp.Run(ctx, 900, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c := r.Entities["watch_history"]; c.Imported != imported || c.Skipped != skipped || c.Unsupported != 1000 {
			t.Fatalf("history result: %+v", c)
		}
	}
	run(1, 1)
	if countRows(t, ctx, dest, "watch_history WHERE position_seconds=91") != 1 {
		t.Fatal("existing destination history was overwritten")
	}
	var matches int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM watch_history h JOIN users u ON u.id=h.user_id JOIN videos v ON v.id=h.video_id WHERE u.username='alice' AND NOT u.history_enabled AND v.title='First Video' AND h.position_seconds=42 AND h.created_at='2020-01-01Z' AND h.updated_at='2020-01-02Z'`).Scan(&matches); err != nil || matches != 1 {
		t.Fatalf("history or preference mismatch: %d %v", matches, err)
	}
	mustExec(t, ctx, dest, `UPDATE watch_history SET position_seconds=99; UPDATE users SET history_enabled=true WHERE username='alice'`)
	imp.sourceAuthoritative = true
	run(0, 2)
	if countRows(t, ctx, dest, "watch_history WHERE position_seconds=99") != 2 || countRows(t, ctx, dest, "users WHERE username='alice' AND history_enabled") != 1 {
		t.Fatal("rerun overwrote destination edits")
	}
	// Clearing history is permanent even when PeerTube recreates the pair under a new row ID.
	mustExec(t, ctx, dest, `DELETE FROM watch_history`)
	mustExec(t, ctx, src, `UPDATE "userVideoHistory" SET id=id+10000 WHERE id>1000`)
	run(0, 2)
	if countRows(t, ctx, dest, "watch_history") != 0 {
		t.Fatal("rerun resurrected cleared history")
	}
	mustExec(t, ctx, src, `INSERT INTO "userVideoHistory" (id,"userId","videoId","currentTime") VALUES (20000,2,1,-7)`)
	// A checkpoint failure must roll back the associated native history insert.
	mustExec(t, ctx, dest, `ALTER TABLE peertube_import_ledger ADD CONSTRAINT reject_history CHECK(entity_kind<>'watch_history') NOT VALID`)
	if _, err := imp.Run(ctx, 900, nil); err == nil {
		t.Fatal("expected ledger failure")
	}
	if countRows(t, ctx, dest, "watch_history") != 0 {
		t.Fatal("partial history commit")
	}
	mustExec(t, ctx, dest, `ALTER TABLE peertube_import_ledger DROP CONSTRAINT reject_history`)
	run(1, 2)
	if countRows(t, ctx, dest, "watch_history WHERE position_seconds=0") != 1 {
		t.Fatal("negative position not clamped")
	}
}
