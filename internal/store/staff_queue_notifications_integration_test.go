//go:build integration

// Integration test for the three staff approval-queue fan-outs (migration
// 0157). Every WHO-gets-told rule lives in SQL, so it is proved here on real
// PostgreSQL. The DB is shared with sibling tests that leave staff behind, so
// membership is asserted only over this test's own seeded users.
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestStaffQueueNotificationFanOutsOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var seeded []uuid.UUID
	mkUser := func(name, role string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, 'x', $3) RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test", role,
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		seeded = append(seeded, id)
		return id
	}
	admin := mkUser("sq-admin", "admin")
	mod := mkUser("sq-mod", "moderator")
	plain := mkUser("sq-user", "user")
	inactive := mkUser("sq-inactive", "admin")
	deleted := mkUser("sq-deleted", "moderator")
	prefOff := mkUser("sq-prefoff", "admin")
	uploaderMod := mkUser("sq-uploader", "moderator")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"new_registration_request", "upload_quarantined", "federation_follower_request"} {
		if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{UserID: prefOff, Type: typ, Enabled: false}); err != nil {
			t.Fatal(err)
		}
	}

	// told returns which of the seeded users hold a notification of typ whose
	// subject column col equals subject.
	told := func(typ, col string, subject uuid.UUID) map[uuid.UUID]bool {
		t.Helper()
		rows, err := st.Pool.Query(ctx,
			`SELECT user_id FROM notifications WHERE type = $1 AND `+col+` = $2 AND user_id = ANY($3)`,
			typ, subject, seeded)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got[id] = true
		}
		return got
	}
	assertSet := func(label string, got map[uuid.UUID]bool, want ...uuid.UUID) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: told %d seeded users, want %d (%v)", label, len(got), len(want), got)
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("%s: %s was not told", label, w)
			}
		}
	}

	// Registration requests: admin + moderator.
	var reqID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO registration_requests (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"applicant-"+suffix, "applicant-"+suffix+"@example.test").Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM registration_requests WHERE id = $1`, reqID)
	})
	if _, err := q.NotifyStaffOfNewRegistrationRequest(ctx, reqID); err != nil {
		t.Fatal(err)
	}
	assertSet("registration", told("new_registration_request", "registration_request_id", reqID), admin, mod, uploaderMod)
	before := len(told("new_registration_request", "registration_request_id", reqID))
	if _, err := q.NotifyStaffOfNewRegistrationRequest(ctx, reqID); err != nil {
		t.Fatal(err)
	}
	if after := len(told("new_registration_request", "registration_request_id", reqID)); after != before {
		t.Errorf("registration fan-out is not idempotent: %d -> %d", before, after)
	}

	// Quarantined upload: admin + moderator, never the uploader, only while quarantined.
	_, channelID := seedOwnerChannel(ctx, t, st, "sqchan")
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET owner_id = $1 WHERE id = $2`, uploaderMod, channelID); err != nil {
		t.Fatal(err)
	}
	videoID := seedPublishedVideo(ctx, t, st, channelID, "parked")
	if n, err := q.NotifyStaffOfQuarantinedUpload(ctx, videoID); err != nil || n != 0 {
		t.Fatalf("published video fan-out = %d, %v; want 0 (not quarantined)", n, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE videos SET state = 'quarantined' WHERE id = $1`, videoID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NotifyStaffOfQuarantinedUpload(ctx, videoID); err != nil {
		t.Fatal(err)
	}
	assertSet("quarantine", told("upload_quarantined", "video_id", videoID), admin, mod)
	if n, err := q.NotifyStaffOfQuarantinedUpload(ctx, videoID); err != nil || n != 0 {
		t.Errorf("repeat quarantine fan-out inserted %d, %v; want 0", n, err)
	}

	// Federation follower request: admins only (the routes are admin-only).
	var followID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO remote_follows (channel_id, remote_actor_url, state, follow_activity_url)
		 VALUES ($1, $2, 'pending', $3) RETURNING id`,
		channelID, "https://remote.example/users/"+suffix, "https://remote.example/follow/"+suffix).Scan(&followID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NotifyStaffOfFederationFollowerRequest(ctx, followID); err != nil {
		t.Fatal(err)
	}
	assertSet("follower request", told("federation_follower_request", "remote_follow_id", followID), admin)
	if n, err := q.NotifyStaffOfFederationFollowerRequest(ctx, followID); err != nil || n != 0 {
		t.Errorf("repeat follower fan-out inserted %d, %v; want 0", n, err)
	}
	_ = plain
}
