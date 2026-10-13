//go:build integration

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/watchword"
)

// TestMigration0172WatchedWordListsPreservesAndRestores: the flat instance list
// (and the matches that cite it) moves into a default instance list on up, and
// down restores the flat table — per-account terms dropped, instance duplicates
// merged — without losing a single flat term or match.
func TestMigration0172WatchedWordListsPreservesAndRestores(t *testing.T) {
	base := os.Getenv("DATABASE_URL")
	if base == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	rnd := make([]byte, 6)
	_, _ = rand.Read(rnd)
	tempDB := "vidra_migtest_wwl_" + hex.EncodeToString(rnd)
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{tempDB}.Sanitize()); err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{tempDB}.Sanitize()+` WITH (FORCE)`)
	}()
	u, _ := url.Parse(base)
	u.Path = "/" + tempDB
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect temp db: %v", err)
	}
	defer conn.Close(ctx)

	// Everything BEFORE 0172: the flat world.
	ups := loadUpMigrations(t)
	for _, m := range ups {
		if m.version < 172 {
			applyMigration(ctx, t, conn, m)
		}
	}
	var userID, wSpam, wScam uuid.UUID
	var chanID, videoID, commentID uuid.UUID
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(conn.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ('wwl','wwl@example.test','x') RETURNING id`).Scan(&userID), "user")
	must(conn.QueryRow(ctx, `INSERT INTO watched_words (word, created_by) VALUES ('Spam',$1) RETURNING id`, userID).Scan(&wSpam), "word")
	must(conn.QueryRow(ctx, `INSERT INTO watched_words (word, created_by) VALUES ('scam',$1) RETURNING id`, userID).Scan(&wScam), "word")
	must(conn.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1,'wwl-chan','W') RETURNING id`, userID).Scan(&chanID), "channel")
	must(conn.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1,'v','public','published') RETURNING id`, chanID).Scan(&videoID), "video")
	must(conn.QueryRow(ctx, `INSERT INTO comments (video_id, user_id, body) VALUES ($1,$2,'a spam') RETURNING id`, videoID, userID).Scan(&commentID), "comment")
	_, err = conn.Exec(ctx, `INSERT INTO watched_word_matches (watched_word_id, comment_id, matched_text, matched_term) VALUES ($1,$2,'a spam','Spam')`, wSpam, commentID)
	must(err, "match")

	for _, m := range ups {
		if m.version == 172 {
			applyMigration(ctx, t, conn, m)
		}
	}

	// UP: both terms are in the one default INSTANCE list; the match names it.
	var n int
	must(conn.QueryRow(ctx, `SELECT count(*) FROM watched_words w JOIN watched_word_lists l ON l.id = w.list_id
		WHERE l.is_default AND l.owner_id IS NULL AND l.action = 'flag' AND w.id = ANY($1)`, []uuid.UUID{wSpam, wScam}).Scan(&n), "count")
	if n != 2 {
		t.Fatalf("terms in default list = %d, want 2", n)
	}
	var listName string
	must(conn.QueryRow(ctx, `SELECT list_name FROM watched_word_matches WHERE watched_word_id = $1`, wSpam).Scan(&listName), "match list")
	if listName != "Default" {
		t.Errorf("backfilled match list_name = %q, want Default", listName)
	}
	// An INSERT naming no list (the previous release) still lands in the default list.
	var legacyList uuid.UUID
	must(conn.QueryRow(ctx, `INSERT INTO watched_words (word) VALUES ('legacy') RETURNING list_id`).Scan(&legacyList), "legacy insert")
	must(conn.QueryRow(ctx, `SELECT count(*) FROM watched_word_lists WHERE id = $1 AND is_default`, legacyList).Scan(&n), "legacy list")
	if n != 1 {
		t.Errorf("a list-less insert did not land in the default list")
	}

	// A per-account list and an overlapping instance list, then DOWN.
	var ownList, otherInst uuid.UUID
	must(conn.QueryRow(ctx, `INSERT INTO watched_word_lists (owner_id, name) VALUES ($1,'mine') RETURNING id`, userID).Scan(&ownList), "own list")
	must(conn.QueryRow(ctx, `INSERT INTO watched_word_lists (name) VALUES ('other') RETURNING id`).Scan(&otherInst), "instance list")
	_, err = conn.Exec(ctx, `INSERT INTO watched_words (list_id, word) VALUES ($1,'private-term'), ($2,'SCAM'), ($2,'extra')`, ownList, otherInst)
	must(err, "seed more terms")

	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0172_watched_word_lists.down.sql"))
	must(err, "read down")
	if _, err := conn.Exec(ctx, string(down)); err != nil {
		t.Fatalf("0172 down: %v", err)
	}
	rows, err := conn.Query(ctx, `SELECT lower(word) FROM watched_words ORDER BY 1`)
	must(err, "flat words")
	var got []string
	for rows.Next() {
		var w string
		must(rows.Scan(&w), "scan")
		got = append(got, w)
	}
	rows.Close()
	want := []string{"extra", "legacy", "scam", "spam"} // private-term dropped, scam merged
	if len(got) != len(want) {
		t.Fatalf("flat terms after down = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flat terms after down = %v, want %v", got, want)
		}
	}
	must(conn.QueryRow(ctx, `SELECT count(*) FROM watched_word_matches WHERE watched_word_id = $1 AND matched_term = 'Spam'`, wSpam).Scan(&n), "match survives")
	if n != 1 {
		t.Errorf("the flagged match did not survive down")
	}
	// The flat uniqueness is back.
	if _, err := conn.Exec(ctx, `INSERT INTO watched_words (word) VALUES ('SPAM')`); err == nil {
		t.Errorf("duplicate term accepted after down; flat unique index missing")
	}
}

// TestWatchedWordListsScopingAndHoldOnRealPG: per-account lists are owner-only,
// apply only to comments on the owner's videos, record the list that matched,
// and drive ShouldHold from the list's action.
func TestWatchedWordListsScopingAndHoldOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	svc := watchword.NewService(st.Queries())

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1,$2,'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	alice, bob, mod := mkUser("wl-alice"), mkUser("wl-bob"), mkUser("wl-mod")
	mkVideo := func(owner uuid.UUID, name string) uuid.UUID {
		t.Helper()
		var ch, v uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1,$2,'C') RETURNING id`, owner, name+"-"+suffix).Scan(&ch); err != nil {
			t.Fatalf("seed channel: %v", err)
		}
		if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1,'v','public','published') RETURNING id`, ch).Scan(&v); err != nil {
			t.Fatalf("seed video: %v", err)
		}
		return v
	}
	aliceVideo, bobVideo := mkVideo(alice, "wl-a"), mkVideo(bob, "wl-b")
	mkComment := func(video, author uuid.UUID, body string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO comments (video_id, user_id, body) VALUES ($1,$2,$3) RETURNING id`, video, author, body).Scan(&id); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
		return id
	}

	hold := "hold-" + suffix
	flagTerm := "flag-" + suffix
	// Alice's own hold list; the moderator's instance flag list.
	aliceList, err := svc.CreateList(ctx, alice, alice, "Alice's list", "hold", []string{hold})
	if err != nil {
		t.Fatalf("create account list: %v", err)
	}
	instList, err := svc.CreateList(ctx, uuid.Nil, mod, "Instance "+suffix, "flag", []string{flagTerm})
	if err != nil {
		t.Fatalf("create instance list: %v", err)
	}
	t.Cleanup(func() { _ = svc.DeleteList(context.Background(), uuid.Nil, instList.ID) })

	// Owner-only: bob and the instance scope cannot see, change or delete alice's list.
	if _, err := svc.GetList(ctx, bob, aliceList.ID); !errors.Is(err, watchword.ErrListNotFound) {
		t.Errorf("bob reads alice's list: %v, want not found", err)
	}
	if _, err := svc.GetList(ctx, uuid.Nil, aliceList.ID); !errors.Is(err, watchword.ErrListNotFound) {
		t.Errorf("instance scope reads alice's list: %v, want not found", err)
	}
	newName := "stolen"
	if _, err := svc.UpdateList(ctx, bob, bob, aliceList.ID, &newName, nil, nil, false); !errors.Is(err, watchword.ErrListNotFound) {
		t.Errorf("bob updates alice's list: %v, want not found", err)
	}
	if err := svc.DeleteList(ctx, bob, aliceList.ID); !errors.Is(err, watchword.ErrListNotFound) {
		t.Errorf("bob deletes alice's list: %v, want not found", err)
	}
	if _, err := svc.GetList(ctx, alice, instList.ID); !errors.Is(err, watchword.ErrListNotFound) {
		t.Errorf("alice reads an instance list through her scope: %v, want not found", err)
	}
	if lists, total, _ := svc.ListLists(ctx, bob, 20, 0); total != 0 || len(lists) != 0 {
		t.Errorf("bob lists %d/%d lists, want none of alice's", len(lists), total)
	}

	// Alice's list holds comments on HER video only.
	if h, err := svc.ShouldHold(ctx, aliceVideo, "so "+hold+" here"); err != nil || !h {
		t.Errorf("ShouldHold on alice's video = %v, %v; want true", h, err)
	}
	if h, _ := svc.ShouldHold(ctx, bobVideo, "so "+hold+" here"); h {
		t.Errorf("alice's list held a comment on bob's video")
	}
	// A flag-action instance list never holds, but flags on every video.
	if h, _ := svc.ShouldHold(ctx, bobVideo, flagTerm); h {
		t.Errorf("a flag list held a comment")
	}

	// FlagComment records the list that matched, per-account list included.
	onAlice := mkComment(aliceVideo, bob, hold+" and "+flagTerm)
	if n, err := svc.FlagComment(ctx, onAlice, hold+" and "+flagTerm); err != nil || n != 2 {
		t.Fatalf("FlagComment on alice's video = %d, %v; want 2", n, err)
	}
	onBob := mkComment(bobVideo, alice, hold+" and "+flagTerm)
	if n, err := svc.FlagComment(ctx, onBob, hold+" and "+flagTerm); err != nil || n != 1 {
		t.Fatalf("FlagComment on bob's video = %d, %v; want 1 (alice's list must not apply)", n, err)
	}
	rows, err := st.Pool.Query(ctx, `SELECT comment_id, list_name FROM watched_word_matches WHERE comment_id = ANY($1)`, []uuid.UUID{onAlice, onBob})
	if err != nil {
		t.Fatal(err)
	}
	lists := map[uuid.UUID]map[string]bool{onAlice: {}, onBob: {}}
	for rows.Next() {
		var c uuid.UUID
		var name string
		_ = rows.Scan(&c, &name)
		lists[c][name] = true
	}
	rows.Close()
	if !lists[onAlice]["Alice's list"] || !lists[onAlice]["Instance "+suffix] || len(lists[onBob]) != 1 || !lists[onBob]["Instance "+suffix] {
		t.Errorf("recorded lists = %v", lists)
	}

	// Default list: undeletable, words not replaceable; names unique per scope.
	def, _, _ := svc.ListLists(ctx, uuid.Nil, 1, 0)
	if len(def) == 0 || !def[0].IsDefault {
		t.Fatalf("default list is not first: %+v", def)
	}
	if err := svc.DeleteList(ctx, uuid.Nil, def[0].ID); !errors.Is(err, watchword.ErrDefaultList) {
		t.Errorf("delete default = %v, want ErrDefaultList", err)
	}
	if _, err := svc.UpdateList(ctx, uuid.Nil, mod, def[0].ID, nil, nil, []string{"x"}, true); !errors.Is(err, watchword.ErrDefaultList) {
		t.Errorf("replace default words = %v, want ErrDefaultList", err)
	}
	if _, err := svc.CreateList(ctx, alice, alice, "ALICE'S LIST", "flag", nil); !errors.Is(err, watchword.ErrListNameTaken) {
		t.Errorf("duplicate name = %v, want ErrListNameTaken", err)
	}
	if _, err := svc.CreateList(ctx, bob, bob, "Alice's list", "flag", nil); err != nil {
		t.Errorf("the same name in another account's scope must be allowed: %v", err)
	}

	// Replace semantics: kept terms stay, absent ones go, new ones arrive.
	w2 := []string{hold, "extra-" + suffix}
	got, err := svc.UpdateList(ctx, alice, alice, aliceList.ID, nil, nil, w2, true)
	if err != nil || len(got.Words) != 2 {
		t.Fatalf("replace words = %+v, %v", got, err)
	}
	// Deleting the list takes its terms; the matches keep their snapshot.
	if err := svc.DeleteList(ctx, alice, aliceList.ID); err != nil {
		t.Fatalf("delete own list: %v", err)
	}
	var kept int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM watched_word_matches WHERE comment_id = $1 AND list_name = 'Alice''s list' AND list_id IS NULL`, onAlice).Scan(&kept)
	if kept != 1 {
		t.Errorf("match after list delete: kept=%d, want 1 with the list name snapshot", kept)
	}
}
