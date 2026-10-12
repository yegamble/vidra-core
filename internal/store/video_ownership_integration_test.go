//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestResolveOwnershipRecipient: one answer (no row) for every account a video
// may not be given to, and the mute is reported rather than refused.
func TestResolveOwnershipRecipient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

	giver, _ := seedOwnerChannel(ctx, t, st, "ocG")
	taker, _ := seedOwnerChannel(ctx, t, st, "ocT")
	var name string
	if err := st.Pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, taker).Scan(&name); err != nil {
		t.Fatal(err)
	}
	resolve := func() (sqlcgen.ResolveOwnershipRecipientRow, error) {
		return q.ResolveOwnershipRecipient(ctx, sqlcgen.ResolveOwnershipRecipientParams{InitiatorID: giver, Username: strings.ToUpper(name)})
	}
	if r, err := resolve(); err != nil || r.ID != taker || r.MutedInitiator {
		t.Fatalf("eligible (case-insensitive) = %+v, %v", r, err)
	}
	for _, c := range []struct{ name, on, off string }{
		{"banned", `UPDATE users SET is_active = false WHERE id = $1`, `UPDATE users SET is_active = true WHERE id = $1`},
		{"deleted", `UPDATE users SET deleted_at = now() WHERE id = $1`, `UPDATE users SET deleted_at = NULL WHERE id = $1`},
		{"blocked by recipient", `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, `DELETE FROM user_blocks WHERE blocker_id = $1`},
		{"blocked recipient", `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($2, $1)`, `DELETE FROM user_blocks WHERE blocked_id = $1`},
	} {
		args := []any{taker}
		if strings.HasPrefix(c.name, "blocked") {
			args = append(args, giver)
		}
		exec(c.on, args...)
		if _, err := resolve(); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: err = %v, want no row", c.name, err)
		}
		exec(c.off, taker)
	}
	if _, err := q.ResolveOwnershipRecipient(ctx, sqlcgen.ResolveOwnershipRecipientParams{InitiatorID: giver, Username: "nobody-" + uuid.NewString()}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown: err = %v, want no row", err)
	}
	exec(`INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, taker, giver)
	if r, err := resolve(); err != nil || !r.MutedInitiator {
		t.Errorf("muted = %+v, %v, want the recipient with muted_initiator", r, err)
	}
}

// TestVideoOwnershipChangeLifecycle proves the request table against real
// PostgreSQL: one pending request per video, the recipient's list, and an
// accept that moves the video in the same statement — only while the request
// is pending and theirs, the initiator still owns the video and the target
// channel is the recipient's.
func TestVideoOwnershipChangeLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	giver, giverCh := seedOwnerChannel(ctx, t, st, "olG")
	taker, takerCh := seedOwnerChannel(ctx, t, st, "olT")
	other, otherCh := seedOwnerChannel(ctx, t, st, "olO")
	vid := seedPublishedVideo(ctx, t, st, giverCh, "gift")
	for _, size := range []int64{100, 23} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO video_files (video_id, kind, storage_key, size_bytes) VALUES ($1, 'original', $2, $3)`,
			vid, uuid.NewString(), size); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := q.SumVideoStorageUsage(ctx, vid); err != nil || n != 123 {
		t.Fatalf("SumVideoStorageUsage = %d, %v; want 123", n, err)
	}

	give := func() (sqlcgen.VideoOwnershipChange, error) {
		return q.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{VideoID: vid, InitiatorID: giver, NextOwnerID: taker, VideoTitle: "gift"})
	}
	first, err := give()
	if err != nil || first.Status != "pending" {
		t.Fatalf("give = %+v, %v", first, err)
	}
	if _, err := give(); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second pending give: err = %v, want no row", err)
	}
	if n, err := q.RefuseVideoOwnershipChange(ctx, sqlcgen.RefuseVideoOwnershipChangeParams{ID: first.ID, NextOwnerID: other}); err != nil || n != 0 {
		t.Fatalf("refuse by a non-recipient = %d, %v", n, err)
	}
	if n, err := q.RefuseVideoOwnershipChange(ctx, sqlcgen.RefuseVideoOwnershipChangeParams{ID: first.ID, NextOwnerID: taker}); err != nil || n != 1 {
		t.Fatalf("refuse = %d, %v", n, err)
	}
	second, err := give() // a refused request no longer blocks a new one
	if err != nil {
		t.Fatalf("give after refuse: %v", err)
	}

	rows, err := q.ListIncomingVideoOwnershipChanges(ctx, sqlcgen.ListIncomingVideoOwnershipChangesParams{NextOwnerID: taker, ResultLimit: 10})
	if err != nil || len(rows) != 2 || rows[0].ID != second.ID || rows[0].VideoTitle != "gift" || rows[1].Status != "refused" {
		t.Fatalf("list = %+v, %v", rows, err)
	}
	if n, _ := q.CountIncomingVideoOwnershipChanges(ctx, taker); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	if n, _ := q.CountIncomingVideoOwnershipChanges(ctx, other); n != 0 {
		t.Errorf("count for a bystander = %d, want 0", n)
	}

	accept := func(as, ch uuid.UUID) (sqlcgen.AcceptVideoOwnershipChangeRow, error) {
		return q.AcceptVideoOwnershipChange(ctx, sqlcgen.AcceptVideoOwnershipChangeParams{ID: second.ID, NextOwnerID: as, ChannelID: ch})
	}
	untouched := func(label string) {
		t.Helper()
		row, err := q.GetVideoByID(ctx, vid)
		oc, gerr := q.GetVideoOwnershipChange(ctx, second.ID)
		if err != nil || gerr != nil || row.ChannelID != giverCh || oc.Status != "pending" {
			t.Fatalf("%s changed state: channel=%s status=%s (%v, %v)", label, row.ChannelID, oc.Status, err, gerr)
		}
	}
	if _, err := accept(other, otherCh); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("accept by a non-recipient: err = %v", err)
	}
	untouched("non-recipient accept")
	if _, err := accept(taker, otherCh); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("accept into a foreign channel: err = %v", err)
	}
	untouched("foreign-channel accept")

	// The initiator loses the video between give and accept: refused.
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET owner_id = $1 WHERE id = $2`, other, giverCh); err != nil {
		t.Fatal(err)
	}
	if _, err := accept(taker, takerCh); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("accept after the initiator lost the video: err = %v", err)
	}
	untouched("stale accept")
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET owner_id = $1 WHERE id = $2`, giver, giverCh); err != nil {
		t.Fatal(err)
	}

	moved, err := accept(taker, takerCh)
	if err != nil || moved.ID != vid || moved.FromChannelID != giverCh {
		t.Fatalf("accept = %+v, %v", moved, err)
	}
	row, err := q.GetVideoByID(ctx, vid)
	if err != nil || row.ChannelID != takerCh || row.OwnerID != taker {
		t.Fatalf("after accept: channel=%s owner=%s err=%v", row.ChannelID, row.OwnerID, err)
	}
	if oc, _ := q.GetVideoOwnershipChange(ctx, second.ID); oc.Status != "accepted" {
		t.Errorf("status after accept = %q", oc.Status)
	}
	if _, err := accept(taker, takerCh); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("second accept: err = %v, want no row", err)
	}
	if n, _ := q.RefuseVideoOwnershipChange(ctx, sqlcgen.RefuseVideoOwnershipChangeParams{ID: second.ID, NextOwnerID: taker}); n != 0 {
		t.Errorf("refusing an accepted request changed %d rows", n)
	}

	// Deleting the video takes its requests with it.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM videos WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.CountIncomingVideoOwnershipChanges(ctx, taker); n != 0 {
		t.Errorf("requests after the video was deleted = %d, want 0", n)
	}
}

// TestVideoOwnershipChangeGuards: what the recipient sees never follows the
// video after the request; stale and expired requests neither block the current
// owner nor accept; the giver can withdraw; a refusal starts a cooldown for the
// pair; and no transfer happens while media work on the video is unfinished.
func TestVideoOwnershipChangeGuards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	giver, giverCh := seedOwnerChannel(ctx, t, st, "ogG")
	taker, takerCh := seedOwnerChannel(ctx, t, st, "ogT")
	other, otherCh := seedOwnerChannel(ctx, t, st, "ogO")
	vid := seedPublishedVideo(ctx, t, st, giverCh, "Original Title")
	give := func(from, to uuid.UUID) (sqlcgen.VideoOwnershipChange, error) {
		return q.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{
			VideoID: vid, InitiatorID: from, NextOwnerID: to, VideoTitle: "Original Title"})
	}
	list := func(who uuid.UUID, expiredBefore time.Time) []sqlcgen.ListIncomingVideoOwnershipChangesRow {
		t.Helper()
		rows, err := q.ListIncomingVideoOwnershipChanges(ctx, sqlcgen.ListIncomingVideoOwnershipChangesParams{
			NextOwnerID: who, ExpiredBefore: expiredBefore, ResultLimit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	window := time.Now().Add(-30 * 24 * time.Hour)
	statusOf := func(id uuid.UUID) string {
		t.Helper()
		for _, r := range list(taker, window) {
			if r.ID == id {
				return r.Status
			}
		}
		t.Fatalf("request %s not listed", id)
		return ""
	}

	// Withdraw: only the giver, only while pending.
	first, err := give(giver, taker)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := q.CancelVideoOwnershipChange(ctx, sqlcgen.CancelVideoOwnershipChangeParams{VideoID: vid, InitiatorID: taker}); n != 0 {
		t.Errorf("a non-giver cancelled %d rows", n)
	}
	if n, _ := q.CancelVideoOwnershipChange(ctx, sqlcgen.CancelVideoOwnershipChangeParams{VideoID: vid, InitiatorID: giver}); n != 1 {
		t.Fatalf("giver cancel = %d rows", n)
	}
	if oc, _ := q.GetVideoOwnershipChange(ctx, first.ID); oc.Status != "cancelled" {
		t.Errorf("status after cancel = %q", oc.Status)
	}

	// Refusal cooldown: per (giver, recipient), from the refusal.
	refused, err := give(giver, taker)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := q.RefuseVideoOwnershipChange(ctx, sqlcgen.RefuseVideoOwnershipChangeParams{ID: refused.ID, NextOwnerID: taker}); n != 1 {
		t.Fatal("refuse")
	}
	cool := func(from, to uuid.UUID, after time.Time) bool {
		b, err := q.HasRecentOwnershipRefusal(ctx, sqlcgen.HasRecentOwnershipRefusalParams{InitiatorID: from, NextOwnerID: to, RefusedAfter: after})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !cool(giver, taker, time.Now().Add(-24*time.Hour)) || cool(giver, taker, time.Now().Add(time.Hour)) || cool(giver, other, time.Now().Add(-24*time.Hour)) {
		t.Error("refusal cooldown is not per pair and per window")
	}

	// The list shows the title AT GIVE TIME, whatever the video is called now.
	exec(`UPDATE videos SET title = 'Renamed Later' WHERE id = $1`, vid)
	for _, r := range list(taker, window) {
		if r.VideoTitle != "Original Title" {
			t.Errorf("list title for a %s request = %q, want the snapshot", r.Status, r.VideoTitle)
		}
	}

	// Expired by the offer window: listed as expired, and never accepted.
	pending, err := give(giver, taker)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE video_ownership_changes SET created_at = now() - interval '31 days' WHERE id = $1`, pending.ID)
	if got := statusOf(pending.ID); got != "expired" {
		t.Errorf("an old pending request lists as %q, want expired", got)
	}
	if _, err := q.AcceptVideoOwnershipChange(ctx, sqlcgen.AcceptVideoOwnershipChangeParams{
		ID: pending.ID, NextOwnerID: taker, ChannelID: takerCh, ExpiredBefore: window}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("accepting an expired request: err = %v", err)
	}

	// A stale request (giver lost the video) does not block the new owner.
	exec(`UPDATE video_ownership_changes SET created_at = now() WHERE id = $1`, pending.ID)
	exec(`UPDATE channels SET owner_id = $1 WHERE id = $2`, other, giverCh)
	if got := statusOf(pending.ID); got != "expired" {
		t.Errorf("a stale pending request lists as %q, want expired", got)
	}
	if n, err := q.ExpireStaleVideoOwnershipChanges(ctx, sqlcgen.ExpireStaleVideoOwnershipChangesParams{VideoID: vid, ExpiredBefore: window}); err != nil || n != 1 {
		t.Fatalf("expire stale = %d, %v", n, err)
	}
	if busy, _ := q.HasPendingVideoOwnershipChange(ctx, vid); busy {
		t.Error("a stale request still counts as pending")
	}
	fresh, err := give(other, taker) // the new owner can offer it
	if err != nil {
		t.Fatalf("new owner give: %v", err)
	}
	if n, _ := q.ExpireStaleVideoOwnershipChanges(ctx, sqlcgen.ExpireStaleVideoOwnershipChangesParams{VideoID: vid, ExpiredBefore: window}); n != 0 {
		t.Errorf("expire touched a live request (%d rows)", n)
	}

	// Media work in flight blocks the transfer, in the accept statement itself.
	accept := func() error {
		_, err := q.AcceptVideoOwnershipChange(ctx, sqlcgen.AcceptVideoOwnershipChangeParams{
			ID: fresh.ID, NextOwnerID: taker, ChannelID: takerCh, ExpiredBefore: window})
		return err
	}
	var session uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO upload_sessions (video_id, user_id, filename, total_size, chunk_size, purpose, expires_at)
		VALUES ($1, $2, 'r.mp4', 10, 5, 'replace', now() + interval '1 hour') RETURNING id`, vid, other).Scan(&session); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, on, off string }{
		{"open replace session", `SELECT 1`, `UPDATE upload_sessions SET state = 'queued' WHERE id = '` + session.String() + `'`},
		{"queued replace session", `SELECT 1`, `UPDATE upload_sessions SET state = 'completed' WHERE id = '` + session.String() + `'`},
		{"finalize job", `INSERT INTO upload_finalize_jobs (upload_id, video_id, purpose) VALUES ('` + session.String() + `', '` + vid.String() + `', 'replace')`, `UPDATE upload_finalize_jobs SET state = 'done'`},
		{"import job", `INSERT INTO import_jobs (video_id, url) VALUES ('` + vid.String() + `', 'https://example.test/v')`, `UPDATE import_jobs SET state = 'failed' WHERE video_id = '` + vid.String() + `'`},
		{"transcode job", `INSERT INTO transcode_jobs (video_id, source_key, state) VALUES ('` + vid.String() + `', 'k', 'running')`, `UPDATE transcode_jobs SET state = 'done' WHERE video_id = '` + vid.String() + `'`},
	} {
		exec(c.on)
		if busy, err := q.VideoMediaWorkInFlight(ctx, vid); err != nil || !busy {
			t.Errorf("%s: in flight = %v, %v", c.name, busy, err)
		}
		if err := accept(); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("%s: accept err = %v, want no row", c.name, err)
		}
		exec(c.off)
	}
	if busy, _ := q.VideoMediaWorkInFlight(ctx, vid); busy {
		t.Fatal("finished work still reads as in flight")
	}
	if err := accept(); err != nil {
		t.Fatalf("accept once the work finished: %v", err)
	}
	if row, _ := q.GetVideoByID(ctx, vid); row.ChannelID != takerCh {
		t.Errorf("accept did not move the video")
	}
	_ = otherCh
}
