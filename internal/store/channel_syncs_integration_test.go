//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedUserChannel seeds a user → channel FK chain and returns both ids plus a
// cleanup that cascades the whole chain via the users ON DELETE CASCADE.
func seedUserChannel(t *testing.T, st *Store) (userID, channelID uuid.UUID, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"csync-"+suffix, "csync-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	cleanup = func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'CS') RETURNING id`,
		userID, "csync_"+suffix,
	).Scan(&channelID); err != nil {
		cleanup()
		t.Fatalf("seed channel: %v", err)
	}
	return userID, channelID, cleanup
}

// TestChannelSyncQueriesPersist exercises the W2.C4 channel-sync queries against
// real PostgreSQL: create + unique constraint, list/count, due-claim state flip,
// the seen-ledger dedupe, finish/fail transitions, and the ON DELETE CASCADE from
// channel → channel_syncs → channel_sync_seen.
func TestChannelSyncQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	userID, channelID, cleanup := seedUserChannel(t, st)
	defer cleanup()

	// Create.
	sync, err := q.CreateChannelSync(ctx, sqlcgen.CreateChannelSyncParams{
		ChannelID:          channelID,
		UserID:             userID,
		ExternalChannelUrl: "https://youtube.com/@example",
	})
	if err != nil {
		t.Fatalf("CreateChannelSync: %v", err)
	}
	if sync.State != "waiting_first_run" {
		t.Fatalf("initial state = %q, want waiting_first_run", sync.State)
	}
	if sync.LastSyncAt.Valid {
		t.Errorf("last_sync_at should be NULL before the first run")
	}

	// Duplicate (channel_id, external_channel_url) violates the UNIQUE constraint.
	if _, err := q.CreateChannelSync(ctx, sqlcgen.CreateChannelSyncParams{
		ChannelID: channelID, UserID: userID, ExternalChannelUrl: "https://youtube.com/@example",
	}); err == nil {
		t.Fatal("duplicate create succeeded, want a unique violation")
	} else if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23505" {
		t.Fatalf("duplicate err = %v, want SQLSTATE 23505", err)
	}

	// List + count.
	list, err := q.ListChannelSyncsByUser(ctx, sqlcgen.ListChannelSyncsByUserParams{UserID: userID, ResultLimit: 100})
	if err != nil || len(list) != 1 || list[0].ID != sync.ID {
		t.Fatalf("ListChannelSyncsByUser = %+v, %v", list, err)
	}
	if n, err := q.CountChannelSyncsByUser(ctx, userID); err != nil || n != 1 {
		t.Fatalf("CountChannelSyncsByUser = %d, %v", n, err)
	}

	// A fresh sync (next_run_at defaults to now()) is due: the claim flips it to
	// 'syncing' and returns it.
	claimed, err := q.ClaimDueChannelSyncs(ctx, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != sync.ID {
		t.Fatalf("ClaimDueChannelSyncs = %+v, %v", claimed, err)
	}
	after, err := q.GetChannelSyncByID(ctx, sync.ID)
	if err != nil || after.State != "syncing" {
		t.Fatalf("state after claim = %q (%v), want syncing", after.State, err)
	}
	// A second claim finds nothing (already syncing, excluded from the due set).
	if again, err := q.ClaimDueChannelSyncs(ctx, 10); err != nil || len(again) != 0 {
		t.Fatalf("second claim = %+v, %v, want empty", again, err)
	}

	// Seen ledger: first insert of an id affects 1 row, the repeat affects 0.
	if rows, err := q.InsertChannelSyncSeen(ctx, sqlcgen.InsertChannelSyncSeenParams{SyncID: sync.ID, ExternalID: "vid-1"}); err != nil || rows != 1 {
		t.Fatalf("first seen insert = %d, %v, want 1", rows, err)
	}
	if rows, err := q.InsertChannelSyncSeen(ctx, sqlcgen.InsertChannelSyncSeenParams{SyncID: sync.ID, ExternalID: "vid-1"}); err != nil || rows != 0 {
		t.Fatalf("repeat seen insert = %d, %v, want 0 (dedupe)", rows, err)
	}

	// Finish: idle, last_sync_at stamped, rescheduled into the future.
	next := time.Now().UTC().Add(time.Hour)
	if err := q.FinishChannelSync(ctx, sqlcgen.FinishChannelSyncParams{ID: sync.ID, NextRunAt: next}); err != nil {
		t.Fatalf("FinishChannelSync: %v", err)
	}
	fin, _ := q.GetChannelSyncByID(ctx, sync.ID)
	if fin.State != "idle" || !fin.LastSyncAt.Valid || fin.LastError != "" {
		t.Fatalf("after finish = %+v, want idle + last_sync_at + no error", fin)
	}

	// Fail: failed state with a safe reason retained.
	if err := q.FailChannelSync(ctx, sqlcgen.FailChannelSyncParams{ID: sync.ID, LastError: "could not list the external channel", NextRunAt: next}); err != nil {
		t.Fatalf("FailChannelSync: %v", err)
	}
	fail, _ := q.GetChannelSyncByID(ctx, sync.ID)
	if fail.State != "failed" || fail.LastError != "could not list the external channel" {
		t.Fatalf("after fail = %+v, want failed + last_error", fail)
	}

	// Deleting the channel cascades to the sync and its seen ledger.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	if _, err := q.GetChannelSyncByID(ctx, sync.ID); err == nil {
		t.Error("channel sync survived its channel's deletion, want cascade")
	}
	var seenCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM channel_sync_seen WHERE sync_id = $1`, sync.ID).Scan(&seenCount); err != nil {
		t.Fatalf("count seen: %v", err)
	}
	if seenCount != 0 {
		t.Errorf("seen ledger rows after cascade = %d, want 0", seenCount)
	}
}
