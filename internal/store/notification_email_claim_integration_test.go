//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestClaimDueNotificationEmailsOnRealPG proves the outbox claim: the lease
// (SKIP LOCKED, bounded, attempts counted), the rendered fields, and every
// `deliverable` rule re-asked at send time. It asserts the verdict for each
// recipient exactly — mailing an unverified, deactivated or muting user is the
// failure that matters, and only a live schema holds the rule.
func TestClaimDueNotificationEmailsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	suffix := uuid.NewString()[:8]
	mkUser := func(name string, verified bool) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, email_verified, display_name)
			 VALUES ($1, $2, 'x', $3, $4) RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test", verified, "",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}

	owner := mkUser("claim-owner", true)
	actor := mkUser("claim-actor", true)
	exec(`UPDATE users SET display_name = 'Bob B.' WHERE id = $1`, actor)
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Claim Lab') RETURNING id`,
		owner, "claim-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	mkVideo := func(privacy string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Soldering 101', $2, 'published') RETURNING id`,
			channelID, privacy,
		).Scan(&id); err != nil {
			t.Fatalf("seed video: %v", err)
		}
		return id
	}
	video, blockedVideo, privateVideo, unlistedVideo := mkVideo("public"), mkVideo("public"), mkVideo("private"), mkVideo("unlisted")
	exec(`INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'test')`, blockedVideo)

	// owe queues a notification email for recipient, due long ago so this
	// test's rows sort ahead of anything else in a shared database.
	owe := func(recipient uuid.UUID, typ string, videoID uuid.UUID, withComment bool) uuid.UUID {
		t.Helper()
		var commentID *uuid.UUID
		if withComment {
			var c uuid.UUID
			if err := st.Pool.QueryRow(ctx,
				`INSERT INTO comments (video_id, user_id, body) VALUES ($1, $2, 'hi') RETURNING id`, videoID, actor,
			).Scan(&c); err != nil {
				t.Fatalf("seed comment: %v", err)
			}
			commentID = &c
		}
		actorID := actor
		if typ == "new_video" {
			actorID = owner
		}
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO notifications (user_id, type, actor_id, channel_id, video_id, comment_id)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			recipient, typ, actorID, channelID, videoID, commentID,
		).Scan(&id); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
		exec(`INSERT INTO notification_emails (notification_id, next_attempt_at) VALUES ($1, '2000-01-01')`, id)
		return id
	}
	optIn := func(u uuid.UUID, typ string) {
		t.Helper()
		if err := q.UpsertNotificationEmailPref(ctx, sqlcgen.UpsertNotificationEmailPrefParams{UserID: u, Type: typ, EmailEnabled: true}); err != nil {
			t.Fatalf("seed email pref: %v", err)
		}
	}

	want := map[uuid.UUID]string{} // notification → why, for every row; deliverable when why == ""
	add := func(name string, verified bool, typ string, videoID uuid.UUID, why string, prep func(u, n uuid.UUID)) uuid.UUID {
		u := mkUser(name, verified)
		optIn(u, typ)
		if typ == "new_video" {
			// A new_video recipient follows the channel with the bell on.
			exec(`INSERT INTO channel_follows (follower_id, channel_id) VALUES ($1, $2)`, u, channelID)
		}
		n := owe(u, typ, videoID, typ != "new_video")
		if prep != nil {
			prep(u, n)
		}
		want[n] = why
		return n
	}
	okComment := add("claim-ok", true, "comment", video, "", nil)
	okNewVideo := add("claim-okvideo", true, "new_video", video, "", nil)
	add("claim-unverified", false, "comment", video, "address never verified", nil)
	add("claim-inactive", true, "comment", video, "account deactivated", func(u, _ uuid.UUID) {
		exec(`UPDATE users SET is_active = FALSE WHERE id = $1`, u)
	})
	add("claim-deleted", true, "comment", video, "account deleted", func(u, _ uuid.UUID) {
		exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, u)
	})
	add("claim-prefoff", true, "comment", video, "turned this email off since", func(u, _ uuid.UUID) {
		exec(`UPDATE notification_prefs SET email_enabled = FALSE WHERE user_id = $1`, u)
	})
	add("claim-muter", true, "comment", video, "muted the actor since", func(u, _ uuid.UUID) {
		exec(`INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, u, actor)
	})
	add("claim-blocked", true, "comment_reply", video, "blocked by the actor since", func(u, _ uuid.UUID) {
		exec(`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, actor, u)
	})
	add("claim-blocker", true, "comment_reply", video, "blocked the actor since", func(u, _ uuid.UUID) {
		exec(`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, u, actor)
	})
	add("claim-inappoff", true, "comment", video, "turned the in-app type off since", func(u, _ uuid.UUID) {
		exec(`UPDATE notification_prefs SET enabled = FALSE WHERE user_id = $1`, u)
	})
	add("claim-nullemail", true, "comment", video, "no explicit email opt-in", func(u, _ uuid.UUID) {
		exec(`UPDATE notification_prefs SET email_enabled = NULL WHERE user_id = $1`, u)
	})
	add("claim-unfollowed", true, "new_video", video, "unfollowed the channel since", func(u, _ uuid.UUID) {
		exec(`DELETE FROM channel_follows WHERE follower_id = $1`, u)
	})
	add("claim-bellnone", true, "new_video", video, "set the channel bell to none since", func(u, _ uuid.UUID) {
		exec(`UPDATE channel_follows SET notification_setting = 'none' WHERE follower_id = $1`, u)
	})
	add("claim-cprivate", true, "comment", privateVideo, "commented video made private since", nil)
	add("claim-runlisted", true, "comment_reply", unlistedVideo, "thread's video made unlisted since", nil)
	add("claim-gone", true, "comment", video, "comment deleted since", func(_, n uuid.UUID) {
		exec(`UPDATE comments SET deleted_at = now() WHERE id = (SELECT comment_id FROM notifications WHERE id = $1)`, n)
	})
	add("claim-vblock", true, "comment", blockedVideo, "video under a moderation block", nil)
	add("claim-private", true, "new_video", privateVideo, "new video no longer public", nil)

	// claim returns only THIS test's rows from one claim, plus how many rows it
	// claimed in all: a shared database may hold other tests' due rows, which
	// the claim may take but the assertions must never count.
	claim := func(limit int32) (map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow, int) {
		t.Helper()
		rows, err := q.ClaimDueNotificationEmails(ctx, sqlcgen.ClaimDueNotificationEmailsParams{ClaimLimit: limit, LeaseSeconds: 600})
		if err != nil {
			t.Fatalf("ClaimDueNotificationEmails: %v", err)
		}
		if len(rows) > int(limit) {
			t.Fatalf("claimed %d rows, over the limit of %d", len(rows), limit)
		}
		got := map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow{}
		for _, r := range rows {
			if _, ours := want[r.NotificationID]; ours {
				got[r.NotificationID] = r
			}
		}
		return got, len(rows)
	}
	// claimOurs claims until every one of this test's due rows is in hand or
	// the queue has nothing due left, however many foreign rows it wades through.
	claimOurs := func(got map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow) {
		t.Helper()
		for len(got) < len(want) {
			batch, total := claim(100)
			for id, r := range batch {
				got[id] = r
			}
			if total == 0 {
				return
			}
		}
	}

	got, _ := claim(4)
	if len(got) > 4 {
		t.Fatalf("first claim of 4 returned %d of this test's rows", len(got))
	}
	claimOurs(got)
	if len(got) != len(want) {
		t.Fatalf("claimed %d of %d owed rows", len(got), len(want))
	}
	// Leased: nothing of ours comes back until the lease runs out.
	again := map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow{}
	claimOurs(again)
	if len(again) != 0 {
		t.Fatalf("leased rows were claimed again: %d", len(again))
	}

	for id, why := range want {
		r := got[id]
		if r.Attempts != 1 {
			t.Errorf("%s: attempts = %d, want 1 after one claim", r.Email, r.Attempts)
		}
		if r.Deliverable != (why == "") {
			t.Errorf("%s: deliverable = %v, want %v (%s)", r.Email, r.Deliverable, why == "", why)
		}
	}
	if r := got[okComment]; r.ActorName != "Bob B." || r.VideoTitle != "Soldering 101" || r.Type != "comment" ||
		r.Email != "claim-ok-"+suffix+"@example.test" {
		t.Errorf("comment row rendered as %+v", r)
	}
	if r := got[okNewVideo]; r.ChannelName != "Claim Lab" || r.Type != "new_video" {
		t.Errorf("new_video row rendered as %+v", r)
	}

	// A failed send is rescheduled: due again, claimed again, attempts counted.
	// A sent one is deleted for good.
	if err := q.RescheduleNotificationEmail(ctx, sqlcgen.RescheduleNotificationEmailParams{
		NotificationID: okComment, NextAttemptAt: time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RescheduleNotificationEmail: %v", err)
	}
	retried := map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow{}
	claimOurs(retried)
	if r, ok := retried[okComment]; !ok || r.Attempts != 2 || len(retried) != 1 {
		t.Fatalf("after reschedule: %+v, want only the rescheduled row at attempt 2", retried)
	}
	if err := q.DeleteNotificationEmail(ctx, okComment); err != nil {
		t.Fatalf("DeleteNotificationEmail: %v", err)
	}
	var left int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_emails WHERE notification_id = $1`, okComment).Scan(&left); err != nil || left != 0 {
		t.Fatalf("row after delete: %d (err %v), want 0", left, err)
	}
}

// TestClaimDueNotificationEmailsIsExclusiveOnRealPG proves two drains claiming
// at the same moment take DISJOINT rows — the property that stops two api
// instances mailing the same notification twice. The first claim's transaction
// is held open while the second runs, under a lock timeout: without SKIP LOCKED
// the second claim would either block on the first's row locks (and time out)
// or take the same rows.
func TestClaimDueNotificationEmailsIsExclusiveOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	suffix := uuid.NewString()[:8]
	var user uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, email_verified) VALUES ($1, $2, 'x', TRUE) RETURNING id`,
		"claimx-"+suffix, "claimx-"+suffix+"@example.test",
	).Scan(&user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user) })

	// Six owed rows, due before anything else a shared database could hold.
	ours := map[uuid.UUID]bool{}
	for i := 0; i < 6; i++ {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO notifications (user_id, type) VALUES ($1, 'comment') RETURNING id`, user,
		).Scan(&id); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO notification_emails (notification_id, next_attempt_at) VALUES ($1, '1970-01-01')`, id,
		); err != nil {
			t.Fatalf("seed outbox: %v", err)
		}
		ours[id] = true
	}

	claimIn := func(tx pgx.Tx) map[uuid.UUID]bool {
		t.Helper()
		if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '3s'`); err != nil {
			t.Fatalf("lock_timeout: %v", err)
		}
		rows, err := sqlcgen.New(tx).ClaimDueNotificationEmails(ctx,
			sqlcgen.ClaimDueNotificationEmailsParams{ClaimLimit: 3, LeaseSeconds: 600})
		if err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
		got := map[uuid.UUID]bool{}
		for _, r := range rows {
			if ours[r.NotificationID] {
				got[r.NotificationID] = true
			}
		}
		return got
	}
	tx1, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 1: %v", err)
	}
	defer func() { _ = tx1.Rollback(context.Background()) }()
	tx2, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin 2: %v", err)
	}
	defer func() { _ = tx2.Rollback(context.Background()) }()

	first := claimIn(tx1) // holds its row locks: tx1 stays open
	second := claimIn(tx2)
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("claims took %d and %d of this test's rows, want 3 each", len(first), len(second))
	}
	for id := range first {
		if second[id] {
			t.Fatalf("notification %s was claimed by both drains", id)
		}
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// The prune used while the instance has no mail path takes only rows past
	// the age limit, claimed or not.
	var old []uuid.UUID
	for id := range first {
		old = append(old, id)
	}
	if _, err := st.Pool.Exec(ctx,
		`UPDATE notification_emails SET created_at = now() - interval '25 hours' WHERE notification_id = ANY($1)`, old,
	); err != nil {
		t.Fatalf("age rows: %v", err)
	}
	if _, err := sqlcgen.New(st.Pool).DeleteStaleNotificationEmails(ctx, (24 * time.Hour).Seconds()); err != nil {
		t.Fatalf("DeleteStaleNotificationEmails: %v", err)
	}
	var left int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_emails ne
		JOIN notifications n ON n.id = ne.notification_id WHERE n.user_id = $1`, user).Scan(&left); err != nil || left != 3 {
		t.Fatalf("rows left after prune = %d (err %v), want the 3 younger than a day", left, err)
	}
}
