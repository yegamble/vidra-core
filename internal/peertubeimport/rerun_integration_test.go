//go:build integration

package peertubeimport

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// The documented workflow is REPEATED runs against a still-live PeerTube up to a
// cutover. Every other re-run test here proves what a second run must NOT do —
// duplicate, double a counter, resurrect a deletion. These two prove what it
// MUST do: carry what the source gained since the last run.

// addCarol gives the source a third local account with a channel of her own and
// one video on it.
func addCarol(t *testing.T, ctx context.Context, src *pgxpool.Pool, passwordHash string) {
	t.Helper()
	mustExec(t, ctx, src, `INSERT INTO "actor" (id,type,"preferredUsername","publicKey","privateKey","serverId") VALUES
		(6,'Person','carol','PUBKEY-CAROL','PRIVKEY-CAROL-SECRET',NULL),
		(7,'Group','carol_channel','PUBKEY-CCH','PRIVKEY-CCH-SECRET',NULL)`)
	mustExec(t, ctx, src, `INSERT INTO "user" (id,username,email,password,role,"emailVerified")
		VALUES (3,'carol','carol@example.test',$1,2,true)`, passwordHash)
	mustExec(t, ctx, src, `INSERT INTO "account" (id,name,"userId","actorId") VALUES (6,'Carol',3,6)`)
	mustExec(t, ctx, src, `INSERT INTO "videoChannel" (id,name,description,"accountId","actorId")
		VALUES (3,'Carol Channel','',6,7)`)
	mustExec(t, ctx, src, `INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,duration,views)
		VALUES (3,'33333333-3333-3333-3333-333333333333',3,'Carol Video','',1,1,30,0)`)
}

// TestPeerTubeImportRerunCarriesWhatTheSourceGained is the plain delta: nothing
// fails, the source simply gains what a live instance gains in an ordinary week
// — a new account (carol) with her own channel and a video on it, a new video on
// a channel that was ALREADY imported, new comments on an old video and on a new
// one, and a new subscription.
func TestPeerTubeImportRerunCarriesWhatTheSourceGained(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := map[string]int{}
	for _, table := range []string{"users", "channels", "videos", "comments"} {
		before[table] = countRows(t, ctx, dest, table)
	}

	// ── the still-live source grows ──
	addCarol(t, ctx, src, string(hash))
	mustExec(t, ctx, src, `INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,duration,views)
		VALUES (4,'44444444-4444-4444-4444-444444444444',1,'Alice Third Video','',1,1,45,0)`)
	mustExec(t, ctx, src, `INSERT INTO "videoComment" (id,text,"videoId","accountId","inReplyToCommentId") VALUES
		(10,'carol on an old video',1,6,NULL),
		(11,'alice on a new video',3,1,NULL)`)
	mustExec(t, ctx, src, `INSERT INTO "actorFollow" (id,state,"actorId","targetActorId") VALUES (10,'accepted',6,2)`)

	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	for kind, want := range map[string]int{KindUser: 1, KindChannel: 1, KindVideo: 2, KindComment: 2, KindFollow: 1} {
		if got := report.Entities[kind].Imported; got != want {
			t.Errorf("second run imported %d %s, want %d — the delta", got, kind, want)
		}
		if got := report.Entities[kind].Failed; got != 0 {
			t.Errorf("second run failed %d %s, want 0", got, kind)
		}
	}
	for table, gained := range map[string]int{"users": 1, "channels": 1, "videos": 2, "comments": 2} {
		if n := countRows(t, ctx, dest, table); n != before[table]+gained {
			t.Errorf("%s = %d, want %d", table, n, before[table]+gained)
		}
	}
	// The new video on the OLD channel hangs off the channel row the first run
	// made, and carol's video off the channel this run made.
	if got := scanStrings(t, ctx, dest, `
		SELECT c.handle FROM videos v JOIN channels c ON c.id = v.channel_id
		WHERE v.title IN ('Alice Third Video','Carol Video') ORDER BY v.title`); len(got) != 2 ||
		got[0] != "alice_channel" || got[1] != "carol_channel" {
		t.Errorf("new videos landed on channels %v, want [alice_channel carol_channel]", got)
	}

	// A third run is the no-op again.
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	for _, kind := range []string{KindUser, KindChannel, KindVideo, KindComment, KindFollow} {
		if got := report.Entities[kind].Imported; got != 0 {
			t.Errorf("third run imported %d %s, want 0", got, kind)
		}
	}
}

// TestPeerTubeImportRerunRecoversTheChildrenOfAFailedParent is the run that goes
// wrong in the middle. A per-entity failure is NON-terminal by design — the next
// run retries it — so an account that failed on Monday arrives on Tuesday. What
// has to arrive WITH it is everything that hangs off it: its channel, the
// channel's videos, the comments on them.
//
// The failure injected here is a sealer that refuses one key once (a KEK blip).
// Any per-row failure has the same shape: a copy-mode object the store could not
// read, a constraint the row trips until somebody fixes it on the source.
func TestPeerTubeImportRerunRecoversTheChildrenOfAFailedParent(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
	addCarol(t, ctx, src, string(hash))
	mustExec(t, ctx, src, `INSERT INTO "videoComment" (id,text,"videoId","accountId","inReplyToCommentId") VALUES
		(10,'alice on carols video',3,1,NULL),
		(11,'carol on an old video',1,6,NULL)`)
	mustExec(t, ctx, src, `INSERT INTO "videoPlaylist" (id,name,description,privacy,"ownerAccountId",type)
		VALUES (10,'Carol Playlist','',1,6,1)`)
	mustExec(t, ctx, src, `INSERT INTO "actorFollow" (id,state,"actorId","targetActorId") VALUES
		(10,'accepted',6,2),
		(11,'accepted',1,7)`)

	var blip atomic.Bool
	blip.Store(true)
	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy: PolicySkip, MediaMode: MediaModeNone,
		SealKey: func(pem string) (string, error) {
			if blip.Load() && strings.Contains(pem, "CAROL") {
				return "", errors.New("kek unavailable")
			}
			return "sealed:" + pem, nil
		},
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := report.Entities[KindUser].Failed; got != 1 {
		t.Fatalf("fixture integrity: first run failed %d users, want exactly carol", got)
	}
	if n := countRows(t, ctx, dest, "users"); n != 2 {
		t.Fatalf("fixture integrity: users after the first run = %d, want alice and bob only", n)
	}
	// Waiting on a parent leaves NO ledger row: a row is what made the wait
	// permanent, and a re-recorded one would be a write per child on every run.
	var waiting int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM peertube_import_ledger WHERE note LIKE '%not imported'`).Scan(&waiting); err != nil {
		t.Fatalf("count waiting rows: %v", err)
	}
	if waiting != 0 {
		t.Errorf("the first run recorded %d 'not imported' ledger rows, want 0", waiting)
	}
	// ...but every instance migrated by an OLDER release already holds them,
	// terminal, exactly as that release wrote them. They must heal too.
	mustExec(t, ctx, dest, `INSERT INTO peertube_import_ledger (entity_kind, source_id, status, note) VALUES
		('channel','3','skipped','owner user not imported'),
		('video','33333333-3333-3333-3333-333333333333','skipped','channel not imported'),
		('comment','10','skipped','video not imported'),
		('comment','11','skipped','author not imported'),
		('playlist','10','skipped','owner not imported'),
		('follow','3:1','skipped','follower not imported'),
		('follow','1:3','skipped','channel not imported')`)

	// The blip passes. Nothing else changes.
	blip.Store(false)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if got := report.Entities[KindUser].Imported; got != 1 {
		t.Fatalf("second run imported %d users, want carol (a failed row is retried)", got)
	}
	for _, check := range []struct{ what, sql string }{
		{"carol's channel", `SELECT count(*) FROM channels WHERE handle='carol_channel'`},
		{"carol's video", `SELECT count(*) FROM videos WHERE title='Carol Video'`},
		{"the comment ON carol's video", `SELECT count(*) FROM comments WHERE body='alice on carols video'`},
		{"the comment BY carol", `SELECT count(*) FROM comments WHERE body='carol on an old video'`},
		{"carol's playlist", `SELECT count(*) FROM playlists WHERE title='Carol Playlist'`},
		{"carol's subscription to alice_channel", `
			SELECT count(*) FROM channel_follows f JOIN users u ON u.id = f.follower_id
			JOIN channels c ON c.id = f.channel_id WHERE u.username='carol' AND c.handle='alice_channel'`},
		{"alice's subscription to carol_channel", `
			SELECT count(*) FROM channel_follows f JOIN users u ON u.id = f.follower_id
			JOIN channels c ON c.id = f.channel_id WHERE u.username='alice' AND c.handle='carol_channel'`},
	} {
		var n int
		if err := dest.QueryRow(ctx, check.sql).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", check.what, err)
		}
		if n != 1 {
			t.Errorf("%s: %d rows after the recovering run, want 1 — a parent that failed once "+
				"and then imported must bring its children with it", check.what, n)
		}
	}
}
