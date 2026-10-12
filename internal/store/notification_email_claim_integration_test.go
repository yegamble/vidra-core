//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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
	video, blockedVideo, privateVideo := mkVideo("public"), mkVideo("public"), mkVideo("private")
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
	add("claim-gone", true, "comment", video, "comment deleted since", func(_, n uuid.UUID) {
		exec(`UPDATE comments SET deleted_at = now() WHERE id = (SELECT comment_id FROM notifications WHERE id = $1)`, n)
	})
	add("claim-vblock", true, "comment", blockedVideo, "video under a moderation block", nil)
	add("claim-private", true, "new_video", privateVideo, "new video no longer public", nil)

	claim := func(limit int32) map[uuid.UUID]sqlcgen.ClaimDueNotificationEmailsRow {
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
		return got
	}

	got := claim(4)
	if len(got) != 4 {
		t.Fatalf("first claim of 4 returned %d of this test's rows (they are due earliest)", len(got))
	}
	for id, r := range claim(100) {
		got[id] = r
	}
	if len(got) != len(want) {
		t.Fatalf("claimed %d of %d owed rows", len(got), len(want))
	}
	if again := claim(100); len(again) != 0 {
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
	retried := claim(100)
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
