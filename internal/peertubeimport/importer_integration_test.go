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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// sourceCategorySettings is a categories-plugin settings blob in the shape a
// LIVE instance stores: the taxonomy sits in the JSON settings column as a
// STRING that is itself JSON, so it has to be decoded twice. The stock ids are
// all deleted and the instance's own are added above them.
const sourceCategorySettings = `{"json-categories-as-text": "{\"add\":[{\"key\":51,\"label\":\"Giantess\"},{\"key\":52,\"label\":\"Shrunken\"}],\"delete\":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18]}"}`

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
	if got := plan.Entities[KindHLSPlaylist].Planned; got != 1 {
		t.Errorf("plan hls playlists planned = %d, want 1", got)
	}
	if got := plan.Entities[KindComment].Planned; got != 2 {
		t.Errorf("plan comments planned = %d, want 2 (remote comment excluded)", got)
	}
	if got := plan.Entities[KindFollow].Planned; got != 1 {
		t.Errorf("plan follows planned = %d, want 1", got)
	}
	// The per-video families are in the plan too, so --dry-run shows what a run
	// would carry. view_count counts VIDEOS carrying a total, never views.
	if got := plan.Entities[KindViewCount].Planned; got != 1 {
		t.Errorf("plan view counts planned = %d, want 1 (only video 1 has views)", got)
	}
	if got := plan.Entities[KindChapter].Planned; got != 3 {
		t.Errorf("plan chapters planned = %d, want 3", got)
	}
	if got := plan.Entities[KindRating].Planned; got != 3 {
		t.Errorf("plan ratings planned = %d, want 3 (remote account's rating excluded)", got)
	}
	if got := plan.Entities[KindRendition].Planned; got != 2 {
		t.Errorf("plan renditions planned = %d, want 2 (audio-only rung is not a rung)", got)
	}
	// The instance's own category taxonomy: one setting, planned once.
	if got := plan.Entities[KindCategoryTaxonomy].Planned; got != 1 {
		t.Errorf("plan category taxonomy planned = %d, want 1 (this source replaces the stock list)", got)
	}
	if n := countRows(t, ctx, dest, "instance_settings"); n != 0 {
		t.Fatalf("dry-run wrote %d instance settings, want 0", n)
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
	if report.Entities[KindCategoryTaxonomy].Imported != 1 {
		t.Errorf("category taxonomy imported = %d, want 1", report.Entities[KindCategoryTaxonomy].Imported)
	}
	// The source's taxonomy is now the instance's, which is what makes the
	// category ids the videos carry mean something. Video 1 carries category 1 —
	// deleted on the source — so it reads as no category rather than as "Music",
	// exactly as it did on the instance it came from.
	if got := readInstanceSetting(t, ctx, dest, "instance_custom_categories"); got != `["51:Giantess","52:Shrunken"]` {
		t.Errorf("instance_custom_categories = %s, want the source's own two categories", got)
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

	// Per-video data: the view total, the chapters, the ratings.
	var views int64
	if err := dest.QueryRow(ctx, `SELECT views FROM video_view_counts WHERE video_id=$1`, vidID).Scan(&views); err != nil {
		t.Fatalf("read view count: %v", err)
	}
	if views != 100 {
		t.Errorf("views = %d, want the source's 100", views)
	}
	// The day rollup is deliberately EMPTY: the source carries one lifetime total
	// and no daily breakdown, so writing a bucket would invent a history.
	if n := countRows(t, ctx, dest, "video_view_days"); n != 0 {
		t.Errorf("video_view_days rows = %d, want 0 (no daily data exists to import)", n)
	}
	chapterTitles := scanStrings(t, ctx, dest, `SELECT title FROM video_chapters WHERE video_id=$1 ORDER BY start_seconds`, vidID)
	if len(chapterTitles) != 2 || chapterTitles[0] != "Intro" || chapterTitles[1] != "Middle" {
		t.Errorf("chapters = %v, want [Intro Middle] (the blank-title row is unsupported)", chapterTitles)
	}
	if report.Entities[KindChapter].Unsupported != 1 {
		t.Errorf("chapters unsupported = %d, want 1 (blank title)", report.Entities[KindChapter].Unsupported)
	}
	var likes, dislikes int
	if err := dest.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE rating='like'), count(*) FILTER (WHERE rating='dislike')
		FROM video_ratings WHERE video_id=$1`, vidID).Scan(&likes, &dislikes); err != nil {
		t.Fatalf("read ratings: %v", err)
	}
	if likes != 1 || dislikes != 1 {
		t.Errorf("ratings = %d like / %d dislike, want 1/1 (remote rating excluded)", likes, dislikes)
	}
	if report.Entities[KindRating].Unsupported != 1 {
		t.Errorf("ratings unsupported = %d, want 1 (a cleared 'none' rating)", report.Entities[KindRating].Unsupported)
	}
	// Copy mode re-transcodes through Vidra, which writes its own ladder — so the
	// source's rungs are NOT claimed here. Only the audio-only rung is terminal.
	if n := countRows(t, ctx, dest, "video_renditions"); n != 0 {
		t.Errorf("video_renditions = %d, want 0 in copy mode (Vidra's transcode owns the ladder)", n)
	}

	// ── idempotency: a re-run creates nothing new ──
	usersBefore := countRows(t, ctx, dest, "users")
	videosBefore := countRows(t, ctx, dest, "videos")
	commentsBefore := countRows(t, ctx, dest, "comments")
	chaptersBefore := countRows(t, ctx, dest, "video_chapters")
	ratingsBefore := countRows(t, ctx, dest, "video_ratings")
	report2, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if report2.Entities[KindViewCount].Imported != 0 || report2.Entities[KindChapter].Imported != 0 ||
		report2.Entities[KindRating].Imported != 0 {
		t.Errorf("re-run imported per-video data again (views=%d chapters=%d ratings=%d), want 0",
			report2.Entities[KindViewCount].Imported, report2.Entities[KindChapter].Imported,
			report2.Entities[KindRating].Imported)
	}
	if countRows(t, ctx, dest, "video_chapters") != chaptersBefore ||
		countRows(t, ctx, dest, "video_ratings") != ratingsBefore {
		t.Error("re-run duplicated chapters or ratings — not idempotent")
	}
	var viewsAfter int64
	if err := dest.QueryRow(ctx, `SELECT views FROM video_view_counts WHERE video_id=$1`, vidID).Scan(&viewsAfter); err != nil {
		t.Fatalf("read view count after re-run: %v", err)
	}
	if viewsAfter != 100 {
		t.Errorf("re-run changed views to %d, want the same 100 (a re-run must not double a counter)", viewsAfter)
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

func TestPeerTubeImportReferenceMediaMode(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	sharedMediaDir := t.TempDir()
	seedSourceMedia(t, sharedMediaDir)
	sharedMedia, err := storage.NewLocal(sharedMediaDir)
	if err != nil {
		t.Fatal(err)
	}

	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy:    PolicySkip,
		MediaMode: MediaModeReference,
		DestMedia: sharedMedia,
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Entities[KindVideoFile].Imported != 1 {
		t.Errorf("video files imported = %d, want 1 referenced original", report.Entities[KindVideoFile].Imported)
	}
	if report.Entities[KindHLSPlaylist].Imported != 1 {
		t.Errorf("hls playlists imported = %d, want 1 referenced playlist", report.Entities[KindHLSPlaylist].Imported)
	}

	var vidID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM videos WHERE title='First Video'`).Scan(&vidID); err != nil {
		t.Fatalf("read video: %v", err)
	}
	var originalKey, thumbKey, hlsKey string
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id=$1 AND kind='original'`, vidID).Scan(&originalKey); err != nil {
		t.Fatalf("read original key: %v", err)
	}
	if originalKey != "web-videos/v1-720.mp4" {
		t.Errorf("original key = %q, want existing PeerTube key", originalKey)
	}
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id=$1 AND kind='thumbnail'`, vidID).Scan(&thumbKey); err != nil {
		t.Fatalf("read thumbnail key: %v", err)
	}
	if thumbKey != "thumbnails/v1-thumb.jpg" {
		t.Errorf("thumbnail key = %q, want existing PeerTube key", thumbKey)
	}
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM captions WHERE video_id=$1 AND language='en'`, vidID).Scan(&thumbKey); err != nil {
		t.Fatalf("read caption key: %v", err)
	}
	if thumbKey != "captions/v1-en.vtt" {
		t.Errorf("caption key = %q, want existing PeerTube key", thumbKey)
	}
	if err := dest.QueryRow(ctx, `SELECT master_key FROM streaming_playlists WHERE video_id=$1 AND state='ready'`, vidID).Scan(&hlsKey); err != nil {
		t.Fatalf("read hls key: %v", err)
	}
	if hlsKey != "streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-master.m3u8" {
		t.Errorf("hls key = %q, want existing PeerTube master key", hlsKey)
	}
	// The gap this closes: an imported video PLAYS its ladder (hls.js reads the
	// levels out of the master playlist) while the API reported renditions: [] and
	// the quality menu rendered empty. One row per rung of the referenced tree.
	if got := report.Entities[KindRendition].Imported; got != 2 {
		t.Errorf("renditions imported = %d, want 2 (720p + 480p; the audio rung is not one)", got)
	}
	if got := report.Entities[KindRendition].Unsupported; got != 1 {
		t.Errorf("renditions unsupported = %d, want 1 (the audio-only rung)", got)
	}
	type rung struct {
		height, width int
		prefix        string
	}
	rungRows, err := dest.Query(ctx, `SELECT height, width, key_prefix FROM video_renditions WHERE video_id=$1 ORDER BY height DESC`, vidID)
	if err != nil {
		t.Fatalf("read renditions: %v", err)
	}
	var rungs []rung
	for rungRows.Next() {
		var r rung
		if err := rungRows.Scan(&r.height, &r.width, &r.prefix); err != nil {
			t.Fatal(err)
		}
		rungs = append(rungs, r)
	}
	rungRows.Close()
	wantPrefix := "streaming-playlists/hls/11111111-1111-1111-1111-111111111111"
	// Heights come from the source. Widths are derived 16:9 here because this
	// fixture's videoFile carries no dimensions and its video no aspect ratio —
	// 480p rounds UP to an even 854, never an odd 853.
	want := []rung{{720, 1280, wantPrefix}, {480, 854, wantPrefix}}
	if len(rungs) != len(want) {
		t.Fatalf("renditions = %+v, want %+v", rungs, want)
	}
	for i := range want {
		if rungs[i] != want[i] {
			t.Errorf("rendition[%d] = %+v, want %+v", i, rungs[i], want[i])
		}
	}

	rc, err := sharedMedia.Open(ctx, hlsKey)
	if err != nil {
		t.Fatalf("open referenced hls master: %v", err)
	}
	_ = rc.Close()
	rc, err = sharedMedia.Open(ctx, originalKey)
	if err != nil {
		t.Fatalf("open referenced original: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != string(sourceVideoBytes) {
		t.Errorf("referenced original bytes mismatch")
	}
}

// TestImportViewCountsAreDeltaNotDouble is the proof the scheduled-import
// workflow rests on. The operator runs this tool repeatedly against a live
// PeerTube right up to the cutover, so "run it twice" is the normal case, not
// the edge case — and a view total is the one thing here that is a running
// counter rather than a set of rows.
//
// It pins all three ways the arithmetic can be wrong:
//
//   - re-running an UNCHANGED source must add nothing (naive addition doubles);
//   - views Vidra served between runs must SURVIVE (assignment erases them);
//   - a source that gained views must contribute only the gain.
//
// Chapters are checked alongside for the set-shaped half of the same promise.
func TestImportViewCountsAreDeltaNotDouble(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var vidID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM videos WHERE title='First Video'`).Scan(&vidID); err != nil {
		t.Fatalf("read video: %v", err)
	}
	readViews := func(what string) int64 {
		t.Helper()
		var n int64
		if err := dest.QueryRow(ctx, `SELECT views FROM video_view_counts WHERE video_id=$1`, vidID).Scan(&n); err != nil {
			t.Fatalf("read views (%s): %v", what, err)
		}
		return n
	}
	if got := readViews("after first run"); got != 100 {
		t.Fatalf("views after first run = %d, want the source's 100", got)
	}

	// 1. An unchanged source. A second run must be arithmetic-neutral.
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := readViews("after unchanged re-run"); got != 100 {
		t.Fatalf("views after an unchanged re-run = %d, want 100 — the counter was applied twice", got)
	}
	if got := report.Entities[KindViewCount].Imported; got != 0 {
		t.Errorf("unchanged re-run reported %d view counts imported, want 0", got)
	}
	if n := countRows(t, ctx, dest, "video_chapters"); n != 2 {
		t.Fatalf("chapters after re-run = %d, want the same 2 — the set was re-inserted", n)
	}

	// 2. Vidra serves 7 real views of its own between runs, exactly as the live
	//    counter would record them.
	mustExec(t, ctx, dest, `UPDATE video_view_counts SET views = views + 7 WHERE video_id = $1`, vidID)

	// 3. The source gains 50 views and one new chapter before the next scheduled
	//    run. (Writing to the source is the TEST's doing — the importer's own
	//    connection is read-only.)
	mustExec(t, ctx, src, `UPDATE "video" SET views = 150 WHERE id = 1`)
	mustExec(t, ctx, src, `INSERT INTO "videoChapter" (id,"videoId",timecode,title) VALUES (4,1,200,'Outro')`)

	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	// 100 carried + 7 served by Vidra + 50 gained on the source. Assignment would
	// give 150 (Vidra's 7 erased); re-adding the total would give 257.
	if got := readViews("after source gained views"); got != 157 {
		t.Errorf("views = %d, want 157 (100 imported + 7 served by Vidra + 50 new on the source)", got)
	}
	if got := report.Entities[KindViewCount].Imported; got != 1 {
		t.Errorf("view counts imported = %d, want 1 (the one video whose total moved)", got)
	}
	// The new chapter arrives; the two already there are not duplicated.
	titles := scanStrings(t, ctx, dest, `SELECT title FROM video_chapters WHERE video_id=$1 ORDER BY start_seconds`, vidID)
	if len(titles) != 3 || titles[2] != "Outro" {
		t.Errorf("chapters = %v, want [Intro Middle Outro]", titles)
	}

	// 4. A source whose total goes DOWN (a purge, a re-count) walks the counter
	//    back by the same difference.
	mustExec(t, ctx, src, `UPDATE "video" SET views = 120 WHERE id = 1`)
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("fourth run: %v", err)
	}
	if got := readViews("after source total dropped"); got != 127 {
		t.Errorf("views = %d, want 127 (157 less the 30 the source withdrew)", got)
	}

	// 5. A source total of ZERO is "no data here", NOT "withdraw everything". It
	//    has to be, because a source that stops carrying the column at all reads
	//    as zero for every video — and treating that as a withdrawal would wipe
	//    the whole instance's view history on one bad run.
	mustExec(t, ctx, src, `UPDATE "video" SET views = 0 WHERE id = 1`)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("fifth run: %v", err)
	}
	if got := readViews("after source total went to zero"); got != 127 {
		t.Errorf("views = %d, want 127 unchanged — a zero source total must not erase carried views", got)
	}
	if got := report.Entities[KindViewCount].Imported; got != 0 {
		t.Errorf("view counts imported = %d, want 0 for a source with nothing to carry", got)
	}

	// 6. The floor: a withdrawal larger than what Vidra holds stops at zero
	//    rather than going negative. Vidra's counter is reset to 3 to stage it.
	mustExec(t, ctx, dest, `UPDATE video_view_counts SET views = 3 WHERE video_id = $1`, vidID)
	mustExec(t, ctx, src, `UPDATE "video" SET views = 1 WHERE id = 1`)
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("sixth run: %v", err)
	}
	if got := readViews("after an oversized withdrawal"); got != 0 {
		t.Errorf("views = %d, want 0 — a counter must never go negative", got)
	}
}

// readInstanceSetting returns the stored override for a key, or "" when the key
// is not overridden at all (which is what "the built-in taxonomy stands" looks
// like in the database).
func readInstanceSetting(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) string {
	t.Helper()
	var v string
	err := pool.QueryRow(ctx, `SELECT value FROM instance_settings WHERE key=$1`, key).Scan(&v)
	if err == pgx.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read instance setting %s: %v", key, err)
	}
	return v
}

// The taxonomy pass is the one the operator runs REPEATEDLY: a scheduled import
// tracks a source that keeps changing until cutover, against a target the
// operator is also configuring by hand. This walks that whole sequence.
func TestImportCategoryTaxonomyIsRerunSafe(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	reloads := 0
	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy:         PolicySkip,
		ReloadSettings: func(context.Context) error { reloads++; return nil },
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	const key = "instance_custom_categories"
	const sourceTaxonomy = `["51:Giantess","52:Shrunken"]`
	// Rewriting the source's plugin settings is the TEST's doing; the importer's
	// own connection to the source is read-only.
	setSourceTaxonomy := func(taxonomy string) {
		t.Helper()
		mustExec(t, ctx, src,
			`UPDATE "plugin" SET settings = jsonb_set(settings, '{json-categories-as-text}', to_jsonb($1::text)) WHERE name='categories'`,
			taxonomy)
	}
	const allStockDeleted = `"delete":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18]`

	// 1. First run carries the source's taxonomy and tells the running server to
	//    reload its overlay — a taxonomy in the database but not in effect until
	//    the next restart is the silent half-success this pass exists to avoid.
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != sourceTaxonomy {
		t.Fatalf("after first run = %s, want %s", got, sourceTaxonomy)
	}
	if reloads != 1 {
		t.Errorf("settings cache reloaded %d times, want 1", reloads)
	}

	// 2. An unchanged source writes NOTHING at all on the next scheduled run —
	//    not even the same value again. updated_at is the proof: an upsert that
	//    stored an identical value would still move it.
	var writtenAt time.Time
	if err := dest.QueryRow(ctx, `SELECT updated_at FROM instance_settings WHERE key=$1`, key).Scan(&writtenAt); err != nil {
		t.Fatalf("read setting timestamp: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := report.Entities[KindCategoryTaxonomy]; got.Imported != 0 || got.Skipped != 1 {
		t.Errorf("unchanged re-run reported %+v, want 0 imported / 1 skipped", got)
	}
	var stillAt time.Time
	if err := dest.QueryRow(ctx, `SELECT updated_at FROM instance_settings WHERE key=$1`, key).Scan(&stillAt); err != nil {
		t.Fatalf("re-read setting timestamp: %v", err)
	}
	if !stillAt.Equal(writtenAt) {
		t.Errorf("the setting was rewritten by an unchanged re-run (%s -> %s)", writtenAt, stillAt)
	}
	if reloads != 1 {
		t.Errorf("settings cache reloaded %d times, want 1 — a run that wrote nothing reloaded anyway", reloads)
	}

	// 3. The source adds a category before the next run. The stored value is
	//    still the one the import wrote, so the import updates its own value.
	setSourceTaxonomy(`{"add":[{"key":51,"label":"Giantess"},{"key":52,"label":"Shrunken"},{"key":53,"label":"Growth"}],` + allStockDeleted + `}`)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if got := report.Entities[KindCategoryTaxonomy].Imported; got != 1 {
		t.Errorf("category taxonomy imported = %d, want 1 (the source gained a category)", got)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != `["51:Giantess","52:Shrunken","53:Growth"]` {
		t.Fatalf("after the source moved = %s, want the new category carried", got)
	}

	// 3b. The source's plugin now deletes everything and adds nothing. There is
	//     no taxonomy to carry — and the one already carried is NOT withdrawn:
	//     the videos imported under it still carry its ids.
	setSourceTaxonomy(`{"add":[],` + allStockDeleted + `}`)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run against an empty source taxonomy: %v", err)
	}
	if got := report.Entities[KindCategoryTaxonomy]; got.Unsupported != 1 || got.Imported != 0 {
		t.Errorf("run against an empty source taxonomy reported %+v, want 1 unsupported / 0 imported", got)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != `["51:Giantess","52:Shrunken","53:Growth"]` {
		t.Fatalf("after an empty source taxonomy = %s, want the carried taxonomy left standing", got)
	}

	// 3c. The source defines categories again. This still has to be recognised as
	//     the import's OWN value — which it only can be if the runs that wrote
	//     nothing preserved the ledger's memory of what was applied instead of
	//     blanking it.
	setSourceTaxonomy(`{"add":[{"key":51,"label":"Giantess"},{"key":52,"label":"Shrunken"}],` + allStockDeleted + `}`)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run after the source defined categories again: %v", err)
	}
	if got := report.Entities[KindCategoryTaxonomy].Imported; got != 1 {
		t.Errorf("category taxonomy imported = %d, want 1 — the import no longer recognises its own value", got)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != sourceTaxonomy {
		t.Fatalf("after the source defined categories again = %s, want %s", got, sourceTaxonomy)
	}

	// 4. The OPERATOR edits the taxonomy. From here the import must never touch
	//    it again, however much the source moves — a scheduled import that
	//    overwrites this every night is a nightly silent undo of a human's work.
	const operatorTaxonomy = `["51:Giantess","52:Shrunken","99:Operator's Own"]`
	mustExec(t, ctx, dest, `UPDATE instance_settings SET value=$1 WHERE key=$2`, operatorTaxonomy, key)
	setSourceTaxonomy(`{"add":[{"key":51,"label":"Giantess"},{"key":54,"label":"Later"}],` + allStockDeleted + `}`)
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("fourth run: %v", err)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != operatorTaxonomy {
		t.Fatalf("after an operator edit = %s, want the operator's value untouched", got)
	}
	if got := report.Entities[KindCategoryTaxonomy]; got.Imported != 0 || got.Skipped != 1 {
		t.Errorf("run over an operator edit reported %+v, want 0 imported / 1 skipped", got)
	}
	if len(report.Conflicts) == 0 {
		t.Error("leaving the operator's taxonomy alone was not reported as a conflict; the operator would never learn the source diverged")
	}

	// 5. The operator clears the key back to the built-ins. That is a decision,
	//    not a gap for the next run to refill.
	mustExec(t, ctx, dest, `DELETE FROM instance_settings WHERE key=$1`, key)
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("fifth run: %v", err)
	}
	if got := readInstanceSetting(t, ctx, dest, key); got != "" {
		t.Errorf("after the operator cleared the key = %s, want it to stay cleared", got)
	}
}

// Most PeerTube instances run the stock taxonomy. For them the import must write
// NO override at all: an override that restates the built-in list freezes a
// shipped list against every future change to it.
func TestImportCategoryTaxonomyLeavesBuiltinsAloneWithoutAPlugin(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	hash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	const key = "instance_custom_categories"

	for _, tc := range []struct {
		name  string
		stage string // SQL the TEST runs on the source to create the case
	}{
		{"no plugin table at all", `DROP TABLE "plugin"`},
		{"no categories plugin installed", `DELETE FROM "plugin" WHERE name='categories'`},
		{"the plugin is installed but disabled", `UPDATE "plugin" SET enabled=false WHERE name='categories'`},
		{"the plugin is uninstalled", `UPDATE "plugin" SET uninstalled=true WHERE name='categories'`},
		{"the plugin holds no taxonomy", `UPDATE "plugin" SET settings='{"other-setting":1}'::jsonb WHERE name='categories'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, _ := newScratchDB(t, ctx, base)
			dest, _ := newScratchDB(t, ctx, base)
			applyMigrations(t, ctx, dest)
			seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
			mustExec(t, ctx, src, tc.stage)

			imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
			version, err := imp.Preflight(ctx)
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			report, err := imp.Run(ctx, version, nil)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := readInstanceSetting(t, ctx, dest, key); got != "" {
				t.Errorf("wrote %s, want no override at all (the built-in taxonomy stands)", got)
			}
			if got := report.Entities[KindCategoryTaxonomy].Imported; got != 0 {
				t.Errorf("category taxonomy imported = %d, want 0", got)
			}
			var deferredSeen bool
			for _, d := range report.Deferred {
				if strings.Contains(d, "category taxonomy") {
					deferredSeen = true
				}
			}
			if !deferredSeen {
				t.Error("the report does not say the source defines no taxonomy")
			}
		})
	}
}

// TestPeerTubeImportActorImages is the proof for the avatar/banner pass: type 1
// lands in the avatar slot and type 2 in the banner slot, only LOCAL actors are
// ever fetched, a source that answers with something other than a real image has
// nothing stored for it, one broken image does not fail the run, and a second
// run fetches nothing it already has.
func TestPeerTubeImportActorImages(t *testing.T) {
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

	// The source instance, serving its avatars the way a real one does. Every
	// request is recorded so the test can assert what was NOT asked for.
	var (
		mu       sync.Mutex
		requests = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		mu.Lock()
		requests[name]++
		mu.Unlock()
		switch name {
		case "bob-avatar.png":
			// A transient failure: the run must survive it and retry next time.
			w.WriteHeader(http.StatusInternalServerError)
		case "bob-channel-avatar.png":
			// The /static/ trap: 200, but it is the single-page app's HTML.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(spaHTML)
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		}
	}))
	defer srv.Close()

	// The importer learns the origin from the source's own actors — no flag, no
	// extra operator input. Only LOCAL actors get one, exactly as PeerTube does.
	mustExec(t, ctx, src,
		`UPDATE "actor" SET url = $1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, srv.URL)

	destMediaDir := t.TempDir()
	destMedia, err := storage.NewLocal(destMediaDir)
	if err != nil {
		t.Fatal(err)
	}
	// Reference mode ON PURPOSE: avatars are never in the source's object store,
	// so there is nothing to reference and they must be fetched anyway.
	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: destMedia,
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	plan, err := imp.Plan(ctx, version)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// 4 usable avatars (ids 1,3,4,5) — the remote actor's row is not a candidate
	// at all, and the unrecognised type 9 is not counted as either slot.
	if got := plan.Entities[KindActorAvatar].Planned; got != 4 {
		t.Errorf("plan actor avatars = %d, want 4", got)
	}
	if got := plan.Entities[KindActorBanner].Planned; got != 1 {
		t.Errorf("plan actor banners = %d, want 1", got)
	}
	mu.Lock()
	planRequests := len(requests)
	mu.Unlock()
	if planRequests != 0 {
		t.Fatalf("--dry-run made %d HTTP requests to the source; it must write and fetch nothing", planRequests)
	}

	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// alice's avatar + channel avatar landed; bob's 500'd; bob_channel's was HTML.
	if got := report.Entities[KindActorAvatar].Imported; got != 2 {
		t.Errorf("actor avatars imported = %d, want 2 (alice + alice_channel)", got)
	}
	if got := report.Entities[KindActorBanner].Imported; got != 1 {
		t.Errorf("actor banners imported = %d, want 1", got)
	}
	if got := report.Entities[KindActorAvatar].Failed; got != 1 {
		t.Errorf("actor avatars failed = %d, want 1 (bob's 500) — and the run still returned nil", got)
	}
	// The HTML answer and the unrecognised type are facts about the source, not
	// transient failures, so both are terminal-unsupported.
	if got := report.Entities[KindActorAvatar].Unsupported; got != 2 {
		t.Errorf("actor avatars unsupported = %d, want 2 (the SPA HTML + the unrecognised type)", got)
	}

	// The remote actor's image must never have been requested.
	mu.Lock()
	remoteHits := requests["remote-avatar.png"]
	bobHits := requests["bob-avatar.png"]
	aliceHits := requests["alice-avatar.png"]
	mu.Unlock()
	if remoteHits != 0 {
		t.Errorf("the REMOTE actor's avatar was fetched %d times; remote actors are not imported, so their images are not either", remoteHits)
	}
	if aliceHits != 1 {
		t.Errorf("alice's avatar was fetched %d times, want 1", aliceHits)
	}

	// The images are in the normal places, under the normal key layout, with the
	// content type derived from the BYTES.
	var aliceID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM users WHERE username='alice'`).Scan(&aliceID); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	assertImage := func(table, col string, owner uuid.UUID, kind, wantPrefix string) string {
		t.Helper()
		var key, ct string
		var size int64
		q := `SELECT storage_key, content_type, size_bytes FROM ` + table + ` WHERE ` + col + ` = $1 AND kind = $2`
		if err := dest.QueryRow(ctx, q, owner, kind).Scan(&key, &ct, &size); err != nil {
			t.Fatalf("read %s %s: %v", table, kind, err)
		}
		if !strings.HasPrefix(key, wantPrefix) {
			t.Errorf("%s %s key = %q, want prefix %q", table, kind, key, wantPrefix)
		}
		if !strings.HasSuffix(key, ".png") || ct != "image/png" {
			t.Errorf("%s %s stored as %q/%q, want a .png/image/png (the sniffed type wins)", table, kind, key, ct)
		}
		if size != int64(len(pngBytes)) {
			t.Errorf("%s %s size = %d, want %d", table, kind, size, len(pngBytes))
		}
		if _, err := os.Stat(filepath.Join(destMediaDir, filepath.FromSlash(key))); err != nil {
			t.Errorf("%s %s blob is not on disk: %v", table, kind, err)
		}
		return key
	}
	assertImage("user_images", "user_id", aliceID, "avatar", "avatars/users/")
	assertImage("user_images", "user_id", aliceID, "banner", "banners/users/")

	var aliceChannelID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM channels WHERE handle='alice_channel'`).Scan(&aliceChannelID); err != nil {
		t.Fatalf("read alice_channel: %v", err)
	}
	assertImage("channel_images", "channel_id", aliceChannelID, "avatar", "avatars/channels/")

	// Nothing was stored for the HTML answer.
	var bobChannelID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM channels WHERE handle='bob_channel'`).Scan(&bobChannelID); err != nil {
		t.Fatalf("read bob_channel: %v", err)
	}
	var n int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM channel_images WHERE channel_id = $1`, bobChannelID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("bob_channel has %d images; the source served HTML, which must never be stored as an avatar", n)
	}

	// ── the second run: idempotent ──
	report2, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := report2.Entities[KindActorAvatar].Imported + report2.Entities[KindActorBanner].Imported; got != 0 {
		t.Errorf("second run imported %d actor images, want 0", got)
	}
	mu.Lock()
	aliceHits2, bobHits2 := requests["alice-avatar.png"], requests["bob-avatar.png"]
	mu.Unlock()
	if aliceHits2 != 1 {
		t.Errorf("alice's avatar was fetched %d times across two runs, want 1 — a scheduled re-run must not re-fetch", aliceHits2)
	}
	// A FAILED row is deliberately not terminal: the source's 500 may have been a
	// blip, so the next run asks once more.
	if bobHits2 != bobHits+1 {
		t.Errorf("bob's failed avatar was fetched %d times across two runs, want %d — a transient failure must be retried", bobHits2, bobHits+1)
	}

	// ── an image Vidra already has is never overwritten ──
	mustExec(t, ctx, dest, `UPDATE user_images SET storage_key = 'avatars/users/hand-picked.png' WHERE user_id = $1 AND kind = 'avatar'`, aliceID)
	mustExec(t, ctx, src, `UPDATE "actorImage" SET id = 101 WHERE id = 1`) // the source replaced the file
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("third run: %v", err)
	}
	var key string
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM user_images WHERE user_id = $1 AND kind='avatar'`, aliceID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "avatars/users/hand-picked.png" {
		t.Errorf("storage_key = %q; an import must fill gaps, never overwrite an image the instance already has", key)
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
	write("streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-master.m3u8",
		[]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=1280x720\nv1-720.m3u8\n"))
	write("streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-720.m3u8",
		[]byte("#EXTM3U\n#EXT-X-MAP:URI=\"v1-init.mp4\"\n#EXTINF:4.0,\nv1-720-fragmented.mp4\n#EXT-X-ENDLIST\n"))
	write("streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-init.mp4", []byte("FAKE-INIT"))
	write("streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-720-fragmented.mp4", []byte("FAKE-FMP4"))
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
			language text, duration integer NOT NULL DEFAULT 0, views integer NOT NULL DEFAULT 0,
			"createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoFile" (
			id serial PRIMARY KEY, "videoId" integer, "videoStreamingPlaylistId" integer,
			resolution integer NOT NULL, size bigint NOT NULL, extname text, fps integer, filename text)`,
		`CREATE TABLE "videoStreamingPlaylist" (
			id serial PRIMARY KEY, "videoId" integer NOT NULL, "playlistFilename" text NOT NULL)`,
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
		`CREATE TABLE "videoChapter" (
			id serial PRIMARY KEY, "videoId" integer NOT NULL, timecode integer NOT NULL,
			title text NOT NULL, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "accountVideoRate" (
			id serial PRIMARY KEY, type text NOT NULL, "accountId" integer NOT NULL,
			"videoId" integer NOT NULL, url text, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "plugin" (
			id serial PRIMARY KEY, name text NOT NULL, type integer NOT NULL DEFAULT 1,
			version text, enabled boolean NOT NULL DEFAULT true, uninstalled boolean NOT NULL DEFAULT false,
			settings jsonb, storage jsonb, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		// The real column set on schema 1040. fileUrl/cached are NULL/false for a
		// LOCAL actor's image; a remote actor's row carries them.
		`CREATE TABLE "actorImage" (
			id serial PRIMARY KEY, filename text NOT NULL, height integer, width integer,
			"fileUrl" text, type integer NOT NULL DEFAULT 1, "actorId" integer NOT NULL,
			cached boolean NOT NULL DEFAULT false,
			"createdAt" timestamptz NOT NULL DEFAULT now(), "updatedAt" timestamptz NOT NULL DEFAULT now())`,

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
		`INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,category,licence,language,duration,views) VALUES
			(1,'11111111-1111-1111-1111-111111111111',1,'First Video','hello',1,1,1,1,'en',120,100),
			(2,'22222222-2222-2222-2222-222222222222',1,'Second Video','',3,1,NULL,NULL,NULL,60,0)`,
		`INSERT INTO "videoFile" (id,"videoId",resolution,size,extname,filename) VALUES
			(1,1,720,` + strconv.Itoa(len(sourceVideoBytes)) + `,'.mp4','v1-720.mp4'),
			(2,1,480,10,'.mp4','v1-480.mp4')`,
		`INSERT INTO "videoStreamingPlaylist" (id,"videoId","playlistFilename") VALUES
			(1,1,'v1-master.m3u8')`,
		// The HLS ladder: videoFile rows hang off the STREAMING PLAYLIST, not the
		// video, which is why the older per-video file read never saw them. File 5
		// is PeerTube's audio-only rung (resolution 0) — not a rung of the quality
		// ladder, and unstorable in video_renditions, which CHECKs height > 0.
		`INSERT INTO "videoFile" (id,"videoStreamingPlaylistId",resolution,size,extname,filename) VALUES
			(3,1,720,4096,'.mp4','v1-720-fragmented.mp4'),
			(4,1,480,2048,'.mp4','v1-480-fragmented.mp4'),
			(5,1,0,64,'.mp4','v1-audio-fragmented.mp4')`,
		// Chapter 3 has a blank title: video_chapters CHECKs 1..120 characters, so
		// it is reported unsupported rather than written as an empty mark.
		`INSERT INTO "videoChapter" (id,"videoId",timecode,title) VALUES
			(1,1,0,'Intro'),
			(2,1,90,'Middle'),
			(3,1,150,'   ')`,
		// alice likes video 1, bob dislikes it, the REMOTE account's like is excluded
		// (no Vidra user to attribute it to), and alice's 'none' row on video 2 is a
		// cleared rating, not an opinion.
		`INSERT INTO "accountVideoRate" (id,type,"accountId","videoId") VALUES
			(1,'like',1,1),
			(2,'dislike',2,1),
			(3,'like',5,1),
			(4,'none',1,2)`,
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
		// This source runs peertube-plugin-categories: it withdraws the stock 1-18
		// and defines its own at 51/52, which is why an import that carried the
		// videos' category ids without the taxonomy left every one of them pointing
		// at nothing. The other plugin row is here so the read has to FIND the
		// categories one rather than take the first plugin installed.
		`INSERT INTO "plugin" (id,name,enabled,uninstalled,settings) VALUES
			(1,'livechat',true,false,'{}'::jsonb),
			(2,'categories',true,false,$plug$` + sourceCategorySettings + `$plug$::jsonb)`,
		// Actor images. 1/2 = alice's account avatar + banner, 3 = alice_channel's
		// avatar, 4 = bob's avatar (the server 500s on it), 5 = bob_channel's avatar
		// (the server answers with the SPA's HTML), 6 = an unrecognised type, and
		// 7 belongs to the REMOTE actor — the row this importer must never fetch.
		`INSERT INTO "actorImage" (id,filename,type,"actorId","fileUrl",cached) VALUES
			(1,'alice-avatar.png',1,1,NULL,false),
			(2,'alice-banner.png',2,1,NULL,false),
			(3,'alice-channel-avatar.png',1,2,NULL,false),
			(4,'bob-avatar.png',1,3,NULL,false),
			(5,'bob-channel-avatar.png',1,4,NULL,false),
			(6,'weird.png',9,3,NULL,false),
			(7,'remote-avatar.png',1,5,'https://remote.example/lazy-static/avatars/remote-avatar.png',true)`,
	}
	for _, s := range stmts {
		mustExec(t, ctx, pool, s)
	}
}
