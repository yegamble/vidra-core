//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestNotificationEmailOutboxOnRealPG is the proof behind migration 0156: WHO is
// owed a notification email is decided in the same SQL statement that creates
// the in-app notification, so — like the 0101 fan-out it extends — only a live
// schema can hold the rule. It asserts the queued set EXACTLY, because the
// failure that matters is silent over-delivery: mailing someone who never
// opted in, or mailing again on a hook that fires twice.
func TestNotificationEmailOutboxOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	setInApp := func(user uuid.UUID, typ string, on bool) {
		t.Helper()
		if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{UserID: user, Type: typ, Enabled: on}); err != nil {
			t.Fatalf("seed in-app pref: %v", err)
		}
	}
	setEmail := func(user uuid.UUID, typ string, on bool) {
		t.Helper()
		if err := q.UpsertNotificationEmailPref(ctx, sqlcgen.UpsertNotificationEmailPrefParams{UserID: user, Type: typ, EmailEnabled: on}); err != nil {
			t.Fatalf("seed email pref: %v", err)
		}
	}
	// owed reads back exactly whose notifications are owed an email.
	owed := func() map[uuid.UUID]int {
		t.Helper()
		rows, err := st.Pool.Query(ctx, `
			SELECT n.user_id FROM notification_emails ne
			JOIN notifications n ON n.id = ne.notification_id
			JOIN users u ON u.id = n.user_id
			WHERE u.username LIKE '%-' || $1::text`, suffix)
		if err != nil {
			t.Fatalf("read outbox: %v", err)
		}
		defer rows.Close()
		got := map[uuid.UUID]int{}
		for rows.Next() {
			var u uuid.UUID
			if err := rows.Scan(&u); err != nil {
				t.Fatalf("scan outbox: %v", err)
			}
			got[u]++
		}
		return got
	}

	owner := mkUser("mail-owner")
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Mail') RETURNING id`,
		owner, "mail-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	optIn := mkUser("mail-optin")         // email on for new_video        → notified + owed
	inAppOnly := mkUser("mail-inapp")     // nothing stored (email default) → notified, not owed
	inAppOn := mkUser("mail-inappon")     // in-app row, email NULL         → notified, not owed
	optedOut := mkUser("mail-optout")     // email explicitly off           → notified, not owed
	otherType := mkUser("mail-othertype") // email on for comment only      → notified, not owed
	inAppOff := mkUser("mail-inappoff")   // email on, in-app new_video off → neither
	muter := mkUser("mail-muter")         // email on, muted the owner      → neither
	for _, u := range []uuid.UUID{optIn, inAppOnly, inAppOn, optedOut, otherType, inAppOff, muter} {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO channel_follows (follower_id, channel_id) VALUES ($1, $2)`, u, channelID,
		); err != nil {
			t.Fatalf("seed follow: %v", err)
		}
	}
	setEmail(optIn, "new_video", true)
	setInApp(inAppOn, "new_video", true)
	setEmail(optedOut, "new_video", false)
	setEmail(otherType, "comment", true)
	setEmail(inAppOff, "new_video", true)
	setInApp(inAppOff, "new_video", false)
	setEmail(muter, "new_video", true)
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, muter, owner,
	); err != nil {
		t.Fatalf("seed mute: %v", err)
	}

	// The two columns never write through each other: an in-app change keeps
	// the email choice, and an email-only row carries the in-app default.
	setInApp(optIn, "new_video", true)
	var inAppDefault bool
	if err := st.Pool.QueryRow(ctx,
		`SELECT enabled FROM notification_prefs WHERE user_id = $1 AND type = 'new_video'`, optedOut,
	).Scan(&inAppDefault); err != nil || !inAppDefault {
		t.Fatalf("email-only row enabled = %v (err %v), want the in-app default TRUE", inAppDefault, err)
	}
	emailRows, err := q.ListNotificationEmailPrefs(ctx, optIn)
	if err != nil || len(emailRows) != 1 || emailRows[0].Type != "new_video" || !emailRows[0].EmailEnabled {
		t.Fatalf("optIn email prefs after an in-app upsert = %+v (err %v), want [new_video:true]", emailRows, err)
	}
	if rows, err := q.ListNotificationEmailPrefs(ctx, inAppOnly); err != nil || len(rows) != 0 {
		t.Fatalf("a user with no email choice listed %+v (err %v), want none", rows, err)
	}

	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'Owed', 'public', 'published') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	notified, err := q.NotifyFollowersOfNewVideo(ctx, videoID)
	if err != nil {
		t.Fatalf("NotifyFollowersOfNewVideo: %v", err)
	}
	if notified != 5 {
		t.Fatalf("notified = %d, want 5 (the count is followers notified, not emails queued)", notified)
	}
	if got := owed(); len(got) != 1 || got[optIn] != 1 {
		t.Fatalf("owed after fan-out = %v, want exactly one email for the opted-in follower", got)
	}

	// The drain deletes a row once it is sent. A publish hook that fires again
	// must not queue that email a second time: the queue is keyed off what the
	// notification INSERT returned, and the re-run inserts nothing.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM notification_emails ne USING notifications n
		WHERE n.id = ne.notification_id AND n.user_id = $1`, optIn); err != nil {
		t.Fatalf("simulate a sent email: %v", err)
	}
	if again, err := q.NotifyFollowersOfNewVideo(ctx, videoID); err != nil || again != 0 {
		t.Fatalf("re-run notified %d (err %v), want 0", again, err)
	}
	if got := owed(); len(got) != 0 {
		t.Fatalf("re-run queued %v, want nothing", got)
	}

	// The single-row path decides in the same statement: email on for comment
	// is owed; the same type for a user who never opted in is not; and a type
	// the user turned on email for does not leak into another type.
	vid := pgtype.UUID{Bytes: videoID, Valid: true}
	create := func(user uuid.UUID, typ string) uuid.UUID {
		t.Helper()
		row, err := q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{UserID: user, Type: typ, VideoID: vid})
		if err != nil {
			t.Fatalf("CreateNotification(%s): %v", typ, err)
		}
		return row.ID
	}
	mailed := create(otherType, "comment")
	create(inAppOnly, "comment")
	setInApp(inAppOn, "comment", true)
	create(inAppOn, "comment")
	create(otherType, "comment_reply")
	if got := owed(); len(got) != 1 || got[otherType] != 1 {
		t.Fatalf("owed after single creates = %v, want exactly the comment owed to otherType", got)
	}

	// The debt goes with the notification it belongs to.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM notifications WHERE id = $1`, mailed); err != nil {
		t.Fatalf("delete notification: %v", err)
	}
	if got := owed(); len(got) != 0 {
		t.Fatalf("owed after deleting the notification = %v, want nothing", got)
	}
}
