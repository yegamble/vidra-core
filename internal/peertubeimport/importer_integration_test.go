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
	"errors"
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

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/quota"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
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
	// A dry run answers "how much of this is restricted?" before anything is
	// written: bob is suspended on the source, video 2 is flagged nsfw.
	if got := plan.Entities[KindUserSuspension].Planned; got != 1 {
		t.Errorf("plan suspensions planned = %d, want 1 (bob is blocked on the source)", got)
	}
	if got := plan.Entities[KindVideoSensitive].Planned; got != 1 {
		t.Errorf("plan sensitive videos planned = %d, want 1 (video 2 is nsfw)", got)
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
	if n := countRows(t, ctx, dest, "video_files"); n < 4 {
		t.Errorf("video_files = %d, want >=4 (original + thumbnail + storyboard + storyboard vtt)", n)
	}
	// The poster lands on VIDRA's key, not the source's: an imported poster has
	// to be indistinguishable from a generated one, or the endpoints that serve
	// it and the GC that sweeps it disagree about where it lives.
	var thumbKey string
	var thumbSize int64
	if err := dest.QueryRow(ctx, `SELECT storage_key, size_bytes FROM video_files WHERE video_id=$1 AND kind='thumbnail'`, vidID).Scan(&thumbKey, &thumbSize); err != nil {
		t.Fatalf("read thumbnail: %v", err)
	}
	if want := "thumbnails/" + vidID.String() + ".jpg"; thumbKey != want {
		t.Errorf("thumbnail key = %q, want the native key %q", thumbKey, want)
	}
	if ok, _ := destMedia.Exists(ctx, thumbKey); !ok {
		t.Errorf("thumbnail row points at %q and there is no such object — the exact failure this pass exists to fix", thumbKey)
	}
	if thumbSize != int64(len(jpegBytes)) {
		t.Errorf("thumbnail size = %d, want %d", thumbSize, len(jpegBytes))
	}
	// The storyboard comes across as BOTH rows. PeerTube stores the sprite sheet
	// and no WebVTT map, so the map is synthesised from its geometry columns; a
	// sprite with no map is a has_storyboard:true that renders nothing.
	var spriteKey, vttKey string
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id=$1 AND kind='storyboard'`, vidID).Scan(&spriteKey); err != nil {
		t.Fatalf("read storyboard sprite: %v", err)
	}
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id=$1 AND kind='storyboard_vtt'`, vidID).Scan(&vttKey); err != nil {
		t.Fatalf("read storyboard vtt: %v", err)
	}
	if spriteKey != "storyboards/"+vidID.String()+".jpg" || vttKey != "storyboards/"+vidID.String()+".vtt" {
		t.Errorf("storyboard keys = %q / %q, want the native pair", spriteKey, vttKey)
	}
	vttRC, err := destMedia.Open(ctx, vttKey)
	if err != nil {
		t.Fatalf("open synthesised vtt: %v", err)
	}
	vttBody, _ := io.ReadAll(vttRC)
	_ = vttRC.Close()
	if !strings.HasPrefix(string(vttBody), "WEBVTT") {
		t.Errorf("synthesised map is not a WebVTT file:\n%s", vttBody)
	}
	// 120s of video at 30s a tile is four cues, and the sprite is referenced
	// RELATIVELY — the .vtt route depends on that, which is why it is never
	// redirectable.
	if n := strings.Count(string(vttBody), "storyboard.jpg#xywh="); n != 4 {
		t.Errorf("vtt has %d cues, want 4 (120s / 30s a tile):\n%s", n, vttBody)
	}
	if strings.Contains(string(vttBody), "/storyboards/") {
		t.Errorf("the sprite must be referenced relatively:\n%s", vttBody)
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
	// The poster is NOT referenced, and that is the point. PeerTube's object
	// storage covers five families and thumbnails are not one of them, so
	// thumbnails/<source-filename> names nothing: a row pointing there is the
	// broken image on every card that this pass was written to stop. With no
	// source media root and no reachable origin the family is DEFERRED, loudly,
	// instead of being recorded as carried.
	if err := dest.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id=$1 AND kind='thumbnail'`, vidID).Scan(&thumbKey); err == nil {
		t.Errorf("reference mode recorded a thumbnail at %q; PeerTube never stores one in object storage", thumbKey)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("read thumbnail key: %v", err)
	}
	var deferredThumbs bool
	for _, d := range report.Deferred {
		if strings.Contains(d, "video thumbnails") {
			deferredThumbs = true
		}
	}
	if !deferredThumbs {
		t.Errorf("nothing in the report says the posters were not carried: %v", report.Deferred)
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

// ── source-authoritative resync ──
//
// The mode this exercises can corrupt a production catalogue if it is wrong, so
// the test is written as the migration it is for: import a source, let both
// sides move the way they really do over the days before a cutover, and then
// assert BOTH halves — that the default import still refuses to touch any of it,
// and that the source-authoritative one changes exactly what it should and
// nothing else.

// queryTracer records every statement a pool issues, so a test can assert on the
// SHAPE of a run's database traffic rather than on a stopwatch.
type queryTracer struct {
	mu   sync.Mutex
	sqls []string
}

func (q *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	q.sqls = append(q.sqls, data.SQL)
	q.mu.Unlock()
	return ctx
}

func (q *queryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (q *queryTracer) reset() {
	q.mu.Lock()
	q.sqls = nil
	q.mu.Unlock()
}

// countMatching returns how many recorded statements contain needle.
func (q *queryTracer) countMatching(needle string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, s := range q.sqls {
		if strings.Contains(s, needle) {
			n++
		}
	}
	return n
}

// tracedPool opens a second pool onto an existing scratch database, with a
// tracer attached.
func tracedPool(t *testing.T, ctx context.Context, base, name string) (*pgxpool.Pool, *queryTracer) {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	tracer := &queryTracer{}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, tracer
}

func TestPeerTubeImportSourceAuthoritativeResync(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	firstHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	_, srcName := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	srcPool, srcTrace := tracedPool(t, ctx, base, srcName)
	seedPeerTube(t, ctx, srcPool, string(firstHash), secretPrivKeyAlice)

	// Metadata only: this mode must never re-download media, and the surest way
	// to prove a run did not open the object store is not to give it one.
	gapFill := NewImporter(dest, NewSourceFromPool(srcPool), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := gapFill.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := gapFill.Run(ctx, version, nil); err != nil {
		t.Fatalf("first (gap-filling) run: %v", err)
	}

	// ── what the instance looked like after the migration ──
	var aliceID, vidID, chanID, playlistID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM users WHERE username='alice'`).Scan(&aliceID); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	if err := dest.QueryRow(ctx, `SELECT id FROM videos WHERE title='First Video'`).Scan(&vidID); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if err := dest.QueryRow(ctx, `SELECT id FROM channels WHERE handle='alice_channel'`).Scan(&chanID); err != nil {
		t.Fatalf("read channel: %v", err)
	}
	if err := dest.QueryRow(ctx, `SELECT id FROM playlists WHERE title='My Playlist'`).Scan(&playlistID); err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	videosBefore := countRows(t, ctx, dest, "videos")
	usersBefore := countRows(t, ctx, dest, "users")
	var alicePub, alicePriv string
	if err := dest.QueryRow(ctx, `SELECT public_key_pem, private_key_pem FROM account_actor_keys WHERE user_id=$1`, aliceID).Scan(&alicePub, &alicePriv); err != nil {
		t.Fatalf("read actor key: %v", err)
	}

	// ── things that happen on THIS instance between runs ──
	//
	// A video uploaded straight to Vidra, on an imported channel. It has no ledger
	// row, so nothing in this mode can see it — that is the whole safety property,
	// and it is asserted rather than assumed.
	var nativeID uuid.UUID
	if err := dest.QueryRow(ctx, `
		INSERT INTO videos (channel_id, title, description, privacy, state)
		VALUES ($1, 'Uploaded Here', 'native', 'public', 'published') RETURNING id`, chanID).Scan(&nativeID); err != nil {
		t.Fatalf("insert native video: %v", err)
	}
	mustExec(t, ctx, dest, `INSERT INTO video_tags (video_id, tag) VALUES ($1,'native')`, nativeID)
	mustExec(t, ctx, dest, `INSERT INTO video_chapters (video_id, start_seconds, title) VALUES ($1, 5, 'Native chapter')`, nativeID)
	// A rendition row of the kind a Vidra re-transcode writes, on an IMPORTED
	// video. The comment on ImportInsertVideoRendition explains what overwriting
	// one of these breaks; this is the assertion behind it.
	mustExec(t, ctx, dest, `INSERT INTO video_renditions (video_id, height, width, key_prefix) VALUES ($1, 720, 1280, 'hls/vidra-transcode/')`, vidID)
	// Vidra serves 7 views of its own, so the view counter carries Vidra-native
	// data that an assignment would destroy.
	mustExec(t, ctx, dest, `UPDATE video_view_counts SET views = views + 7 WHERE video_id = $1`, vidID)
	// An operator replaces the carried taxonomy by hand.
	mustExec(t, ctx, dest, `UPDATE instance_settings SET value = '["90:Handmade"]' WHERE key = 'instance_custom_categories'`)

	// ── things that happen on the still-live SOURCE between runs ──
	secondHash, err := bcrypt.GenerateFromPassword([]byte("a-new-password-entirely"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, srcPool, `UPDATE "video" SET name='First Video (edited)', description='rewritten', privacy=2, views=120 WHERE id=1`)
	mustExec(t, ctx, srcPool, `UPDATE "user" SET password=$1, role=1, "emailVerified"=false WHERE id=1`, string(secondHash))
	mustExec(t, ctx, srcPool, `UPDATE "account" SET name='Alice Renamed' WHERE id=1`)
	// The source's ActivityPub keypair is regenerated. It must NOT follow: this
	// instance is already federating as that actor, and replacing a live signing
	// key invalidates every HTTP signature outstanding against it.
	mustExec(t, ctx, srcPool, `UPDATE "actor" SET "publicKey"='PUBKEY-ALICE-ROTATED', "privateKey"='PRIVKEY-ALICE-ROTATED' WHERE id=1`)
	mustExec(t, ctx, srcPool, `UPDATE "videoChannel" SET name='Alice Channel (renamed)', description='new blurb' WHERE id=1`)
	// The chapter at 90s MOVES to 95s. This is the case a DO UPDATE cannot
	// express: (video_id, start_seconds) is the primary key, so an upsert leaves
	// the 90s mark standing and the video shows both.
	mustExec(t, ctx, srcPool, `UPDATE "videoChapter" SET timecode=95 WHERE id=2`)
	// Tags: 'music' is dropped, 'jazz' is added.
	mustExec(t, ctx, srcPool, `DELETE FROM "videoTag" WHERE "videoId"=1 AND "tagId"=1`)
	mustExec(t, ctx, srcPool, `INSERT INTO "tag" (id,name) VALUES (3,'jazz')`)
	mustExec(t, ctx, srcPool, `INSERT INTO "videoTag" ("videoId","tagId") VALUES (1,3)`)
	// Alice changes her mind about the video; bob unrates his dislike outright
	// (PeerTube deletes the row).
	mustExec(t, ctx, srcPool, `UPDATE "accountVideoRate" SET type='dislike' WHERE id=1`)
	mustExec(t, ctx, srcPool, `DELETE FROM "accountVideoRate" WHERE id=2`)
	// The playlist loses its second slot and gains a title.
	mustExec(t, ctx, srcPool, `DELETE FROM "videoPlaylistElement" WHERE id=2`)
	mustExec(t, ctx, srcPool, `UPDATE "videoPlaylist" SET name='My Playlist (renamed)' WHERE id=1`)
	// Bob unsubscribes from alice_channel.
	mustExec(t, ctx, srcPool, `DELETE FROM "actorFollow" WHERE id=1`)

	// ── half one: the DEFAULT import still refuses to touch any of it ──
	report, err := gapFill.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("gap-filling re-run: %v", err)
	}
	if total := report.Entities[KindVideo].Updated + report.Entities[KindUser].Updated +
		report.Entities[KindChapter].Updated + report.Entities[KindChannel].Updated; total != 0 {
		t.Fatalf("the DEFAULT import reported %d updates; gap-filling must never update anything", total)
	}
	if got := scanStrings(t, ctx, dest, `SELECT title FROM videos WHERE id=$1`, vidID); got[0] != "First Video" {
		t.Errorf("the gap-filling run changed the title to %q; the default must leave divergence alone", got[0])
	}
	if got := readInstanceSetting(t, ctx, dest, "instance_custom_categories"); got != `["90:Handmade"]` {
		t.Errorf("the gap-filling run overwrote the operator's taxonomy: %s", got)
	}

	// ── half two: the source wins ──
	srcTrace.reset()
	resync := NewImporter(dest, NewSourceFromPool(srcPool), Options{
		Policy: PolicySkip, MediaMode: MediaModeNone, SourceAuthoritative: true,
	})
	report, err = resync.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("source-authoritative run: %v", err)
	}
	if !report.SourceAuthoritative {
		t.Error("the report must record which side won")
	}

	// RULE 1 — nothing is ever re-INSERTed. ImportInsertVideo has no ON CONFLICT,
	// so a resync that reached it would create a DUPLICATE video and the ledger
	// upsert would then repoint at the duplicate, orphaning the original row and
	// its blobs. Both halves are asserted: the count, and the mapping.
	if n := countRows(t, ctx, dest, "videos"); n != videosBefore+1 {
		t.Errorf("videos = %d, want %d (the one uploaded here, and NO duplicate of an imported one)", n, videosBefore+1)
	}
	if n := countRows(t, ctx, dest, "users"); n != usersBefore {
		t.Errorf("users = %d, want %d — the resync inserted an account", n, usersBefore)
	}
	var ledgerVideo uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT vidra_id FROM peertube_import_ledger WHERE entity_kind='video' AND source_id='11111111-1111-1111-1111-111111111111'`).Scan(&ledgerVideo); err != nil {
		t.Fatalf("read video ledger row: %v", err)
	}
	if ledgerVideo != vidID {
		t.Fatalf("the ledger now points at %s, want the original %s — the original row has been orphaned", ledgerVideo, vidID)
	}

	// Video metadata followed the source.
	var title, description, privacy string
	if err := dest.QueryRow(ctx, `SELECT title, description, privacy FROM videos WHERE id=$1`, vidID).Scan(&title, &description, &privacy); err != nil {
		t.Fatalf("read video: %v", err)
	}
	if title != "First Video (edited)" || description != "rewritten" || privacy != "unlisted" {
		t.Errorf("video = %q/%q/%q, want the source's edited metadata", title, description, privacy)
	}
	if report.Entities[KindVideo].Updated != 1 {
		t.Errorf("videos updated = %d, want 1", report.Entities[KindVideo].Updated)
	}

	// The MOVED chapter did not duplicate: two marks, at 0 and 95.
	starts := scanStrings(t, ctx, dest, `SELECT start_seconds::text FROM video_chapters WHERE video_id=$1 ORDER BY start_seconds`, vidID)
	if len(starts) != 2 || starts[0] != "0" || starts[1] != "95" {
		t.Errorf("chapter starts = %v, want [0 95] — a moved mark was duplicated rather than replaced", starts)
	}

	// Tags are a SET: what the source dropped is gone, what it added is here.
	tags := scanStrings(t, ctx, dest, `SELECT tag FROM video_tags WHERE video_id=$1 ORDER BY tag`, vidID)
	if len(tags) != 2 || tags[0] != "jazz" || tags[1] != "test" {
		t.Errorf("tags = %v, want [jazz test]", tags)
	}

	// The user's mapped fields, INCLUDING the password hash — a source-side change
	// in the days before cutover is otherwise never carried and the account cannot
	// log in afterwards.
	var gotHash, role, displayName, username, email string
	var verified bool
	if err := dest.QueryRow(ctx, `SELECT password_hash, role, display_name, username, email, email_verified FROM users WHERE id=$1`, aliceID).
		Scan(&gotHash, &role, &displayName, &username, &email, &verified); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	if gotHash != string(secondHash) {
		t.Error("the source's new password hash was not carried")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(gotHash), []byte("a-new-password-entirely")); err != nil {
		t.Errorf("the carried hash does not verify the source's new password: %v", err)
	}
	if role != "moderator" || displayName != "Alice Renamed" || verified {
		t.Errorf("alice = role %q / %q / verified %v, want moderator / Alice Renamed / false", role, displayName, verified)
	}
	if username != "alice" || email != "alice@example.test" {
		t.Errorf("alice's natural keys changed to %q/%q; they are the conflict policy's, not this mode's", username, email)
	}

	// Channel metadata, but never the handle.
	var chanName, chanDesc, chanHandle string
	if err := dest.QueryRow(ctx, `SELECT display_name, description, handle FROM channels WHERE id=$1`, chanID).Scan(&chanName, &chanDesc, &chanHandle); err != nil {
		t.Fatalf("read channel: %v", err)
	}
	if chanName != "Alice Channel (renamed)" || chanDesc != "new blurb" {
		t.Errorf("channel = %q/%q, want the source's renamed channel", chanName, chanDesc)
	}
	if chanHandle != "alice_channel" {
		t.Errorf("channel handle changed to %q; the handle is this instance's public URL and the natural key", chanHandle)
	}

	// Ratings: alice's like became a dislike, and bob's deleted row took his
	// rating with it.
	ratings := scanStrings(t, ctx, dest, `SELECT rating FROM video_ratings WHERE video_id=$1 ORDER BY rating`, vidID)
	if len(ratings) != 1 || ratings[0] != "dislike" {
		t.Errorf("ratings = %v, want [dislike] (alice changed her vote; bob's was deleted on the source)", ratings)
	}

	// Playlist: renamed, and the removed slot is gone.
	var playlistTitle string
	if err := dest.QueryRow(ctx, `SELECT title FROM playlists WHERE id=$1`, playlistID).Scan(&playlistTitle); err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if playlistTitle != "My Playlist (renamed)" {
		t.Errorf("playlist title = %q, want the source's", playlistTitle)
	}
	var items int
	if err := dest.QueryRow(ctx, `SELECT count(*) FROM playlist_items WHERE playlist_id=$1`, playlistID).Scan(&items); err != nil {
		t.Fatalf("read playlist items: %v", err)
	}
	if items != 1 {
		t.Errorf("playlist items = %d, want 1 (the source removed one)", items)
	}

	// The unsubscribe followed.
	if n := countRows(t, ctx, dest, "channel_follows"); n != 0 {
		t.Errorf("channel_follows = %d, want 0 — the source no longer has that subscription", n)
	}

	// The taxonomy the operator replaced by hand is overwritten, because that is
	// what asking for the source to win means.
	if got := readInstanceSetting(t, ctx, dest, "instance_custom_categories"); got != `["51:Giantess","52:Shrunken"]` {
		t.Errorf("instance_custom_categories = %s, want the source's taxonomy", got)
	}

	// RULE 2 — the actor keypair is byte-identical. Rotating a live federation key
	// invalidates every HTTP signature already in flight.
	var pubAfter, privAfter string
	if err := dest.QueryRow(ctx, `SELECT public_key_pem, private_key_pem FROM account_actor_keys WHERE user_id=$1`, aliceID).Scan(&pubAfter, &privAfter); err != nil {
		t.Fatalf("read actor key: %v", err)
	}
	if pubAfter != alicePub || privAfter != alicePriv {
		t.Fatal("the resync rewrote an actor keypair — every outstanding HTTP signature is now invalid")
	}

	// RULE 3 — the view counter is still a delta. 100 carried + 7 Vidra served + 20
	// the source gained = 127. Assigning the source's 120 would erase Vidra's 7.
	var views int64
	if err := dest.QueryRow(ctx, `SELECT views FROM video_view_counts WHERE video_id=$1`, vidID).Scan(&views); err != nil {
		t.Fatalf("read views: %v", err)
	}
	if views != 127 {
		t.Errorf("views = %d, want 127 (100 carried + 7 served here + 20 gained on the source); assignment would give 120", views)
	}

	// RULE 4 — the rendition a Vidra re-transcode wrote is untouched.
	var prefix string
	if err := dest.QueryRow(ctx, `SELECT key_prefix FROM video_renditions WHERE video_id=$1 AND height=720`, vidID).Scan(&prefix); err != nil {
		t.Fatalf("read rendition: %v", err)
	}
	if prefix != "hls/vidra-transcode/" {
		t.Errorf("rendition key_prefix = %q, want Vidra's own — the per-rung download now points at a source tree with no such asset", prefix)
	}

	// THE OWNERSHIP RULE — the natively-created video is exactly as it was.
	var nativeTitle string
	if err := dest.QueryRow(ctx, `SELECT title FROM videos WHERE id=$1`, nativeID).Scan(&nativeTitle); err != nil {
		t.Fatalf("read native video: %v", err)
	}
	if nativeTitle != "Uploaded Here" {
		t.Errorf("the natively-uploaded video's title is now %q", nativeTitle)
	}
	if got := scanStrings(t, ctx, dest, `SELECT tag FROM video_tags WHERE video_id=$1`, nativeID); len(got) != 1 || got[0] != "native" {
		t.Errorf("the natively-uploaded video's tags = %v, want [native]", got)
	}
	if got := scanStrings(t, ctx, dest, `SELECT title FROM video_chapters WHERE video_id=$1`, nativeID); len(got) != 1 {
		t.Errorf("the natively-uploaded video's chapters = %v, want the one it was given here", got)
	}

	// RULE 6 — the fast re-run. A source-authoritative run against an UNCHANGED
	// source must change nothing and must not read the source per entity: the
	// families the older passes read one video (or one playlist) at a time get
	// bulk reads under this mode, and losing that turns a 21-second no-op re-run
	// into minutes over an SSH tunnel.
	before := readTimestamps(t, ctx, dest, vidID, aliceID, chanID, playlistID)
	srcTrace.reset()
	report, err = resync.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("unchanged source-authoritative re-run: %v", err)
	}
	for kind, counts := range report.Entities {
		if counts.Updated != 0 || counts.Imported != 0 {
			t.Errorf("an unchanged re-run reported %s imported=%d updated=%d, want 0/0",
				kind, counts.Imported, counts.Updated)
		}
	}
	if after := readTimestamps(t, ctx, dest, vidID, aliceID, chanID, playlistID); after != before {
		t.Errorf("an unchanged re-run bumped updated_at (%v -> %v); it issued writes it did not need to", before, after)
	}
	// The needles are the PER-ENTITY forms of the two reads (they are the only
	// statements that bind an entity id), so they count round trips that scale
	// with the catalogue — the thing that would break the re-run — rather than the
	// bulk statements that replaced them.
	if n := srcTrace.countMatching(`WHERE vt."videoId" = $1`); n != 0 {
		t.Errorf("the re-run issued %d per-video tag reads against the source; tags must come from the single bulk read", n)
	}
	if n := srcTrace.countMatching(`WHERE "videoPlaylistId" = $1`); n != 0 {
		t.Errorf("the re-run issued %d per-playlist element reads against the source; they must come from the single bulk read", n)
	}
	if n := srcTrace.countMatching(`FROM "videoTag" vt`); n != 1 {
		t.Errorf("the source's tags were read %d times, want exactly one bulk statement", n)
	}
	if n := srcTrace.countMatching(`FROM "videoPlaylistElement" e`); n != 1 {
		t.Errorf("the source's playlist elements were read %d times, want exactly one bulk statement", n)
	}
	// The chapter set is still two marks — a re-run that "replaced" an unchanged
	// set would be both a write and a risk.
	if n := countRows(t, ctx, dest, "video_chapters"); n != 3 {
		t.Errorf("chapters = %d, want 3 (two imported + the one created here)", n)
	}
}

// readTimestamps returns the updated_at of the four rows the resync can write.
// They are set by the UPDATE statements themselves, so an unchanged pair proves
// no UPDATE ran — a stronger claim than a row count, which a rewrite of the same
// values would satisfy.
func readTimestamps(t *testing.T, ctx context.Context, pool *pgxpool.Pool, video, user, channel, playlist uuid.UUID) [4]time.Time {
	t.Helper()
	var out [4]time.Time
	for i, q := range []struct {
		sql string
		id  uuid.UUID
	}{
		{`SELECT updated_at FROM videos WHERE id=$1`, video},
		{`SELECT updated_at FROM users WHERE id=$1`, user},
		{`SELECT updated_at FROM channels WHERE id=$1`, channel},
		{`SELECT updated_at FROM playlists WHERE id=$1`, playlist},
	} {
		if err := pool.QueryRow(ctx, q.sql, q.id).Scan(&out[i]); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
	}
	return out
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

// TestPeerTubeSourceActorImagesPicksOneVariantPerSlot is the proof for the read
// half of the resolution bug: PeerTube keeps SEVERAL resolutions of every avatar
// and the old read returned all of them, so four rows raced for one destination
// key at concurrency 4 and whichever finished last won. On a real migration that
// produced 1,316 rows for 309 actors and left 137 of 229 user avatars under 5 KB
// while 2.1 MB originals sat in the source.
func TestPeerTubeSourceActorImagesPicksOneVariantPerSlot(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
	// The shape a live instance actually stores: one upload, several generated
	// sizes, the small ones first because they were inserted first. Alice's
	// channel avatar keeps a NULL size next to a real one — that pair is what
	// catches a plain `width DESC`, which sorts NULLs FIRST in PostgreSQL and so
	// would prefer the row whose size the source never recorded.
	mustExec(t, ctx, src, `UPDATE "actorImage" SET width = 48, height = 48 WHERE id = 1`)
	mustExec(t, ctx, src, `INSERT INTO "actorImage" (id,filename,type,"actorId",width,height) VALUES
		(20,'alice-avatar-120.png',1,1,120,120),
		(21,'alice-avatar-1500.png',1,1,1500,1500),
		(22,'alice-avatar-600.png',1,1,600,600),
		(23,'alice-channel-avatar-500.png',1,2,500,500)`)

	images, ok, err := NewSourceFromPool(src).ActorImages(ctx)
	if err != nil || !ok {
		t.Fatalf("ActorImages = %v, %v", ok, err)
	}
	type slot struct {
		user, channel int64
		kind          int
	}
	seen := map[slot]SourceActorImage{}
	for _, img := range images {
		k := slot{kind: img.Type}
		if img.UserID != nil {
			k.user = *img.UserID
		}
		if img.ChannelID != nil {
			k.channel = *img.ChannelID
		}
		if dup, exists := seen[k]; exists {
			t.Fatalf("slot %+v got two rows (%d %q and %d %q); every one of them writes the same destination key, so the pass would race with itself",
				k, dup.ID, dup.Filename, img.ID, img.Filename)
		}
		seen[k] = img
	}
	if got := seen[slot{user: 1, kind: 1}]; got.Filename != "alice-avatar-1500.png" || got.Width != 1500 {
		t.Errorf("alice's avatar = %q (%dpx), want the 1500px original — the largest variant, not whichever row sorted first", got.Filename, got.Width)
	}
	if got := seen[slot{channel: 1, kind: 1}]; got.Filename != "alice-channel-avatar-500.png" {
		t.Errorf("alice_channel's avatar = %q, want the 500px row: a recorded size must beat a NULL one", got.Filename)
	}
	// The other slots are untouched by dedup and must all still be there.
	for _, want := range []struct {
		s        slot
		filename string
	}{
		{slot{user: 1, kind: 2}, "alice-banner.png"},
		{slot{user: 2, kind: 1}, "bob-avatar.png"},
		{slot{user: 2, kind: 9}, "weird.png"},
		{slot{channel: 2, kind: 1}, "bob-channel-avatar.png"},
	} {
		if got := seen[want.s]; got.Filename != want.filename {
			t.Errorf("slot %+v = %q, want %q", want.s, got.Filename, want.filename)
		}
	}
	if len(images) != 6 {
		t.Errorf("got %d rows for 6 slots: %+v", len(images), images)
	}
}

// A source that records no sizes still has the race, and collapsing to one row
// per slot is what removes it — the size columns only decide WHICH row wins.
// They are probed through information_schema rather than assumed, because a
// column that is not there is a syntax error and not a NULL.
func TestPeerTubeSourceActorImagesWithoutSizeColumns(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
	mustExec(t, ctx, src, `INSERT INTO "actorImage" (id,filename,type,"actorId") VALUES
		(20,'alice-avatar-b.png',1,1),
		(21,'alice-avatar-c.png',1,1)`)
	mustExec(t, ctx, src, `ALTER TABLE "actorImage" DROP COLUMN width, DROP COLUMN height`)

	images, ok, err := NewSourceFromPool(src).ActorImages(ctx)
	if err != nil || !ok {
		t.Fatalf("ActorImages = %v, %v", ok, err)
	}
	var aliceAvatars []SourceActorImage
	for _, img := range images {
		if img.UserID != nil && *img.UserID == 1 && img.Type == 1 {
			aliceAvatars = append(aliceAvatars, img)
		}
		if img.Width != 0 || img.Height != 0 {
			t.Errorf("row %d reports %dx%d on a source that records no sizes", img.ID, img.Width, img.Height)
		}
	}
	if len(aliceAvatars) != 1 {
		t.Fatalf("alice's avatar slot got %d rows, want 1 — the dedup, not the sizes, is what fixes the race", len(aliceAvatars))
	}
	if aliceAvatars[0].ID != 21 {
		t.Errorf("chose row %d, want 21: with no size to compare, the newest row is the best evidence available", aliceAvatars[0].ID)
	}
}

// TestPeerTubeImportActorImageOwnership is the proof for the write half: an
// import that never overwrites can never repair its own mistake, and one that
// always overwrites quietly undoes a person's work every night. The ledger's
// memory of what the import itself wrote is what separates the two.
func TestPeerTubeImportActorImageOwnership(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	// Two variants of one upload, distinguishable by stored SIZE so every
	// assertion below can say which one is in the slot.
	smallPNG := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("s"), 32)...)
	largePNG := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("L"), 4096)...)
	var (
		mu        sync.Mutex
		requests  = map[string]int{}
		failLarge bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		mu.Lock()
		requests[name]++
		down := failLarge
		mu.Unlock()
		if name == "alice-large.png" && down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		switch name {
		case "alice-small.png":
			_, _ = w.Write(smallPNG)
		case "alice-large.png":
			_, _ = w.Write(largePNG)
		default:
			_, _ = w.Write(pngBytes)
		}
	}))
	defer srv.Close()
	hits := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		return requests[name]
	}
	setFailLarge := func(v bool) {
		mu.Lock()
		failLarge = v
		mu.Unlock()
	}

	mustExec(t, ctx, src, `DELETE FROM "actorImage"`)
	mustExec(t, ctx, src, `INSERT INTO "actorImage" (id,filename,type,"actorId",width,height) VALUES (1,'alice-small.png',1,1,48,48)`)
	mustExec(t, ctx, src,
		`UPDATE "actor" SET url = $1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, srv.URL)

	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newImporter := func(sourceAuthoritative bool) *Importer {
		return NewImporter(dest, NewSourceFromPool(src), Options{
			Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: destMedia,
			SourceAuthoritative: sourceAuthoritative,
		})
	}
	imp := newImporter(false)
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var aliceID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM users WHERE username='alice'`).Scan(&aliceID); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	avatarSize := func() int64 {
		t.Helper()
		var n int64
		if err := dest.QueryRow(ctx, `SELECT size_bytes FROM user_images WHERE user_id=$1 AND kind='avatar'`, aliceID).Scan(&n); err != nil {
			t.Fatalf("read alice's avatar: %v", err)
		}
		return n
	}
	if got := avatarSize(); got != int64(len(smallPNG)) {
		t.Fatalf("first run stored %d bytes, want the source's only variant (%d)", got, len(smallPNG))
	}

	// ── an unchanged source costs nothing: no fetch, no PUT ──
	report, err := newImporter(false).Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := hits("alice-small.png"); got != 1 {
		t.Errorf("alice's avatar was fetched %d times across two runs, want 1", got)
	}
	if got := report.Entities[KindActorAvatar].Imported; got != 0 {
		t.Errorf("second run imported %d avatars, want 0", got)
	}

	// ── the state an instance migrated with the resolution bug is actually in ──
	// Both variants carry a completed ledger row (the old pass wrote all of them,
	// racing for one key) and neither carries a fingerprint, because that memory
	// did not exist yet. The slot holds the SMALL one, which is how 137 of 229
	// avatars ended up as thumbnails.
	mustExec(t, ctx, src, `INSERT INTO "actorImage" (id,filename,type,"actorId",width,height) VALUES (2,'alice-large.png',1,1,1500,1500)`)
	mustExec(t, ctx, dest, `UPDATE peertube_import_ledger SET applied_value = '' WHERE entity_kind = 'actor_avatar'`)
	mustExec(t, ctx, dest,
		`INSERT INTO peertube_import_ledger (entity_kind, source_id, vidra_id, status, note, applied_value)
		 VALUES ('actor_avatar', '2', $1, 'done', '', '')`, aliceID)

	// A transient failure DURING the heal must not cost the slot its ownership
	// memory: a 'failed' stamp over the completed row would erase the only
	// evidence the import owns this avatar, and every later run would read a
	// person's picture where there is none and refuse to touch it forever.
	// Reduced here to the one completed row so that row IS the only memory —
	// which is exactly the shape of an actor whose source offers one variant.
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind='actor_avatar' AND source_id='1'`)
	setFailLarge(true)
	report, err = newImporter(false).Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("failing healing run: %v", err)
	}
	if got := report.Entities[KindActorAvatar].Failed; got != 1 {
		t.Fatalf("failed = %d, want 1 (the source 500'd)", got)
	}
	if got := avatarSize(); got != int64(len(smallPNG)) {
		t.Fatalf("a failed fetch changed the slot to %d bytes", got)
	}
	setFailLarge(false)

	if _, err := newImporter(false).Run(ctx, version, nil); err != nil {
		t.Fatalf("healing run: %v", err)
	}
	if got := avatarSize(); got != int64(len(largePNG)) {
		t.Fatalf("healing run left %d bytes in the slot, want the full-size original (%d) — an import that can never replace its own write can never repair this", got, len(largePNG))
	}

	// ── a person uploads their own avatar: never overwritten, always reported ──
	mustExec(t, ctx, dest,
		`UPDATE user_images SET size_bytes = 4242, updated_at = now() WHERE user_id = $1 AND kind = 'avatar'`, aliceID)
	before := hits("alice-large.png")
	report, err = newImporter(false).Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("gap-fill run: %v", err)
	}
	if got := avatarSize(); got != 4242 {
		t.Errorf("size_bytes = %d, want 4242 — an import must never overwrite an image a person put there", got)
	}
	if got := hits("alice-large.png"); got != before {
		t.Errorf("the source was asked for %d more images; a slot the import will not write must not be fetched either", got-before)
	}
	var conflict bool
	for _, c := range report.Conflicts {
		if strings.Contains(c, "avatar") && strings.Contains(c, "left unchanged") {
			conflict = true
		}
	}
	if !conflict {
		t.Errorf("nothing in the report says the avatar was left alone: %v — divergence nobody is told about is the failure this guards against", report.Conflicts)
	}

	// ── the seam: same state, source-authoritative, opposite outcome ──
	if _, err := newImporter(true).Run(ctx, version, nil); err != nil {
		t.Fatalf("source-authoritative run: %v", err)
	}
	if got := avatarSize(); got != int64(len(largePNG)) {
		t.Fatalf("source-authoritative run left %d bytes, want the source's variant (%d)", got, len(largePNG))
	}

	// ...and it is still not a re-fetch-everything mode: once the two sides agree
	// it writes exactly as little as gap-fill does.
	before = hits("alice-large.png")
	if _, err := newImporter(true).Run(ctx, version, nil); err != nil {
		t.Fatalf("second source-authoritative run: %v", err)
	}
	if got := hits("alice-large.png"); got != before {
		t.Errorf("source-authoritative re-fetched %d images from a source that had not moved", got-before)
	}
}

// An oversize source image is a fact about the source: it will be exactly as big
// next time. Recorded as a plain failure it was retried on every single run and
// failed identically every time — 5 rows of permanent, self-inflicted load on a
// live production instance.
func TestPeerTubeImportActorImageOversizeIsTerminal(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	oversize := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("x"), maxLazyStaticBytes)...)
	var (
		mu       sync.Mutex
		requests = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		mu.Lock()
		requests[name]++
		mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		if name == "alice-huge.png" {
			_, _ = w.Write(oversize)
			return
		}
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	mustExec(t, ctx, src, `DELETE FROM "actorImage"`)
	mustExec(t, ctx, src, `INSERT INTO "actorImage" (id,filename,type,"actorId",width,height) VALUES (1,'alice-huge.png',1,1,4000,4000)`)
	mustExec(t, ctx, src,
		`UPDATE "actor" SET url = $1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, srv.URL)

	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: destMedia,
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := report.Entities[KindActorAvatar].Unsupported; got != 1 {
		t.Errorf("unsupported = %d, want 1 — too big is a fact about the source, not a transient error", got)
	}
	if got := report.Entities[KindActorAvatar].Failed; got != 0 {
		t.Errorf("failed = %d, want 0 — a failed row is retried forever and this one can only ever fail", got)
	}
	var status string
	if err := dest.QueryRow(ctx,
		`SELECT status FROM peertube_import_ledger WHERE entity_kind='actor_avatar' AND source_id='1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unsupported" {
		t.Fatalf("ledger status = %q, want unsupported", status)
	}

	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("second run: %v", err)
	}
	mu.Lock()
	got := requests["alice-huge.png"]
	mu.Unlock()
	if got != 1 {
		t.Errorf("the oversize image was downloaded %d times across two runs, want 1", got)
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
	version, err := imp.Preflight(ctx)
	if err == nil {
		t.Error("preflight must REFUSE an unverified (too-old) version without --force")
	}
	// The refused version comes back anyway: it is the number an operator has to
	// be shown before they can be asked to accept it.
	if version != 500 {
		t.Errorf("refused preflight returned version %d, want 500", version)
	}
	var refusal *UnverifiedSchemaError
	if !errors.As(err, &refusal) || refusal.Code() != CodeUnverifiedSchema {
		t.Errorf("refusal = %v, want an *UnverifiedSchemaError coded %q", err, CodeUnverifiedSchema)
	}

	// A human passing --force overrides the refusal (agents must never do this).
	impForce := NewImporter(dest, NewSourceFromPool(src), Options{Force: true})
	if _, err := impForce.Preflight(ctx); err != nil {
		t.Errorf("preflight with force should proceed: %v", err)
	}

	// An acknowledgement NAMING the detected version is the admin path's sign-off
	// and opens the same gate — nothing more.
	impAck := NewImporter(dest, NewSourceFromPool(src), Options{AcknowledgedSchemaVersion: 500})
	if _, err := impAck.Preflight(ctx); err != nil {
		t.Errorf("preflight with an acknowledgement of the detected version should proceed: %v", err)
	}

	// An acknowledgement of some OTHER version is no acknowledgement of this one.
	// This is what stops a stale sign-off from outliving the source it was made
	// against, and what stops a caller that never looked from guessing one.
	for _, ack := range []int{499, 501, 800, -500} {
		impWrong := NewImporter(dest, NewSourceFromPool(src), Options{AcknowledgedSchemaVersion: ack})
		if _, err := impWrong.Preflight(ctx); err == nil {
			t.Errorf("preflight accepted an acknowledgement of %d against a detected version of 500", ack)
		}
	}
}

// A source whose version cannot be READ at all is refused with a DIFFERENT class,
// and no acknowledgement can lift it: an acknowledgement is a statement about a
// specific version, and there is no version here to make it about. Only a human
// on the CLI, who can go and look at the source, gets past this one.
func TestPeerTubeImportPreflightUndetectableVersion(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	// An application table with no row at all — version undetectable.
	mustExec(t, ctx, src, `CREATE TABLE "application" (id serial PRIMARY KEY, "migrationVersion" integer NOT NULL)`)

	_, err := NewImporter(dest, NewSourceFromPool(src), Options{}).Preflight(ctx)
	var refusal *UnverifiedSchemaError
	if !errors.As(err, &refusal) || refusal.Code() != CodeUndetectableSchema {
		t.Fatalf("refusal = %v, want an *UnverifiedSchemaError coded %q", err, CodeUndetectableSchema)
	}
	if refusal.Acknowledgeable() {
		t.Error("an undetectable version must not be presented as acknowledgeable")
	}
	for _, ack := range []int{0, 1, 1040} {
		if _, err := NewImporter(dest, NewSourceFromPool(src), Options{AcknowledgedSchemaVersion: ack}).Preflight(ctx); err == nil {
			t.Errorf("acknowledgement of %d lifted the undetectable-version stop", ack)
		}
	}
	// --force still does, because a human ran it.
	if _, err := NewImporter(dest, NewSourceFromPool(src), Options{Force: true}).Preflight(ctx); err != nil {
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
	// Real JPEG magic, because the import sniffs what these bytes ARE before it
	// stores them — the gate that stops an HTML error page becoming a poster.
	write("thumbnails/v1-thumb.jpg", jpegBytes)
	write("storyboards/v1-storyboard.jpg", jpegBytes)
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
		// password is NULLABLE, exactly as PeerTube declares it (@AllowNull(true)):
		// an LDAP/OIDC/SAML plugin-auth user has no locally stored password. The
		// old fixture said NOT NULL, which is why no test here could express the
		// case that aborts a whole run.
		// blocked is PeerTube's account SUSPENSION (@AllowNull(false) @Default(false)).
		// The fixture omitted it, which is why no test here could catch an importer
		// that stood every suspended account up with a working password.
		`CREATE TABLE "user" (
			id serial PRIMARY KEY, username text NOT NULL, email text NOT NULL, password text,
			role integer NOT NULL, "emailVerified" boolean, blocked boolean NOT NULL DEFAULT false,
			"createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoChannel" (
			id serial PRIMARY KEY, name text NOT NULL, description text, "accountId" integer NOT NULL,
			"actorId" integer NOT NULL, "createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "video" (
			id serial PRIMARY KEY, uuid uuid NOT NULL, "channelId" integer NOT NULL, name text NOT NULL,
			description text, privacy integer NOT NULL, state integer NOT NULL, category integer, licence integer,
			language text, duration integer NOT NULL DEFAULT 0, views integer NOT NULL DEFAULT 0,
			nsfw boolean NOT NULL DEFAULT false,
			"originallyPublishedAt" timestamptz,
			"createdAt" timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE "videoFile" (
			id serial PRIMARY KEY, "videoId" integer, "videoStreamingPlaylistId" integer,
			resolution integer NOT NULL, size bigint NOT NULL, extname text, fps integer, filename text)`,
		`CREATE TABLE "videoStreamingPlaylist" (
			id serial PRIMARY KEY, "videoId" integer NOT NULL, "playlistFilename" text NOT NULL)`,
		`CREATE TABLE "thumbnail" (
			id serial PRIMARY KEY, filename text NOT NULL, type integer NOT NULL, "videoId" integer,
			height integer, width integer)`,
		// PeerTube writes NO migration for this table — it is created by
		// sequelizeTypescript.sync() on boot — which is why the importer probes
		// information_schema for it instead of inferring it from the version.
		`CREATE TABLE "storyboard" (
			id serial PRIMARY KEY, filename text NOT NULL,
			"totalHeight" integer NOT NULL, "totalWidth" integer NOT NULL,
			"spriteHeight" integer NOT NULL, "spriteWidth" integer NOT NULL,
			"spriteDuration" integer NOT NULL,
			"fileUrl" text, cached boolean NOT NULL DEFAULT false,
			"videoId" integer NOT NULL UNIQUE,
			"createdAt" timestamptz NOT NULL DEFAULT now(), "updatedAt" timestamptz NOT NULL DEFAULT now())`,
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
		// videoBlacklist is a table of its own and touches NEITHER video.privacy nor
		// video.state, so a blacklisted video is indistinguishable from a published
		// one in every column the importer reads. Its mapping onto video_blocks
		// lands in its own PR; the table is here now so that PR is a mapping change
		// and not a fixture change.
		`CREATE TABLE "videoBlacklist" (
			id serial PRIMARY KEY, "videoId" integer NOT NULL UNIQUE, reason text,
			unfederated boolean NOT NULL DEFAULT false, type integer NOT NULL DEFAULT 1,
			"createdAt" timestamptz NOT NULL DEFAULT now())`,
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
		// bob is SUSPENDED on the source and alice is not: the fixture carries the
		// case, so a regression that fails the flag open shows up in every test
		// that imports this graph, not only in the one written for it.
		`INSERT INTO "user" (id,username,email,password,role,"emailVerified",blocked) VALUES
			(1,'alice','alice@example.test','` + passwordHash + `',2,true,false),
			(2,'bob','bob@example.test','` + passwordHash + `',0,false,true)`,
		`INSERT INTO "account" (id,name,"userId","actorId") VALUES
			(1,'Alice',1,1),(2,'Bob',2,3),(5,'Remote',NULL,5)`,
		`INSERT INTO "videoChannel" (id,name,description,"accountId","actorId") VALUES
			(1,'Alice Channel','a desc',1,2),(2,'Bob Channel','',2,4)`,
		// Video 1 carries an originallyPublishedAt (it was first published
		// elsewhere and imported INTO this PeerTube); video 2 does not, which is
		// the ordinary case for something first published on the source itself.
		// Video 2 is flagged nsfw on the source and video 1 is not, so the pass has
		// to carry a flag rather than set one for everything.
		`INSERT INTO "video" (id,uuid,"channelId",name,description,privacy,state,category,licence,language,duration,views,nsfw,"originallyPublishedAt") VALUES
			(1,'11111111-1111-1111-1111-111111111111',1,'First Video','hello',1,1,1,1,'en',120,100,false,'2016-04-01T12:30:00Z'),
			(2,'22222222-2222-2222-2222-222222222222',1,'Second Video','',3,1,NULL,NULL,NULL,60,0,true,NULL)`,
		`INSERT INTO "videoBlacklist" (id,"videoId",reason) VALUES (1,2,'staged for the video_blocks mapping')`,
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
		// A 2x2 grid of 192x108 sprites at 30s each. Video 1 is 120s long, so all
		// four cells hold a real frame; the tile count still comes from the
		// DURATION and not from the grid (see media.PlanFromSprites).
		`INSERT INTO "storyboard" (id,filename,"totalWidth","totalHeight","spriteWidth","spriteHeight","spriteDuration","videoId")
			VALUES (1,'v1-storyboard.jpg',384,216,192,108,30,1)`,
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

// ── video posters and storyboards ──

// PeerTube 8.1 unified previews and miniatures into one table and started
// writing ONE ROW PER CONFIGURED SIZE. Exactly one of them can be this video's
// poster here, so the read has to choose — and "whichever the source inserted
// first", which is what the old query took, lands on the 280x157 thumbnail.
func TestPeerTubeImportThumbnailVariantSelection(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	t.Run("an 8.2 source, which has no type column at all", func(t *testing.T) {
		src, _ := newScratchDB(t, ctx, base)
		seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
		// The 8.1 refactor DROPPED `type`; the sizes are the shipped defaults plus
		// the 1400x1400 square PeerTube generates "for podcast applications".
		mustExec(t, ctx, src, `DELETE FROM "thumbnail"`)
		mustExec(t, ctx, src, `ALTER TABLE "thumbnail" DROP COLUMN type`)
		mustExec(t, ctx, src, `INSERT INTO "thumbnail" (id,filename,"videoId",width,height) VALUES
			(1,'v1-280.jpg',1,280,157),
			(2,'v1-850.jpg',1,850,480),
			(3,'v1-1280.jpg',1,1280,720),
			(4,'v1-1920.jpg',1,1920,1080),
			(5,'v1-square.jpg',1,1400,1400)`)

		thumbs, present, err := NewSourceFromPool(src).VideoThumbnails(ctx)
		if err != nil {
			t.Fatalf("read thumbnails: %v", err)
		}
		if !present {
			t.Fatal("the source has a thumbnail table")
		}
		if len(thumbs) != 1 {
			t.Fatalf("got %d rows, want exactly one per video — five variants all name the same poster here", len(thumbs))
		}
		// 1400x1400 has 1.96M pixels to 1920x1080's 2.07M, so largest-by-area alone
		// would be right here by luck; the square rule is what makes it right when
		// the admin's configured sizes are not the shipped ones. Both are asserted.
		if thumbs[0].Filename != "v1-1920.jpg" {
			t.Fatalf("chose %q (%dx%d), want the 1920x1080 variant", thumbs[0].Filename, thumbs[0].Width, thumbs[0].Height)
		}
	})

	t.Run("a square variant never wins on area alone", func(t *testing.T) {
		src, _ := newScratchDB(t, ctx, base)
		seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
		mustExec(t, ctx, src, `DELETE FROM "thumbnail"`)
		mustExec(t, ctx, src, `ALTER TABLE "thumbnail" DROP COLUMN type`)
		// An instance whose configured square is BIGGER than its widescreen. Area
		// alone would pick it, and every card in the catalogue would be letterboxed.
		mustExec(t, ctx, src, `INSERT INTO "thumbnail" (id,filename,"videoId",width,height) VALUES
			(1,'v1-1280.jpg',1,1280,720),
			(2,'v1-square.jpg',1,2000,2000)`)

		thumbs, _, err := NewSourceFromPool(src).VideoThumbnails(ctx)
		if err != nil {
			t.Fatalf("read thumbnails: %v", err)
		}
		if len(thumbs) != 1 || thumbs[0].Filename != "v1-1280.jpg" {
			t.Fatalf("chose %+v, want the 16:9 variant; Vidra renders one poster into 16:9 surfaces", thumbs)
		}
	})

	t.Run("a pre-8.1 source, where a PREVIEW beats a MINIATURE", func(t *testing.T) {
		src, _ := newScratchDB(t, ctx, base)
		seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
		// ThumbnailType = { MINIATURE: 1, PREVIEW: 2 }. The preview is the full-size
		// one, and the old query filtered to type = 1.
		mustExec(t, ctx, src, `DELETE FROM "thumbnail"`)
		mustExec(t, ctx, src, `INSERT INTO "thumbnail" (id,filename,type,"videoId",width,height) VALUES
			(1,'v1-miniature.jpg',1,1,223,122),
			(2,'v1-preview.jpg',2,1,850,480)`)

		thumbs, _, err := NewSourceFromPool(src).VideoThumbnails(ctx)
		if err != nil {
			t.Fatalf("read thumbnails: %v", err)
		}
		if len(thumbs) != 1 || thumbs[0].Filename != "v1-preview.jpg" {
			t.Fatalf("chose %+v, want the PREVIEW", thumbs)
		}
	})

	t.Run("a playlist thumbnail is not a video's", func(t *testing.T) {
		src, _ := newScratchDB(t, ctx, base)
		seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)
		mustExec(t, ctx, src, `ALTER TABLE "thumbnail" ADD COLUMN "videoPlaylistId" integer`)
		mustExec(t, ctx, src, `ALTER TABLE "thumbnail" ALTER COLUMN "videoId" DROP NOT NULL`)
		mustExec(t, ctx, src, `INSERT INTO "thumbnail" (id,filename,type,"videoId","videoPlaylistId")
			VALUES (99,'playlist-cover.jpg',1,NULL,1)`)

		thumbs, _, err := NewSourceFromPool(src).VideoThumbnails(ctx)
		if err != nil {
			t.Fatalf("read thumbnails: %v", err)
		}
		for _, th := range thumbs {
			if th.Filename == "playlist-cover.jpg" {
				t.Fatal("a videoPlaylistId row was read as a video's poster")
			}
		}
	})
}

// A source too old to have storyboards at all. PeerTube writes no migration for
// that table — it is schema-synced on boot — so its absence cannot be inferred
// from the version and MUST be a clean deferral rather than a failed run.
func TestPeerTubeImportStoryboardTableAbsentIsDeferred(t *testing.T) {
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
	mustExec(t, ctx, src, `DROP TABLE "storyboard"`)

	srcMediaDir := t.TempDir()
	seedSourceMedia(t, srcMediaDir)
	srcMedia, err := storage.NewLocal(srcMediaDir)
	if err != nil {
		t.Fatal(err)
	}
	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imp := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy: PolicySkip, SrcMedia: srcMedia, DestMedia: destMedia,
	})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v — a source without the table is a family it does not have, not a failure", err)
	}
	var deferred bool
	for _, d := range report.Deferred {
		if strings.Contains(d, "storyboard") {
			deferred = true
		}
	}
	if !deferred {
		t.Errorf("nothing in the report mentions storyboards: %v", report.Deferred)
	}
	if got := report.Entities[KindStoryboard].Failed; got != 0 {
		t.Errorf("storyboard failures = %d, want 0", got)
	}
	if n := countRows(t, ctx, dest, "video_files WHERE kind LIKE 'storyboard%'"); n != 0 {
		t.Errorf("%d storyboard rows written for a source that has none", n)
	}
	// ...and the posters, which the same source DOES have, still come across.
	if got := report.Entities[KindThumbnail].Imported; got != 1 {
		t.Errorf("thumbnails imported = %d, want 1 — one family's absence must not cost another", got)
	}
}

// The instance this whole change was written for: ~12–16k videos whose
// kind='thumbnail' row points at thumbnails/<peertube-filename>, an object
// PeerTube never stored. has_thumbnail said true and GET /thumbnail 404'd on 40
// of 40 sampled. The old importer wrote no ledger row for a poster, so the key
// shape and the object are the only provenance there is — once.
func TestPeerTubeImportThumbnailRepairsWhatAnOlderReleaseWrote(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

	src, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, src, string(hash), secretPrivKeyAlice)

	var (
		mu       sync.Mutex
		requests = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[path.Base(r.URL.Path)]++
		mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	}))
	defer srv.Close()
	hits := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		return requests[name]
	}
	mustExec(t, ctx, src,
		`UPDATE "actor" SET url = $1 || '/accounts/' || "preferredUsername" WHERE "serverId" IS NULL`, srv.URL)

	destMedia, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newImporter := func(sourceAuthoritative bool) *Importer {
		return NewImporter(dest, NewSourceFromPool(src), Options{
			Policy: PolicySkip, MediaMode: MediaModeReference, DestMedia: destMedia,
			SourceAuthoritative: sourceAuthoritative,
		})
	}
	imp := newImporter(false)
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
	nativeKey := "thumbnails/" + vidID.String() + ".jpg"
	poster := func() (string, int64) {
		t.Helper()
		var key string
		var size int64
		if err := dest.QueryRow(ctx,
			`SELECT storage_key, size_bytes FROM video_files WHERE video_id=$1 AND kind='thumbnail'`, vidID).Scan(&key, &size); err != nil {
			t.Fatalf("read poster: %v", err)
		}
		return key, size
	}
	if key, _ := poster(); key != nativeKey {
		t.Fatalf("first run stored the poster at %q, want %q", key, nativeKey)
	}
	if ok, _ := destMedia.Exists(ctx, nativeKey); !ok {
		t.Fatal("the poster row points at an object that is not there")
	}

	// ── an unchanged source costs nothing: no fetch, no PUT ──
	before := hits("v1-thumb.jpg")
	report, err := newImporter(false).Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := hits("v1-thumb.jpg"); got != before {
		t.Errorf("the source was asked for %d more posters; an unchanged slot must be settled from the database", got-before)
	}
	if got := report.Entities[KindThumbnail].Imported; got != 0 {
		t.Errorf("second run imported %d posters, want 0", got)
	}

	// ── the state a reference-mode migration is actually in ──
	// A row at the SOURCE's key, no object behind it, and no ledger provenance
	// because the old importer recorded none.
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind = 'thumbnail'`)
	mustExec(t, ctx, dest,
		`UPDATE video_files SET storage_key = 'thumbnails/v1-thumb.jpg', size_bytes = 0 WHERE video_id = $1 AND kind = 'thumbnail'`, vidID)
	_ = destMedia.Delete(ctx, nativeKey)
	if _, err := newImporter(false).Run(ctx, version, nil); err != nil {
		t.Fatalf("healing run: %v", err)
	}
	key, size := poster()
	if key != nativeKey || size != int64(len(jpegBytes)) {
		t.Fatalf("healing run left the poster at %q (%d bytes); a row whose object is not there is a gap in any mode", key, size)
	}
	if ok, _ := destMedia.Exists(ctx, nativeKey); !ok {
		t.Fatal("the healed poster row still points at nothing")
	}

	// ── the configuration that ALWAYS worked must not regress ──
	// --source-local-root copy mode wrote thumbnails/<random-uuid>.jpg AND the
	// bytes. No ledger provenance, a non-native key — and a perfectly good poster.
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind = 'thumbnail'`)
	legacyKey := "thumbnails/" + uuid.NewString() + ".jpg"
	if _, err := destMedia.Put(ctx, legacyKey, bytes.NewReader(jpegBytes)); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, dest,
		`UPDATE video_files SET storage_key = $2, size_bytes = $3 WHERE video_id = $1 AND kind = 'thumbnail'`,
		vidID, legacyKey, int64(len(jpegBytes)))
	before = hits("v1-thumb.jpg")
	if _, err := newImporter(false).Run(ctx, version, nil); err != nil {
		t.Fatalf("adoption run: %v", err)
	}
	if key, _ := poster(); key != legacyKey {
		t.Errorf("poster moved to %q; a copy-mode migration's own working poster must be left exactly as it is", key)
	}
	if got := hits("v1-thumb.jpg"); got != before {
		t.Errorf("the source was asked for %d more posters; a working poster costs no fetch", got-before)
	}

	// ── a creator's poster is never written over ──
	// SetThumbnail lands on the NATIVE key, which is what stops the bridge above
	// claiming it.
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind = 'thumbnail'`)
	if _, err := destMedia.Put(ctx, nativeKey, bytes.NewReader(append(jpegBytes, 'x'))); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, dest,
		`UPDATE video_files SET storage_key = $2, size_bytes = 4242 WHERE video_id = $1 AND kind = 'thumbnail'`, vidID, nativeKey)
	before = hits("v1-thumb.jpg")
	report, err = newImporter(false).Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("gap-fill run: %v", err)
	}
	if _, size := poster(); size != 4242 {
		t.Errorf("size_bytes = %d, want 4242 — an import must never overwrite a poster a creator uploaded", size)
	}
	if got := hits("v1-thumb.jpg"); got != before {
		t.Errorf("the source was asked for %d more posters; a slot the import will not write must not be fetched", got-before)
	}
	var conflict bool
	for _, c := range report.Conflicts {
		if strings.Contains(c, "thumbnail") && strings.Contains(c, "left unchanged") {
			conflict = true
		}
	}
	if !conflict {
		t.Errorf("nothing in the report says the poster was left alone: %v — divergence nobody is told about is the failure this guards against", report.Conflicts)
	}

	// ── the seam: same state, source-authoritative, opposite outcome ──
	if _, err := newImporter(true).Run(ctx, version, nil); err != nil {
		t.Fatalf("source-authoritative run: %v", err)
	}
	if _, size := poster(); size != int64(len(jpegBytes)) {
		t.Errorf("source-authoritative run left %d bytes, want the source's poster (%d)", size, len(jpegBytes))
	}
}

// ── originally_published_at (migration 0119) ──
//
// PeerTube records when a video was first published SOMEWHERE ELSE, and until
// now Vidra had nowhere to put it, so a 2016 talk migrated in 2026 read as a
// 2026 video. These three tests pin the whole path: the insert carries it, a
// pass of its own backfills the catalogue an earlier release already imported,
// and a source too old to have the column loses the dates and nothing else.

// The straight path: a source video that carries the date gets it, and one that
// does not stays NULL rather than being defaulted to anything.
func TestPeerTubeImportCarriesOriginallyPublishedAt(t *testing.T) {
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

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	want := time.Date(2016, 4, 1, 12, 30, 0, 0, time.UTC)
	if got := readOriginalDate(t, ctx, dest, "First Video"); got == nil || !got.Equal(want) {
		t.Errorf("First Video originally_published_at = %v, want %v", got, want)
	}
	// The source says nothing about this one, and NULL is the answer: it was
	// first published on the source itself.
	if got := readOriginalDate(t, ctx, dest, "Second Video"); got != nil {
		t.Errorf("Second Video originally_published_at = %v, want NULL", got)
	}

	c := report.Entities[KindVideoOriginalDate]
	if c == nil {
		t.Fatalf("the report carries no %q counter", KindVideoOriginalDate)
	}
	if c.Imported != 1 || c.Failed != 0 {
		t.Errorf("original dates = %+v, want 1 imported / 0 failed", c)
	}
	// The video with no date to carry is not counted at all — "there is no data
	// here" is not a skip, the same convention importViewCounts follows.
	if c.Skipped != 0 {
		t.Errorf("original dates skipped = %d, want 0 (a video with no date is not a skip)", c.Skipped)
	}

	// A re-run against an unchanged source writes nothing new — the ledger is
	// what makes the pass free the second time.
	report, err = imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if got := report.Entities[KindVideoOriginalDate].Imported; got != 0 {
		t.Errorf("unchanged re-run imported %d original dates, want 0", got)
	}
	if got := readOriginalDate(t, ctx, dest, "First Video"); got == nil || !got.Equal(want) {
		t.Errorf("originally_published_at after re-run = %v, want %v unchanged", got, want)
	}
}

// The reason the pass exists at all: a catalogue imported BEFORE the column
// existed. importOneVideo never runs again for a video with a terminal ledger
// row, so nothing folded into the video insert could ever reach those videos —
// only a pass with a ledger kind of its own does.
func TestPeerTubeImportBackfillsOriginallyPublishedAtOntoAnOlderImport(t *testing.T) {
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

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Rewind the destination to what an EARLIER release left behind: the videos
	// and their 'video' ledger rows are all there and terminal, but no date was
	// ever written and no date-pass ledger row exists.
	mustExec(t, ctx, dest, `UPDATE videos SET originally_published_at = NULL`)
	mustExec(t, ctx, dest, `DELETE FROM peertube_import_ledger WHERE entity_kind = $1`, KindVideoOriginalDate)
	if got := readOriginalDate(t, ctx, dest, "First Video"); got != nil {
		t.Fatalf("staging failed: originally_published_at = %v, want NULL", got)
	}

	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("backfill run: %v", err)
	}
	want := time.Date(2016, 4, 1, 12, 30, 0, 0, time.UTC)
	if got := readOriginalDate(t, ctx, dest, "First Video"); got == nil || !got.Equal(want) {
		t.Errorf("originally_published_at after the backfill run = %v, want %v", got, want)
	}
	if got := report.Entities[KindVideoOriginalDate].Imported; got != 1 {
		t.Errorf("backfill imported %d original dates, want 1", got)
	}
	// The video pass itself did nothing — proving the date arrived through the
	// backfill and not through a re-import.
	if got := report.Entities[KindVideo].Imported; got != 0 {
		t.Errorf("videos imported on the backfill run = %d, want 0", got)
	}
	if n := countRows(t, ctx, dest, "videos"); n != 2 {
		t.Errorf("videos = %d, want the same 2 — the backfill must not have re-inserted anything", n)
	}
}

// A source older than PeerTube's originallyPublishedAt column. Its absence is
// probed, never assumed, and it must cost the original dates and nothing else.
func TestPeerTubeImportOriginallyPublishedAtColumnAbsent(t *testing.T) {
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
	mustExec(t, ctx, src, `ALTER TABLE "video" DROP COLUMN "originallyPublishedAt"`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v — a source without the column has no dates to give, which is not a failure", err)
	}
	for _, title := range []string{"First Video", "Second Video"} {
		if got := readOriginalDate(t, ctx, dest, title); got != nil {
			t.Errorf("%s originally_published_at = %v, want NULL (the source has no such column)", title, got)
		}
	}
	c := report.Entities[KindVideoOriginalDate]
	if c.Failed != 0 || c.Imported != 0 {
		t.Errorf("original dates = %+v, want 0 imported / 0 failed", c)
	}
	// One family's absence must not cost another: the videos themselves and
	// their view totals still come across.
	if got := report.Entities[KindVideo].Imported; got != 2 {
		t.Errorf("videos imported = %d, want 2", got)
	}
	if got := report.Entities[KindViewCount].Imported; got != 1 {
		t.Errorf("view counts imported = %d, want 1", got)
	}
}

// readOriginalDate reads one video's originally_published_at, keeping NULL
// distinguishable from any instant.
func readOriginalDate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, title string) *time.Time {
	t.Helper()
	var got *time.Time
	if err := pool.QueryRow(ctx, `SELECT originally_published_at FROM videos WHERE title=$1`, title).Scan(&got); err != nil {
		t.Fatalf("read originally_published_at for %q: %v", title, err)
	}
	if got != nil {
		utc := got.UTC()
		return &utc
	}
	return nil
}

// The catastrophic combination, and the reason resyncOneVideo carries the stored
// value forward instead of letting a nil source value win: --source-authoritative
// against a source too old to HAVE originallyPublishedAt.
//
// Under that mode the source is the truth, so the naive reading is that a source
// reporting no date for every video should clear every date here. It must not.
// "No opinion" is not "clear it" — a source without the column has no opinion
// about ANY video, so the naive version silently erases the whole catalogue's
// original dates on the first run of the mode, and the dates only ever existed
// on the source that no longer reports them. There is nothing to restore from.
func TestPeerTubeImportSourceAuthoritativeKeepsDatesWhenSourceColumnAbsent(t *testing.T) {
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

	// A first, ordinary import off a source that still HAS the column: this is
	// the catalogue that will be at stake.
	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	want := time.Date(2016, 4, 1, 12, 30, 0, 0, time.UTC)
	if got := readOriginalDate(t, ctx, dest, "First Video"); got == nil || !got.Equal(want) {
		t.Fatalf("staging failed: originally_published_at = %v, want %v", got, want)
	}

	// The source loses the column — an operator pointing the tool at an older
	// instance, or a source downgraded between runs. Something else about the
	// video changes too, so the resync definitely REACHES this row and writes it:
	// a guard that only held because nothing was written would prove nothing.
	mustExec(t, ctx, src, `ALTER TABLE "video" DROP COLUMN "originallyPublishedAt"`)
	mustExec(t, ctx, src, `UPDATE "video" SET name='First Video (edited)' WHERE id=1`)

	resync := NewImporter(dest, NewSourceFromPool(src), Options{
		Policy: PolicySkip, MediaMode: MediaModeNone, SourceAuthoritative: true,
	})
	if _, err := resync.Run(ctx, version, nil); err != nil {
		t.Fatalf("source-authoritative run: %v", err)
	}

	// The edit the source DID make is carried...
	if got := scanStrings(t, ctx, dest, `SELECT title FROM videos WHERE title LIKE 'First Video%'`); len(got) != 1 || got[0] != "First Video (edited)" {
		t.Fatalf("title after the resync = %v, want [First Video (edited)] — the row was not reached, so this test proves nothing", got)
	}
	// ...and the date the source can no longer speak about SURVIVES.
	if got := readOriginalDate(t, ctx, dest, "First Video (edited)"); got == nil || !got.Equal(want) {
		t.Errorf("originally_published_at after a source-authoritative run against a column-less source = %v, want %v kept — a source with no opinion must not erase the catalogue", got, want)
	}
	// The video that never had one still has none: the guard preserves, it does
	// not invent.
	if got := readOriginalDate(t, ctx, dest, "Second Video"); got != nil {
		t.Errorf("Second Video originally_published_at = %v, want NULL", got)
	}
}

// ── plugin-auth users (no locally stored password) ──

// PeerTube declares user.password @AllowNull(true): a user authenticated by an
// LDAP/OIDC/SAML plugin has no locally stored password, so the column is NULL.
// The source read scanned that NULL straight into a Go string, which pgx v5
// rejects — and users is the FIRST family in parent-first order, so ONE such
// user on the source aborted the entire import before anything was written.
func TestPeerTubeImportCarriesAPluginAuthUserWithNoPassword(t *testing.T) {
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

	// carol signs in through an auth plugin: a local actor and account like any
	// other user, and a NULL password because the instance never stored one.
	mustExec(t, ctx, src, `INSERT INTO "actor" (id,type,"preferredUsername","publicKey","privateKey","serverId")
		VALUES (6,'Person','carol','PUBKEY-CAROL','PRIVKEY-CAROL-SECRET',NULL)`)
	mustExec(t, ctx, src, `INSERT INTO "user" (id,username,email,password,role,"emailVerified")
		VALUES (3,'carol','carol@example.test',NULL,2,true)`)
	mustExec(t, ctx, src, `INSERT INTO "account" (id,name,"userId","actorId") VALUES (6,'Carol',3,6)`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	// (a) the run completes. Before the fix this failed here, on the scan.
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v — one plugin-auth user must not abort the import", err)
	}
	// (b) every user imports, the password-less one included.
	if got := report.Entities[KindUser].Imported; got != 3 {
		t.Errorf("users imported = %d, want 3 (alice, bob, carol)", got)
	}
	if n := countRows(t, ctx, dest, "users"); n != 3 {
		t.Fatalf("users = %d, want 3 (remote excluded, carol included)", n)
	}
	// (c) carol lands with an EMPTY hash: not a valid bcrypt hash, so password
	// login can never verify — she is locked out of it, not crashing the run.
	var carolHash string
	if err := dest.QueryRow(ctx, `SELECT password_hash FROM users WHERE username='carol'`).Scan(&carolHash); err != nil {
		t.Fatalf("read carol: %v", err)
	}
	if carolHash != "" {
		t.Errorf("carol password_hash = %q, want empty", carolHash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(carolHash), []byte(testPassword)); err == nil {
		t.Error("an empty password hash verified a password — password login must be impossible for a plugin-auth user")
	}
	// The user who DID have a password is untouched by any of this.
	var aliceHash string
	if err := dest.QueryRow(ctx, `SELECT password_hash FROM users WHERE username='alice'`).Scan(&aliceHash); err != nil {
		t.Fatalf("read alice: %v", err)
	}
	if aliceHash != string(hash) {
		t.Error("alice password hash was not carried verbatim")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(aliceHash), []byte(testPassword)); err != nil {
		t.Errorf("carried bcrypt hash does not verify the original password: %v", err)
	}
}

// ── restriction flags: user.blocked and video.nsfw ──
//
// Both are columns the source uses to say "this is not ordinary", and the
// importer read NEITHER: a suspended account arrived able to sign in with the
// bcrypt hash the import carried verbatim, and every sensitive video arrived
// unflagged, leaving the instance and per-user hide/warn/blur policy nothing to
// act on (and vidra-search indexing it as clean). Failing open is the wrong
// default for both: a wrongly-carried suspension costs an operator one click,
// a wrongly-dropped one costs an incident.
func TestPeerTubeImportCarriesSuspensionAndSensitivity(t *testing.T) {
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
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The flag reaches the column...
	if readUserActive(t, ctx, dest, "bob") {
		t.Error("bob is blocked on the source but imported is_active=true — a suspended account arrived able to sign in")
	}
	if !readUserActive(t, ctx, dest, "alice") {
		t.Error("alice is not blocked on the source but imported is_active=false — the suspension leaked onto everyone")
	}
	// ...and BITES on the real login path, which is the only thing an operator
	// actually cares about. The carried password still verifies, so this proves
	// the gate and not a broken hash.
	svc := auth.NewService(sqlcgen.New(dest),
		auth.NewTokenIssuer("test-signing-secret-not-a-real-one", "vidra", "vidra", time.Hour), time.Hour)
	if _, err := svc.Login(ctx, auth.LoginInput{Identifier: "bob@example.test", Password: testPassword}, "test"); !errors.Is(err, auth.ErrAccountDisabled) {
		t.Errorf("login as the suspended account = %v, want ErrAccountDisabled", err)
	}
	if _, err := svc.Login(ctx, auth.LoginInput{Identifier: "alice@example.test", Password: testPassword}, "test"); err != nil {
		t.Errorf("login as the account that was NOT suspended failed: %v", err)
	}

	// video.nsfw → videos.is_sensitive, carried and not invented.
	if !readVideoSensitive(t, ctx, dest, "Second Video") {
		t.Error("Second Video is nsfw on the source but imported is_sensitive=false — the content policy has nothing to act on")
	}
	if readVideoSensitive(t, ctx, dest, "First Video") {
		t.Error("First Video is not nsfw on the source but imported is_sensitive=true")
	}

	// The report has to SAY so: an operator finishing a migration needs the
	// number of accounts that arrived locked out before telling anyone their
	// account is ready.
	if got := report.Entities[KindUserSuspension].Imported; got != 1 {
		t.Errorf("suspensions carried = %d, want 1", got)
	}
	if got := report.Entities[KindVideoSensitive].Imported; got != 1 {
		t.Errorf("sensitive videos carried = %d, want 1", got)
	}
}

// A source too old to carry either column must lose that ONE signal and nothing
// else — the probe degrades, it never aborts the run.
func TestPeerTubeImportRestrictionColumnsAbsent(t *testing.T) {
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
	mustExec(t, ctx, src, `ALTER TABLE "user" DROP COLUMN blocked`)
	mustExec(t, ctx, src, `ALTER TABLE "video" DROP COLUMN nsfw`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v — a source that cannot speak about suspensions or sensitivity is not a failed run", err)
	}
	// Everything else still comes across.
	if got := report.Entities[KindUser].Imported; got != 2 {
		t.Errorf("users imported = %d, want 2", got)
	}
	if got := report.Entities[KindVideo].Imported; got != 2 {
		t.Errorf("videos imported = %d, want 2", got)
	}
	// The absent column reads as PeerTube's own default for it, so nothing is
	// carried and nothing is invented.
	if !readUserActive(t, ctx, dest, "bob") || !readUserActive(t, ctx, dest, "alice") {
		t.Error("a source with no blocked column suspended somebody anyway")
	}
	if readVideoSensitive(t, ctx, dest, "Second Video") || readVideoSensitive(t, ctx, dest, "First Video") {
		t.Error("a source with no nsfw column flagged something anyway")
	}
}

func readUserActive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string) bool {
	t.Helper()
	var active bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM users WHERE username=$1`, username).Scan(&active); err != nil {
		t.Fatalf("read is_active for %s: %v", username, err)
	}
	return active
}

func readVideoSensitive(t *testing.T, ctx context.Context, pool *pgxpool.Pool, title string) bool {
	t.Helper()
	var sensitive bool
	if err := pool.QueryRow(ctx, `SELECT is_sensitive FROM videos WHERE title=$1`, title).Scan(&sensitive); err != nil {
		t.Fatalf("read is_sensitive for %s: %v", title, err)
	}
	return sensitive
}

// A moderator-removed source video must arrive REMOVED. PeerTube keeps the
// blacklist in a table of its own and touches neither video.privacy nor
// video.state, so the video the source has taken down is byte-identical to a
// live one in every column the importer used to read — it imported public and
// published, i.e. back on the public feed, the channel page, the sitemap, RSS,
// and pushed into vidra-search.
func TestPeerTubeImportCarriesTheVideoBlacklist(t *testing.T) {
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
	// The blacklisted fixture video is PRIVATE, which would hide it on the
	// destination for a reason that has nothing to do with the block. Making it
	// public is what puts the block itself on trial: video 1 is public and NOT
	// blacklisted, so it is the control that proves the pass carries a flag
	// rather than removing everything.
	mustExec(t, ctx, src, `UPDATE "video" SET privacy = 1 WHERE id = 2`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// The row exists, and it exists for the blacklisted video only.
	blockedTitles := readBlockedVideoTitles(t, ctx, dest)
	if len(blockedTitles) != 1 || blockedTitles[0] != "Second Video" {
		t.Fatalf("video_blocks rows = %v, want exactly [Second Video] — the source's blacklist did not reach the destination", blockedTitles)
	}
	// The video is still THERE — carried and blocked, not skipped. Skipping it
	// would take the operator's ability to review it away, and would let any
	// later re-run resurrect it as a fresh public import.
	var stillThere bool
	if err := dest.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM videos WHERE title='Second Video')`).Scan(&stillThere); err != nil {
		t.Fatalf("read the blacklisted video: %v", err)
	}
	if !stillThere {
		t.Error("the blacklisted video was not imported at all; it must arrive blocked, not absent")
	}

	q := sqlcgen.New(dest)
	// ...and the block BITES on all three surfaces the destination's ten
	// eligibility predicates guard: the public feed, the channel page, and the
	// search documents vidra-search indexes.
	feed, err := q.ListPublicVideosSorted(ctx, sqlcgen.ListPublicVideosSortedParams{Sort: "recent", ResultLimit: 50})
	if err != nil {
		t.Fatalf("list public videos: %v", err)
	}
	assertBlockedTitleAbsent(t, "public feed", mappedTitles(feed, func(r sqlcgen.ListPublicVideosSortedRow) string { return r.Title }))

	channelID := readChannelIDByHandle(t, ctx, dest, "alice_channel")
	page, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
		ChannelID: channelID, Sort: "recent", ResultLimit: 50,
	})
	if err != nil {
		t.Fatalf("list channel videos: %v", err)
	}
	assertBlockedTitleAbsent(t, "channel page", mappedTitles(page, func(r sqlcgen.ListPublicVideosByChannelRow) string { return r.Title }))

	docs, err := q.ListVideoSearchDocsPage(ctx, sqlcgen.ListVideoSearchDocsPageParams{After: uuid.Nil, PageSize: 50})
	if err != nil {
		t.Fatalf("list search docs: %v", err)
	}
	assertBlockedTitleAbsent(t, "vidra-search documents", mappedTitles(docs, func(r sqlcgen.ListVideoSearchDocsPageRow) string { return r.Title }))

	// The report has to SAY so: "2 videos imported" does not answer "how many
	// of them arrived taken down?", which is the number an operator checks
	// before announcing the new instance.
	if got := report.Entities[KindVideoBlock].Imported; got != 1 {
		t.Errorf("blocks carried = %d, want 1", got)
	}

	// A re-run is a no-op: exactly one block row, no duplicate, no error.
	if _, err := imp.Run(ctx, version, nil); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if n := countRows(t, ctx, dest, "video_blocks"); n != 1 {
		t.Errorf("video_blocks rows after re-run = %d, want 1", n)
	}
}

// A source too old to have the table at all must lose this ONE signal and
// nothing else — the probe degrades, it never aborts the run.
func TestPeerTubeImportVideoBlacklistTableAbsent(t *testing.T) {
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
	mustExec(t, ctx, src, `DROP TABLE "videoBlacklist"`)

	imp := NewImporter(dest, NewSourceFromPool(src), Options{Policy: PolicySkip, MediaMode: MediaModeNone})
	version, err := imp.Preflight(ctx)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	report, err := imp.Run(ctx, version, nil)
	if err != nil {
		t.Fatalf("run: %v — a source that cannot speak about its blacklist is not a failed run", err)
	}
	if got := report.Entities[KindVideo].Imported; got != 2 {
		t.Errorf("videos imported = %d, want 2", got)
	}
	if n := countRows(t, ctx, dest, "video_blocks"); n != 0 {
		t.Errorf("video_blocks rows = %d, want 0 — a source with no blacklist blocked something anyway", n)
	}
	// The operator has to be TOLD the signal is missing, or they will read the
	// zero as "nothing was taken down on the source".
	var noted bool
	for _, d := range report.Deferred {
		noted = noted || strings.Contains(d, "videoBlacklist")
	}
	if !noted {
		t.Errorf("deferred families = %v, want one naming the absent videoBlacklist table", report.Deferred)
	}
}

func readBlockedVideoTitles(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT v.title FROM video_blocks b JOIN videos v ON v.id = b.video_id ORDER BY v.title`)
	if err != nil {
		t.Fatalf("read video_blocks: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scan video_blocks: %v", err)
		}
		out = append(out, title)
	}
	return out
}

func readChannelIDByHandle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, handle string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM channels WHERE handle=$1`, handle).Scan(&id); err != nil {
		t.Fatalf("read channel %s: %v", handle, err)
	}
	return id
}

func mappedTitles[T any](rows []T, title func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, title(r))
	}
	return out
}

// assertBlockedTitleAbsent fails when the blocked video is on a surface, and
// ALSO when the control video is missing from it — a surface that returned
// nothing proves the predicate works no more than an empty table would.
func assertBlockedTitleAbsent(t *testing.T, surface string, titles []string) {
	t.Helper()
	var sawControl bool
	for _, got := range titles {
		if got == "Second Video" {
			t.Errorf("%s still lists the blacklisted video (titles=%v)", surface, titles)
		}
		if got == "First Video" {
			sawControl = true
		}
	}
	if !sawControl {
		t.Errorf("%s does not list the video that is NOT blacklisted (titles=%v) — the surface is empty, so it proves nothing", surface, titles)
	}
}

// TestPeerTubeImportMergeLinkedRowsAreNotResynced pins the boundary between the
// two orthogonal options a real cutover combines. --conflict-policy merge maps a
// source entity onto a row that was ALREADY on this instance (that is what it is
// for: the source's children need something to hang off). --source-authoritative
// lets a re-run rewrite the rows the import OWNS. A row the import merely linked
// to is not one it owns, so the resync must leave it exactly as this instance
// made it — while still carrying the source onto the rows the import really did
// create, which is the whole point of the mode and is the control here.
func TestPeerTubeImportMergeLinkedRowsAreNotResynced(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	sourceHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	srcPool, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, srcPool, string(sourceHash), secretPrivKeyAlice)

	// ── what is on this instance before the operator ever runs the tool ──
	//
	// The owner's own account, which happens to share a username with a source
	// account, and a channel made here by a different local creator whose handle
	// happens to match a source channel's. Both are exactly what merge points the
	// source's children at, and neither came from the source.
	ownerHash, err := bcrypt.GenerateFromPassword([]byte("the-owners-own-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var ownerID, carolID, localChanID uuid.UUID
	if err := dest.QueryRow(ctx,
		`INSERT INTO users (username,email,password_hash,role) VALUES ('alice','owner@example.test',$1,'admin') RETURNING id`,
		string(ownerHash)).Scan(&ownerID); err != nil {
		t.Fatalf("seed pre-existing owner: %v", err)
	}
	if err := dest.QueryRow(ctx,
		`INSERT INTO users (username,email,password_hash,role) VALUES ('carol','carol@example.test','x','user') RETURNING id`).Scan(&carolID); err != nil {
		t.Fatalf("seed local creator: %v", err)
	}
	if err := dest.QueryRow(ctx,
		`INSERT INTO channels (owner_id,handle,display_name,description) VALUES ($1,'alice_channel','Carol Channel','made here') RETURNING id`,
		carolID).Scan(&localChanID); err != nil {
		t.Fatalf("seed local channel: %v", err)
	}

	runMerge := func(sourceAuthoritative bool) {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(srcPool), Options{
			Policy: PolicyMerge, MediaMode: MediaModeNone, SourceAuthoritative: sourceAuthoritative,
		})
		v, err := imp.Preflight(ctx)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		if _, err := imp.Run(ctx, v, nil); err != nil {
			t.Fatalf("run (source_authoritative=%v): %v", sourceAuthoritative, err)
		}
	}
	runMerge(false)

	// bob collided with nothing, so the import CREATED his account and his
	// channel. Reading their ids here is also the fixture check: if the import
	// had done nothing at all these lookups fail, so the "unchanged" assertions
	// below cannot pass merely because no run happened.
	var bobID, bobChanID uuid.UUID
	if err := dest.QueryRow(ctx, `SELECT id FROM users WHERE username='bob'`).Scan(&bobID); err != nil {
		t.Fatalf("read imported bob: %v", err)
	}
	if err := dest.QueryRow(ctx, `SELECT id FROM channels WHERE handle='bob_channel'`).Scan(&bobChanID); err != nil {
		t.Fatalf("read imported bob_channel: %v", err)
	}

	// The ledger states the provenance ONCE, at creation.
	for _, tc := range []struct {
		kind, sourceID string
		want           bool
	}{
		{KindUser, "1", false},    // merged onto the owner's pre-existing account
		{KindUser, "2", true},     // bob: inserted by the import
		{KindChannel, "1", false}, // merged onto the local creator's channel
		{KindChannel, "2", true},  // bob_channel: inserted by the import
	} {
		var got bool
		if err := dest.QueryRow(ctx,
			`SELECT created_by_import FROM peertube_import_ledger WHERE entity_kind=$1 AND source_id=$2`,
			tc.kind, tc.sourceID).Scan(&got); err != nil {
			t.Fatalf("ledger %s/%s: %v", tc.kind, tc.sourceID, err)
		}
		if got != tc.want {
			t.Errorf("ledger %s/%s created_by_import = %v, want %v", tc.kind, tc.sourceID, got, tc.want)
		}
	}

	// ── the source moves on, and the operator runs the cutover sync ──
	newHash, err := bcrypt.GenerateFromPassword([]byte("changed-on-the-source"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, srcPool, `UPDATE "user" SET password=$1 WHERE id IN (1,2)`, string(newHash))
	mustExec(t, ctx, srcPool, `UPDATE "account" SET name='Alice Renamed' WHERE id=1`)
	mustExec(t, ctx, srcPool, `UPDATE "videoChannel" SET name='Alice Channel (renamed)' WHERE id=1`)
	mustExec(t, ctx, srcPool, `UPDATE "videoChannel" SET name='Bob Channel (renamed)' WHERE id=2`)
	runMerge(true)

	// The rows the import only LINKED to belong to this instance, and are left
	// exactly as this instance made them.
	var gotHash, gotRole string
	if err := dest.QueryRow(ctx, `SELECT password_hash, role FROM users WHERE id=$1`, ownerID).Scan(&gotHash, &gotRole); err != nil {
		t.Fatalf("read the pre-existing account after the resync: %v", err)
	}
	if gotHash != string(ownerHash) {
		t.Errorf("the resync rewrote the password_hash of an account the import did not create")
	}
	if gotRole != "admin" {
		t.Errorf("pre-existing account role = %q, want %q — the resync must not re-grade an account it did not create", gotRole, "admin")
	}
	var gotOwner uuid.UUID
	var gotName string
	if err := dest.QueryRow(ctx, `SELECT owner_id, display_name FROM channels WHERE id=$1`, localChanID).Scan(&gotOwner, &gotName); err != nil {
		t.Fatalf("read the local channel after the resync: %v", err)
	}
	if gotOwner != carolID {
		t.Errorf("the local channel was reassigned to %v, want its own owner %v", gotOwner, carolID)
	}
	if gotName != "Carol Channel" {
		t.Errorf("local channel display_name = %q, want %q", gotName, "Carol Channel")
	}

	// …and the mode still does what it exists to do on the rows the import DID
	// create. Without these two the guard could be "satisfied" by an over-broad
	// predicate that turns --source-authoritative into a no-op.
	if err := dest.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, bobID).Scan(&gotHash); err != nil {
		t.Fatalf("read the imported account after the resync: %v", err)
	}
	if gotHash != string(newHash) {
		t.Errorf("the imported account's password_hash was NOT resynced — --source-authoritative has become a no-op")
	}
	if err := dest.QueryRow(ctx, `SELECT display_name FROM channels WHERE id=$1`, bobChanID).Scan(&gotName); err != nil {
		t.Fatalf("read the imported channel after the resync: %v", err)
	}
	if gotName != "Bob Channel (renamed)" {
		t.Errorf("imported channel display_name = %q, want the source's new name", gotName)
	}
}

// A migrated creator arrives with a back-catalogue already stored and a quota
// nobody chose for them. Usage is not a counter — it is SUM(video_files.size_bytes)
// over the rows this importer just wrote with the source's real byte counts — so
// on the shipped INSTANCE_DEFAULT_QUOTA_BYTES=5 GiB every creator whose catalogue
// is bigger than that is over quota the instant the import commits, and their
// first upload after being told "we've moved" is a 422 quota_exceeded. Reference
// mode makes it sharper: the bytes are the SOURCE's, never copied here, and they
// still charge the creator's quota.
//
// The import therefore states storage_quota_bytes = 0 (unlimited for that
// account) ONCE, at creation. It says it only about accounts it CREATED — a
// pre-existing local account that --conflict-policy merge pointed the source's
// children at is not an imported account — and it never re-asserts it, so a
// quota an operator sets afterwards survives every cutover run.
func TestPeerTubeImportGivesImportedCreatorsUnlimitedQuota(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	sourceHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	srcPool, _ := newScratchDB(t, ctx, base)
	dest, _ := newScratchDB(t, ctx, base)
	applyMigrations(t, ctx, dest)
	seedPeerTube(t, ctx, srcPool, string(sourceHash), secretPrivKeyAlice)

	// ── what stands on this instance before the tool is ever run ──
	//
	// alice: the owner's own account, which happens to share a username with a
	// source account, so merge links onto it. carol: a local creator the import
	// never sees at all — the CONTROL row. Both are on the instance default.
	var localAliceID, carolID uuid.UUID
	if err := dest.QueryRow(ctx,
		`INSERT INTO users (username,email,password_hash,role) VALUES ('alice','owner@example.test','x','admin') RETURNING id`).
		Scan(&localAliceID); err != nil {
		t.Fatalf("seed pre-existing owner: %v", err)
	}
	if err := dest.QueryRow(ctx,
		`INSERT INTO users (username,email,password_hash,role) VALUES ('carol','carol@example.test','x','user') RETURNING id`).
		Scan(&carolID); err != nil {
		t.Fatalf("seed local control creator: %v", err)
	}

	runMerge := func(sourceAuthoritative bool) *Report {
		t.Helper()
		imp := NewImporter(dest, NewSourceFromPool(srcPool), Options{
			Policy: PolicyMerge, MediaMode: MediaModeNone, SourceAuthoritative: sourceAuthoritative,
		})
		v, err := imp.Preflight(ctx)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		rep, err := imp.Run(ctx, v, nil)
		if err != nil {
			t.Fatalf("run (source_authoritative=%v): %v", sourceAuthoritative, err)
		}
		return rep
	}
	report := runMerge(false)

	// Fixture integrity: bob's row has to EXIST and have come from this run,
	// otherwise every "quota" assertion below could pass on an empty database.
	bobID := readUserID(t, ctx, dest, "bob")
	if got := report.Entities[KindUser].Imported; got != 1 {
		t.Fatalf("users imported = %d, want 1 (bob; alice merged onto the local account)", got)
	}

	// The account the import CREATED is unlimited...
	if q := readUserQuota(t, ctx, dest, "bob"); q == nil || *q != 0 {
		t.Errorf("imported bob storage_quota_bytes = %v, want 0 (unlimited) — every migrated creator inherits the instance default and is over it", quotaStr(q))
	}
	// ...and the two accounts the import did not create are untouched. carol
	// proves the write is targeted and not a global default change; the
	// merged-onto alice proves "imported" means created here, not linked to.
	if q := readUserQuota(t, ctx, dest, "alice"); q != nil {
		t.Errorf("merge-linked pre-existing alice storage_quota_bytes = %s, want NULL — the import handed unlimited storage to an account it did not create", quotaStr(q))
	}
	if q := readUserQuota(t, ctx, dest, "carol"); q != nil {
		t.Errorf("local control carol storage_quota_bytes = %s, want NULL — an account the import never saw was rewritten", quotaStr(q))
	}

	// It BITES on the gate an upload actually runs (checkUploadQuotas →
	// quota.CheckFits → 422 quota_exceeded), against the SHIPPED default.
	const shippedDefault = int64(5 * 1024 * 1024 * 1024) // INSTANCE_DEFAULT_QUOTA_BYTES in the meta template
	const overTheDefault = shippedDefault + 1
	qsvc := quota.NewService(sqlcgen.New(dest), shippedDefault)
	if err := qsvc.CheckFits(ctx, bobID, overTheDefault); err != nil {
		t.Errorf("upload of %d bytes by the imported creator = %v, want it to fit — this is the 422 on their first upload after cutover", overTheDefault, err)
	}
	for name, id := range map[string]uuid.UUID{"merge-linked alice": localAliceID, "local carol": carolID} {
		if err := qsvc.CheckFits(ctx, id, overTheDefault); !errors.Is(err, quota.ErrExceeded) {
			t.Errorf("upload of %d bytes by %s = %v, want quota.ErrExceeded — the instance default stopped applying to accounts the import did not create", overTheDefault, name, err)
		}
	}

	// The report says it happened: an operator reading the run needs to see that
	// the tool decided their quota policy for them.
	if got := report.Entities[KindUserQuotaUnlimited].Imported; got != 1 {
		t.Errorf("unlimited quotas granted = %d, want 1", got)
	}

	// ── the operator then makes a deliberate decision, and cuts over ──
	const operatorQuota = int64(4096)
	mustExec(t, ctx, dest, `UPDATE users SET storage_quota_bytes=$1 WHERE id=$2`, operatorQuota, bobID)
	newHash, err := bcrypt.GenerateFromPassword([]byte("changed-on-the-source"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, srcPool, `UPDATE "user" SET password=$1 WHERE id=2`, string(newHash))
	runMerge(true)

	// Fixture integrity again: --source-authoritative has to have DONE something
	// to bob's row, or "the quota survived" would be a statement about a no-op.
	var gotHash string
	if err := dest.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, bobID).Scan(&gotHash); err != nil {
		t.Fatalf("read the imported account after the resync: %v", err)
	}
	if gotHash != string(newHash) {
		t.Fatalf("the resync did not update the imported account — the quota assertion below would prove nothing")
	}
	if q := readUserQuota(t, ctx, dest, "bob"); q == nil || *q != operatorQuota {
		t.Errorf("bob storage_quota_bytes after the resync = %s, want %d — a resync wiped a quota the operator set on a migrated creator", quotaStr(q), operatorQuota)
	}
	if q := readUserQuota(t, ctx, dest, "alice"); q != nil {
		t.Errorf("merge-linked alice storage_quota_bytes after the resync = %s, want NULL", quotaStr(q))
	}
	if q := readUserQuota(t, ctx, dest, "carol"); q != nil {
		t.Errorf("local control carol storage_quota_bytes after the resync = %s, want NULL", quotaStr(q))
	}
}

func readUserID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username=$1`, username).Scan(&id); err != nil {
		t.Fatalf("read user id for %s: %v", username, err)
	}
	return id
}

func readUserQuota(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string) *int64 {
	t.Helper()
	var q *int64
	if err := pool.QueryRow(ctx, `SELECT storage_quota_bytes FROM users WHERE username=$1`, username).Scan(&q); err != nil {
		t.Fatalf("read storage_quota_bytes for %s: %v", username, err)
	}
	return q
}

func quotaStr(q *int64) string {
	if q == nil {
		return "NULL"
	}
	return strconv.FormatInt(*q, 10)
}
