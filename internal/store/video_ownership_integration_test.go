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
		return q.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{VideoID: vid, InitiatorID: giver, NextOwnerID: taker})
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
