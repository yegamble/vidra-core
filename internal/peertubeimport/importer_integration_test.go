//go:build integration

// End-to-end PeerTube-import tests (fix_plan P18). They seed a scratch PostgreSQL
// with a KNOWN-version PeerTube schema subset + fixture rows, run the importer
// against a fresh Vidra database + a temp media store, and assert the mapping
// ledger, idempotency (re-run is a no-op), dry-run correctness, conflict
// handling, the version gate, and that NO secret (password hash / private key) is
// logged. Tiny media fixtures only.
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/peertubeimport/ -run TestPeerTubeImport
package peertubeimport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/vidra/vidra-core/internal/storage"
)

const testPassword = "correct-horse-battery-staple"

// secretPrivKey and secretHash below are seeded into the source and MUST NEVER
// appear in logs. testHash is a real bcrypt hash carried through the import.
const secretPrivKeyAlice = "PRIVKEY-ALICE-DO-NOT-LOG"

func TestPeerTubeImportEndToEnd(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	testHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(testHash), secretPrivKeyAlice)

	srcMediaDir := t.TempDir()
	destMediaDir := t.TempDir()
	seedSourceMedia(t, srcMediaDir)
	srcMedia, err := storage.NewLocal(srcMediaDir)
	if err != nil {
		t.Fatal(err)
	}
	destMedia, err := storage.NewLocal(destMediaDir)
	if err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy:    PolicySkip,
		SrcMedia:  srcMedia,
		DestMedia: destMedia,
		Logger:    logger,
	})

	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if version != 800 {
		t.Fatalf("detected version = %d, want 800", version)
	}

	// ── dry-run BEFORE the real run: reports the plan and writes nothing ──
	plan, err := imp.Plan(ctx, version)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := plan.Entities[KindUser].Planned; got != 2 {
		t.Errorf("plan users planned = %d, want 2", got)
	}
	if got := plan.Entities[KindVideo].Planned; got != 2 {
		t.Errorf("plan videos planned = %d, want 2", got)
	}
	if got := plan.Entities[KindComment].Planned; got != 2 {
		t.Errorf("plan comments planned = %d, want 2 (remote comment excluded)", got)
	}
	if got := plan.Entities[KindFollow].Planned; got != 1 {
		t.Errorf("plan follows planned = %d, want 1", got)
	}
	if n := countRows(t, ctx, dest, "users"); n != 0 {
		t.Fatalf("dry-run wrote %d users, want 0 (must write nothing)", n)
	}
	if n := countRows(t, ctx, dest, "peertube_import_ledger"); n != 0 {
		t.Fatalf("dry-run wrote %d ledger rows, want 0", n)
	}

	// ── the real run ──
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Entities[KindUser].Imported != 2 {
		t.Errorf("users imported = %d, want 2", report.Entities[KindUser].Imported)
	}
	if report.Entities[KindChannel].Imported != 2 {
		t.Errorf("channels imported = %d, want 2", report.Entities[KindChannel].Imported)
	}
	if report.Entities[KindVideo].Imported != 2 {
		t.Errorf("videos imported = %d, want 2", report.Entities[KindVideo].Imported)
	}
	if report.Entities[KindComment].Imported != 2 {
		t.Errorf("comments imported = %d, want 2", report.Entities[KindComment].Imported)
	}
	if report.Entities[KindPlaylist].Imported != 1 {
		t.Errorf("playlists imported = %d, want 1", report.Entities[KindPlaylist].Imported)
	}
	if report.Entities[KindFollow].Imported != 1 {
		t.Errorf("follows imported = %d, want 1", report.Entities[KindFollow].Imported)
	}

	// ── assert the mapped Vidra state ──
	// Only the two LOCAL users; the remote account is excluded.
	if n := countRows(t, ctx, dest, "users"); n != 2 {
		t.Fatalf("users = %d, want 2 (remote excluded)", n)
	}
	// alice: role user, password hash carried verbatim (verifies against the plaintext).
	var aliceID uuid.UUID
	var aliceRole, aliceHash string
	if err := dest.QueryRow(ctx, `SELECT id, role, password_hash FROM users WHERE username='alice'`).Scan(&aliceID, &aliceRole, &aliceHash); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	if aliceRole != "user" {
		t.Errorf("alice role = %q, want user", aliceRole)
	}
	if aliceHash != string(testHash) {
		t.Errorf("alice password hash was not carried verbatim")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(aliceHash), []byte(testPassword)); err != nil {
		t.Errorf("carried bcrypt hash does not verify the original password: %v", err)
	}
	// bob is an admin (PeerTube role 0).
	var bobRole string
	if err := dest.QueryRow(ctx, `SELECT role FROM users WHERE username='bob'`).Scan(&bobRole); err != nil {
		t.Fatalf("read bob: %v", err)
	}
	if bobRole != "admin" {
		t.Errorf("bob role = %q, want admin", bobRole)
	}
	// Federation continuity: alice's actor keypair carried (private key raw, no KEK).
	var pub, priv string
	if err := dest.QueryRow(ctx, `SELECT public_key_pem, private_key_pem FROM account_actor_keys WHERE user_id=$1`, aliceID).Scan(&pub, &priv); err != nil {
		t.Fatalf("read alice actor key: %v", err)
	}
	if pub != "PUBKEY-ALICE" || priv != secretPrivKeyAlice {
		t.Errorf("actor keypair not carried faithfully")
	}

	// channel owned by alice, handle carried.
	var chOwner uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT owner_id FROM channels WHERE handle='alice_channel'`).Scan(&chOwner); err != nil {
		t.Fatalf("read alice_channel: %v", err)
	}
	if chOwner != aliceID {
		t.Errorf("alice_channel owner mismatch")
	}

	// The public video: published, metadata, file, thumbnail, caption, tags.
	var vidID uuid.UUID
	var vidState, vidPrivacy string
	if err := dest.QueryRow(ctx, `SELECT id, state, privacy FROM videos WHERE title='First Video'`).Scan(&vidID, &vidState, &vidPrivacy); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if vidState != "published" || vidPrivacy != "public" {
		t.Errorf("video state/privacy = %s/%s, want published/public", vidState, vidPrivacy)
	}
	var dur int
	if err := dest.QueryRow(ctx, `SELECT duration_seconds FROM video_metadata WHERE video_id=$1`, vidID).Scan(&dur); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if dur != 120 {
		t.Errorf("duration = %d, want 120", dur)
	}
	// Original file row + media copied into the dest store (bytes match the source).
	var origKey string
	var origSize int64
	if err := dest.QueryRow(ctx, `SELECT storage_key, size_bytes FROM video_files WHERE video_id=$1 AND kind='original'`, vidID).Scan(&origKey, &origSize); err != nil {
		t.Fatalf("read original file: %v", err)
	}
	rc, err := destMedia.Open(ctx, origKey)
	if err != nil {
		t.Fatalf("open copied media %q: %v", origKey, err)
	}
	gotBytes, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(gotBytes) != string(sourceVideoBytes) {
		t.Errorf("copied media bytes differ from source")
	}
	if origSize != int64(len(sourceVideoBytes)) {
		t.Errorf("recorded size = %d, want %d", origSize, len(sourceVideoBytes))
	}
	if n := countRows(t, ctx, dest, "video_files"); n < 2 {
		t.Errorf("video_files = %d, want >=2 (original + thumbnail)", n)
	}
	var capLang string
	if err := dest.QueryRow(ctx, `SELECT language FROM captions WHERE video_id=$1`, vidID).Scan(&capLang); err != nil {
		t.Fatalf("read caption: %v", err)
	}
	if capLang != "en" {
		t.Errorf("caption language = %q, want en", capLang)
	}
	tags := scanStrings(t, ctx, dest, `SELECT tag FROM video_tags WHERE video_id=$1 ORDER BY tag`, vidID)
	if len(tags) != 2 || tags[0] != "music" || tags[1] != "test" {
		t.Errorf("tags = %v, want [music test]", tags)
	}

	// Threaded comments: the reply has a parent.
	var replyParent *uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT parent_id FROM comments WHERE body='A reply'`).Scan(&replyParent); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if replyParent == nil {
		t.Error("reply comment must have a parent_id")
	}

	// Playlist with 2 items.
	var plItems int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM playlist_items pi JOIN playlists p ON p.id=pi.playlist_id WHERE p.title='My Playlist'`).Scan(&plItems); err != nil {
		t.Fatalf("read playlist items: %v", err)
	}
	if plItems != 2 {
		t.Errorf("playlist items = %d, want 2", plItems)
	}

	// Follow: bob follows alice_channel.
	var follows int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM channel_follows cf JOIN channels c ON c.id=cf.channel_id JOIN users u ON u.id=cf.follower_id WHERE c.handle='alice_channel' AND u.username='bob'`).Scan(&follows); err != nil {
		t.Fatalf("read follows: %v", err)
	}
	if follows != 1 {
		t.Errorf("bob→alice_channel follows = %d, want 1", follows)
	}

	// ── idempotency: a re-run creates nothing new ──
	usersBefore := countRows(t, ctx, dest, "users")
	videosBefore := countRows(t, ctx, dest, "videos")
	commentsBefore := countRows(t, ctx, dest, "comments")
	report2, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if report2.Entities[KindUser].Imported != 0 || report2.Entities[KindVideo].Imported != 0 {
		t.Errorf("re-run imported new rows (users=%d videos=%d), want 0 (idempotent)",
			report2.Entities[KindUser].Imported, report2.Entities[KindVideo].Imported)
	}
	if report2.Entities[KindUser].Skipped != 2 {
		t.Errorf("re-run users skipped = %d, want 2", report2.Entities[KindUser].Skipped)
	}
	if countRows(t, ctx, dest, "users") != usersBefore ||
		countRows(t, ctx, dest, "videos") != videosBefore ||
		countRows(t, ctx, dest, "comments") != commentsBefore {
		t.Error("re-run changed row counts — not idempotent")
	}

	// ── no secret logged ──
	logs := logBuf.String()
	if bytes.Contains([]byte(logs), []byte(secretPrivKeyAlice)) {
		t.Error("actor private key leaked into logs")
	}
	if bytes.Contains([]byte(logs), []byte(string(testHash))) {
		t.Error("password hash leaked into logs")
	}
	if bytes.Contains([]byte(logs), []byte(testPassword)) {
		t.Error("password plaintext leaked into logs")
	}
}

func TestPeerTubeImportConflictPolicies(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	run := func(policy ConflictPolicy) *pgxpool.Pool {
		src, _ := newScratchDB(t, ctx, base)
		dest, _ := newScratchDB(t, ctx, base)
		applyMigrations(t, ctx, dest)
		seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
		// Pre-existing Vidra user 'alice' to collide with the source's alice.
		if _, err := dest.Exec(ctx, `INSERT INTO users (username, email, password_hash) VALUES ('alice','other@example.test','x')`); err != nil {
			t.Fatal(err)
		}
		imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: policy})
		v, err := imp.Preflight(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := imp.Run(ctx, v, nil); err != nil {
			t.Fatalf("run (policy=%s): %v", policy, err)
		}
		return dest
	}

	t.Run("skip keeps one alice", func(t *testing.T) {
		dest := run(PolicySkip)
		if n := countRows(t, ctx, dest, "users WHERE username LIKE 'alice%'"); n != 1 {
			t.Errorf("skip: alice-like users = %d, want 1 (source alice skipped)", n)
		}
	})

	t.Run("rename imports alice-2", func(t *testing.T) {
		dest := run(PolicyRename)
		names := scanStrings(t, ctx, dest, `SELECT username FROM users WHERE username LIKE 'alice%' ORDER BY username`)
		if len(names) != 2 || names[0] != "alice" || names[1] != "alice-2" {
			t.Errorf("rename: usernames = %v, want [alice alice-2]", names)
		}
	})
}

func TestPeerTubeImportPreflightVersionGate(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	// Seed only the application table with an UNVERIFIED (too old) version.
	mustExec(t, ctx, src, `CREATE TABLE "application" (id serial PRIMARY KEY, "migrationVersion" integer NOT NULL)`)
	mustExec(t, ctx, src, `INSERT INTO "application" ("migrationVersion") VALUES (500)`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{})
	if _, err := imp.Preflight(ctx); err == nil {
		t.Error("preflight must REFUSE an unverified (too-old) version without --force")
	}

	// A human passing --force overrides the refusal (agents must never do this).
	impForce := NewImporter(dest, NewSourceFromPool(src), Options{Force: true})
	if _, err := impForce.Preflight(ctx); err != nil {
		t.Errorf("preflight with force should proceed: %v", err)
	}
}

// ── media fixtures ──

var sourceVideoBytes = []byte("FAKE-MP4-BYTES-tiny-fixture-only")

func seedSourceMedia(t *testing.T, root string) {
	t.Helper()
	write := func(rel string, data []byte) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("web-videos/v1-720.mp4", sourceVideoBytes)
	write("thumbnails/v1-thumb.jpg", []byte("FAKE-JPG"))
	write("captions/v1-en.vtt", []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nhi\n"))
}

// ── scratch database helpers ──

func newScratchDB(t *testing.T, ctx context.Context, base string) (*pgxpool.Pool, string) {
	t.Helper()
	rnd := make([]byte, 6)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatal(err)
	}
	name := "vidra_ptimport_" + hex.EncodeToString(rnd)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch db: %v", err)
	}
	_ = admin.Close(ctx)

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("open scratch pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		a, err := pgx.Connect(dropCtx, base)
		if err != nil {
			return
		}
		defer a.Close(dropCtx)
		_, _ = a.Exec(dropCtx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	})
	return pool, name
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	dir := filepath.Join("..", "..", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ups []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".sql" && len(e.Name()) > 7 && e.Name()[len(e.Name())-7:] == ".up.sql" {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)
	for _, name := range ups {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec: %v\nSQL: %s", err, sql)
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func scanStrings(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// seedPeerTube creates the PeerTube schema subset the importer reads and inserts
// a small fixture graph. passwordHash is a real bcrypt hash carried into Vidra;
// alicePriv is a secret actor private key that must never be logged.
func seedPeerTube(t *testing.T, ctx context.Context, pool *pgxpool.Pool, passwordHash, alicePriv string) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE "application" (id serial PRIMARY KEY, "migrationVersion" integer NOT NULL)`,
		`CREATE TABLE "actor" (
			id serial PRIMARY KEY, type text NOT NULL, "preferredUsername" text NOT NULL,
			url text, "publicKey" text, "privateKey" text, "serverId" integer, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "account" (
			id serial PRIMARY KEY, name text NOT NULL, "userId" integer, "actorId" integer NOT NULL,
			description text, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "user" (
			id serial PRIMARY KEY, username text NOT NULL, email text NOT NULL, password text NOT NULL,
			role integer NOT NULL, "emailVerified" boolean, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoChannel" (
			id serial PRIMARY KEY, name text NOT NULL, description text, "accountId" integer NOT NULL,
			"actorId" integer NOT NULL, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "video" (
			id serial PRIMARY KEY, uuid uuid NOT NULL, "channelId" integer NOT NULL, name text NOT NULL,
			description text, privacy integer NOT NULL, state integer NOT NULL, category integer, licence integer,
			language text, duration integer NOT NULL DEFAULT 0, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoFile" (
			id serial PRIMARY KEY, "videoId" integer, "videoStreamingPlaylistId" integer,
			resolution integer NOT NULL, size bigint NOT NULL, extname text, fps integer, filename text)`,
		`CREATE TABLE "thumbnail" (
			id serial PRIMARY KEY, filename text NOT NULL, type integer NOT NULL, "videoId" integer,
			height integer, width integer)`,
		`CREATE TABLE "videoCaption" (
			id serial PRIMARY KEY, language text NOT NULL, filename text, "videoId" integer NOT NULL)`,
		`CREATE TABLE "videoComment" (
			id serial PRIMARY KEY, url text, text text NOT NULL, "videoId" integer NOT NULL,
			"accountId" integer NOT NULL, "inReplyToCommentId" integer, "originCommentId" integer,
			"deletedAt" timestamptz, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoPlaylist" (
			id serial PRIMARY KEY, name text NOT NULL, description text, privacy integer NOT NULL,
			uuid uuid, "ownerAccountId" integer NOT NULL, "videoChannelId" integer, type integer NOT NULL DEFAULT 1,
			"createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoPlaylistElement" (
			id serial PRIMARY KEY, position integer NOT NULL, "startTimestamp" integer, "stopTimestamp" integer,
			"videoId" integer, "videoPlaylistId" integer NOT NULL)`,
		`CREATE TABLE "tag" (id serial PRIMARY KEY, name text NOT NULL)`,
		`CREATE TABLE "videoTag" ("videoId" integer NOT NULL, "tagId" integer NOT NULL)`,
		`CREATE TABLE "actorFollow" (
			id serial PRIMARY KEY, state text NOT NULL, "actorId" integer NOT NULL,
			"targetActorId" integer NOT NULL, "createdAt" timestamptz NOT NULL DEFAULT now())`,

		// ── fixtures ──
		`INSERT INTO "application" ("migrationVersion") VALUES (800)`,
		// actors: 1 alice(Person,local) 2 alice_channel(Group,local) 3 bob(Person,local)
		// 4 bob_channel(Group,local) 5 remote(Person,serverId=1)
		`INSERT INTO "actor" (id,type,"preferredUsername","publicKey","privateKey","serverId") VALUES
			(1,'Person','alice','PUBKEY-ALICE','` + alicePriv + `',NULL),
			(2,'Group','alice_channel','PUBKEY-ACH','PRIVKEY-ACH-SECRET',NULL),
			(3,'Person','bob','PUBKEY-BOB','PRIVKEY-BOB-SECRET',NULL),
			(4,'Group','bob_channel','PUBKEY-BCH','PRIVKEY-BCH-SECRET',NULL),
			(5,'Person','remote','PUBKEY-R',NULL,1)`,
		`INSERT INTO "user" (id,username,email,password,role,"emailVerified") VALUES
			(1,'alice','alice@example.test','` + passwordHash + `',2,true),
			(2,'bob','bob@example.test','` + passwordHash + `',0,false)`,
		`INSERT INTO "account" (id,name,"userId","actorId") VALUES
			(1,'Alice',1,1),(2,'Bob',2,3),(5,'Remote',NULL,5)`,
		`INSERT INTO "videoChannel" (id,name,description,"accountId","actorId") VALUES
			(1,'Alice Channel','a desc',1,2),(2,'Bob Channel','',2,4)`,
		`INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,category,licence,language,duration) VALUES
			(1,'11111111-1111-1111-1111-111111111111',1,'First Video','hello',1,1,1,1,'en',120),
			(2,'22222222-2222-2222-2222-222222222222',1,'Second Video','',3,1,NULL,NULL,NULL,60)`,
		`INSERT INTO "videoFile" (id,"videoId",resolution,size,extname,filename) VALUES
			(1,1,720,` + strconv.Itoa(len(sourceVideoBytes)) + `,'.mp4','v1-720.mp4'),
			(2,1,480,10,'.mp4','v1-480.mp4')`,
		`INSERT INTO "thumbnail" (id,filename,type,"videoId") VALUES (1,'v1-thumb.jpg',1,1)`,
		`INSERT INTO "videoCaption" (id,language,filename,"videoId") VALUES (1,'en','v1-en.vtt',1)`,
		`INSERT INTO "tag" (id,name) VALUES (1,'music'),(2,'test')`,
		`INSERT INTO "videoTag" ("videoId","tagId") VALUES (1,1),(1,2)`,
		// comment 1 by alice (top-level), 2 by bob (reply), 3 by remote (excluded).
		`INSERT INTO "videoComment" (id,text,"videoId","accountId","inReplyToCommentId") VALUES
			(1,'Great video',1,1,NULL),
			(2,'A reply',1,2,1),
			(3,'remote spam',1,5,NULL)`,
		`INSERT INTO "videoPlaylist" (id,name,description,privacy,"ownerAccountId",type) VALUES
			(1,'My Playlist','',1,1,1),
			(2,'Watch Later','',3,1,2)`,
		`INSERT INTO "videoPlaylistElement" (id,position,"videoId","videoPlaylistId") VALUES
			(1,1,1,1),(2,2,2,1)`,
		// bob (actor 3) follows alice_channel (actor 2).
		`INSERT INTO "actorFollow" (id,state,"actorId","targetActorId") VALUES (1,'accepted',3,2)`,
	}
	for _, s := range stmts {
		mustExec(t, ctx, pool, s)
	}
}
