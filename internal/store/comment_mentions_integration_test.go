//go:build integration

package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestNotifyCommentMentionsOnRealPG holds every rule of the comment_mention
// fan-out, which lives in the statement rather than in Go. Each exclusion is
// applied to a recipient who WOULD otherwise be notified and the EXACT set of
// notified users is asserted, so over-delivery (a muted/blocked/opted-out/
// already-notified user) and under-delivery both fail.
func TestNotifyCommentMentionsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) (uuid.UUID, string) {
		t.Helper()
		uname := name + "-" + suffix
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			uname, uname+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id, uname
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	owner, _ := mkUser("m-owner")
	author, _ := mkUser("m-author")
	ada, adaName := mkUser("m-ada")
	bob, bobName := mkUser("m-bob")
	var channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'M') RETURNING id`, owner, "m-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'V', 'public', 'published') RETURNING id`, channelID).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	mkComment := func(userID *uuid.UUID, body string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, $3) RETURNING id`, videoID, userID, body).Scan(&id); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
		return id
	}
	// fan runs the fan-out and returns who now holds a comment_mention for it.
	fan := func(commentID uuid.UUID, names ...string) map[uuid.UUID]bool {
		t.Helper()
		if _, err := q.NotifyCommentMentions(ctx, sqlcgen.NotifyCommentMentionsParams{CommentID: commentID, Usernames: names}); err != nil {
			t.Fatalf("NotifyCommentMentions: %v", err)
		}
		rows, err := st.Pool.Query(ctx, `SELECT user_id FROM notifications WHERE comment_id = $1 AND type = 'comment_mention'`, commentID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			_ = rows.Scan(&id)
			got[id] = true
		}
		return got
	}
	expect := func(why string, got map[uuid.UUID]bool, want ...uuid.UUID) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: notified %v, want %v", why, got, want)
		}
		for _, w := range want {
			if !got[w] {
				t.Fatalf("%s: notified %v, want %v", why, got, want)
			}
		}
	}

	// Base: both named users are notified; case is irrelevant; the author
	// mentioning themself and an unknown name notify nobody extra.
	c1 := mkComment(&author, "hi")
	expect("base", fan(c1, adaName, bobName, "nobody-"+suffix), ada, bob)
	// Idempotent: running again (an edit, a retry) adds nothing.
	expect("repeat", fan(c1, adaName, bobName), ada, bob)
	var n int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE comment_id = $1`, c1).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows after repeat = %d (%v), want 2", n, err)
	}
	// Edit adds a mention: only the NEW one is created.
	carol, carolName := mkUser("m-carol")
	expect("edit adds carol", fan(c1, adaName, bobName, carolName), ada, bob, carol)

	// Self-mention is skipped.
	var authorUsername string
	_ = st.Pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, author).Scan(&authorUsername)
	expect("self", fan(mkComment(&author, "me"), authorUsername))

	// Each exclusion, on a fresh comment so rows from earlier cases don't mask it.
	cases := []struct {
		why, apply, undo string
		args             []any
		want             []uuid.UUID
	}{
		{"recipient muted the author", `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`, []any{ada, author}, []uuid.UUID{bob}},
		{"recipient blocked the author", `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`, []any{ada, author}, []uuid.UUID{bob}},
		{"author blocked the recipient", `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`, []any{author, ada}, []uuid.UUID{bob}},
		{"recipient turned the type off", `INSERT INTO notification_prefs (user_id, type, enabled) VALUES ($1, 'comment_mention', false)`, `DELETE FROM notification_prefs WHERE user_id = $1`, []any{ada}, []uuid.UUID{bob}},
		{"recipient deactivated", `UPDATE users SET is_active = false WHERE id = $1`, `UPDATE users SET is_active = true WHERE id = $1`, []any{ada}, []uuid.UUID{bob}},
		{"recipient deleted", `UPDATE users SET deleted_at = now() WHERE id = $1`, `UPDATE users SET deleted_at = NULL WHERE id = $1`, []any{ada}, []uuid.UUID{bob}},
	}
	for _, tc := range cases {
		exec(tc.apply, tc.args...)
		expect(tc.why, fan(mkComment(&author, "x"), adaName, bobName), tc.want...)
		exec(tc.undo, tc.args...)
		expect(tc.why+" (lifted)", fan(mkComment(&author, "y"), adaName, bobName), ada, bob)
	}

	// Already told about this comment as the video owner or the parent's author:
	// no second notification for the same comment.
	c2 := mkComment(&author, "reply-ish")
	exec(`INSERT INTO notifications (user_id, type, actor_id, video_id, comment_id) VALUES ($1, 'comment_reply', $2, $3, $4)`, ada, author, videoID, c2)
	exec(`INSERT INTO notifications (user_id, type, actor_id, video_id, comment_id) VALUES ($1, 'comment', $2, $3, $4)`, bob, author, videoID, c2)
	expect("already notified", fan(c2, adaName, bobName))

	// A tombstoned comment notifies nobody.
	c3 := mkComment(&author, "gone")
	exec(`UPDATE comments SET deleted_at = now() WHERE id = $1`, c3)
	expect("tombstone", fan(c3, adaName))

	// Private / unpublished video: no leak.
	exec(`UPDATE videos SET privacy = 'private' WHERE id = $1`, videoID)
	expect("private video", fan(mkComment(&author, "p"), adaName))
	exec(`UPDATE videos SET privacy = 'public' WHERE id = $1`, videoID)
	// A moderator-blocked video: no notification either.
	exec(`INSERT INTO video_blocks (video_id, blocked_by) VALUES ($1, $2)`, videoID, owner)
	expect("blocked video", fan(mkComment(&author, "b"), adaName))
	exec(`DELETE FROM video_blocks WHERE video_id = $1`, videoID)

	// A federated comment (NULL user_id) notifies, with no actor.
	var remote uuid.UUID
	actorURL := "https://far.test/accounts/zed-" + suffix
	exec(`INSERT INTO remote_actors (actor_url, actor_type, preferred_username, domain, public_key_pem) VALUES ($1, 'Person', 'zed', 'far.test', 'pem')`, actorURL)
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, actorURL)
	})
	mkRemote := func() uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO comments (video_id, body, remote_actor_url, remote_author_name, remote_object_url)
			 VALUES ($1, 'from afar', $2, 'zed', $3) RETURNING id`,
			videoID, actorURL, "https://far.test/comments/"+uuid.NewString()).Scan(&id); err != nil {
			t.Fatalf("seed federated comment: %v", err)
		}
		return id
	}
	remote = mkRemote()
	expect("federated", fan(remote, adaName), ada)
	var actor *uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT actor_id FROM notifications WHERE comment_id = $1`, remote).Scan(&actor); err != nil || actor != nil {
		t.Fatalf("federated mention actor = %v (%v), want NULL", actor, err)
	}

	// A federated comment has no local author, so mute/block cannot gate it; the
	// remote-actor blocks and instance mutes/blocks that hide it in the comment
	// list must gate its notification too.
	remoteCases := []struct {
		why, apply, undo string
		args             []any
		want             []uuid.UUID
	}{
		{"recipient blocked the remote actor", `INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url) VALUES ($1, $2)`, `DELETE FROM remote_actor_blocks WHERE blocker_id = $1 AND remote_actor_url = $2`, []any{ada, actorURL}, []uuid.UUID{bob}},
		{"admin blocked the remote actor instance-wide", `INSERT INTO blocked_remote_actors (remote_actor_url) VALUES ($1)`, `DELETE FROM blocked_remote_actors WHERE remote_actor_url = $1`, []any{actorURL}, nil},
		{"recipient muted the remote instance", `INSERT INTO muted_instances (muter_id, domain) VALUES ($1, 'far.test')`, `DELETE FROM muted_instances WHERE muter_id = $1`, []any{ada}, []uuid.UUID{bob}},
		{"admin blocked the remote instance", `INSERT INTO blocked_instances (domain) VALUES ('far.test')`, `DELETE FROM blocked_instances WHERE domain = 'far.test'`, nil, nil},
	}
	for _, tc := range remoteCases {
		exec(tc.apply, tc.args...)
		expect(tc.why, fan(mkRemote(), adaName, bobName), tc.want...)
		exec(tc.undo, tc.args...)
		expect(tc.why+" (lifted)", fan(mkRemote(), adaName, bobName), ada, bob)
	}

	// An unpublished (e.g. scheduled) video must not be pinged about.
	for _, state := range []string{"scheduled", "draft"} {
		exec(`UPDATE videos SET state = $2 WHERE id = $1`, videoID, state)
		expect("video "+state, fan(mkComment(&author, "s"), adaName))
	}
	exec(`UPDATE videos SET state = 'published' WHERE id = $1`, videoID)

	// The cap is TOTAL per comment: later edits cannot fan out fresh batches.
	capComment := mkComment(&author, "many")
	var names []string
	for i := 0; i < 13; i++ {
		_, name := mkUser(fmt.Sprintf("m-cap%02d", i))
		names = append(names, name)
	}
	if got := fan(capComment, names[:8]...); len(got) != 8 {
		t.Fatalf("first batch notified %d, want 8", len(got))
	}
	if got := fan(capComment, names...); len(got) != 10 {
		t.Fatalf("after the edit %d users hold a mention, want the total capped at 10", len(got))
	}
	if got := fan(capComment, names...); len(got) != 10 {
		t.Fatalf("a further edit grew it to %d", len(got))
	}

	// Concurrent fan-outs for one comment (different name sets) must still
	// respect the cap: the per-comment lock makes each see the other's rows.
	raceComment := mkComment(&author, "race")
	var raceNames []string
	for i := 0; i < 24; i++ {
		_, name := mkUser(fmt.Sprintf("m-race%02d", i))
		raceNames = append(raceNames, name)
	}
	repo := st.NotificationRepository()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if _, err := repo.NotifyCommentMentions(ctx, sqlcgen.NotifyCommentMentionsParams{CommentID: raceComment, Usernames: raceNames[g*3 : g*3+3]}); err != nil {
				t.Errorf("concurrent fan-out: %v", err)
			}
		}(g)
	}
	wg.Wait()
	var total int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE comment_id = $1 AND type = 'comment_mention'`, raceComment).Scan(&total); err != nil || total != 10 {
		t.Fatalf("concurrent fan-outs left %d mention rows (%v), want exactly the cap of 10", total, err)
	}
}
