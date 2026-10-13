//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type raceFixture struct {
	st                         *Store
	q                          *sqlcgen.Queries
	giver, giverCh, taker, tCh uuid.UUID
}

func newRaceFixture(ctx context.Context, t *testing.T) raceFixture {
	t.Helper()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	f := raceFixture{st: st, q: st.Queries()}
	f.giver, f.giverCh = seedOwnerChannel(ctx, t, st, "rcG")
	f.taker, f.tCh = seedOwnerChannel(ctx, t, st, "rcT")
	return f
}

// offer seeds a published video of the giver's with a stored original and a
// pending request to the taker.
func (f raceFixture) offer(ctx context.Context, t *testing.T) (video, change uuid.UUID) {
	t.Helper()
	video = seedPublishedVideo(ctx, t, f.st, f.giverCh, "raced")
	if _, err := f.st.Pool.Exec(ctx, `INSERT INTO video_files (video_id, kind, storage_key, size_bytes) VALUES ($1, 'original', 'old-key', 1)`, video); err != nil {
		t.Fatal(err)
	}
	oc, err := f.q.CreateVideoOwnershipChange(ctx, sqlcgen.CreateVideoOwnershipChangeParams{
		VideoID: video, InitiatorID: f.giver, NextOwnerID: f.taker, VideoTitle: "raced"})
	if err != nil {
		t.Fatal(err)
	}
	return video, oc.ID
}

func (f raceFixture) acceptParams(change uuid.UUID) sqlcgen.AcceptVideoOwnershipChangeParams {
	return sqlcgen.AcceptVideoOwnershipChangeParams{ID: change, NextOwnerID: f.taker, ChannelID: f.tCh,
		ExpiredBefore: time.Now().Add(-30 * 24 * time.Hour)}
}

// heldAccept runs the accept the way OwnershipRepo does — lock the video row,
// then accept — but leaves the transaction open, so a racing statement can be
// shown to wait for it.
func (f raceFixture) heldAccept(ctx context.Context, t *testing.T, change uuid.UUID) pgx.Tx {
	t.Helper()
	tx, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := f.q.WithTx(tx)
	if _, err := q.LockOwnershipChangeVideo(ctx, change); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := q.AcceptVideoOwnershipChange(ctx, f.acceptParams(change)); err != nil {
		t.Fatalf("held accept: %v", err)
	}
	return tx
}

// blocked runs fn in the background and proves it is still waiting after a
// pause (it is queued behind a row lock), returning a channel for its result.
func blocked(t *testing.T, fn func() error) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		t.Fatalf("the racing statement did not wait for the lock (err=%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	return done
}

func wait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the racing statement never finished")
		return nil
	}
}

// TestReplaceSwapRacesAnAccept: a replacement that began under the giver is
// swapped in only while the giver still owns the video. An accept committed
// between the replacement's start and its swap — including one the swap had
// to wait for — makes the swap refuse; a swap that wins the lock lands first
// and the transfer follows it.
func TestReplaceSwapRacesAnAccept(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newRaceFixture(ctx, t)
	swap := func(video uuid.UUID) error {
		_, err := f.q.SwapVideoOriginalIfOwner(ctx, sqlcgen.SwapVideoOriginalIfOwnerParams{
			VideoID: video, OwnerID: f.giver, StorageKey: "new-key", SizeBytes: 2})
		return err
	}
	originalOf := func(video uuid.UUID) string {
		t.Helper()
		var key string
		if err := f.st.Pool.QueryRow(ctx, `SELECT storage_key FROM video_files WHERE video_id = $1 AND kind = 'original'`, video).Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}

	// Accept committed after the replacement began: the swap refuses.
	video, change := f.offer(ctx, t)
	if _, err := f.st.OwnershipRepository().AcceptVideoOwnershipChange(ctx, f.acceptParams(change)); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := swap(video); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("swap after the transfer: err = %v, want no row", err)
	}
	if got := originalOf(video); got != "old-key" {
		t.Errorf("original after a refused swap = %q", got)
	}

	// The swap waits for an accept in flight, then refuses.
	video, change = f.offer(ctx, t)
	tx := f.heldAccept(ctx, t, change)
	done := blocked(t, func() error { return swap(video) })
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("swap queued behind the accept: err = %v, want no row", err)
	}
	if got := originalOf(video); got != "old-key" {
		t.Errorf("original after a queued, refused swap = %q", got)
	}

	// A swap that holds the lock first lands; the accept waits and follows.
	video, change = f.offer(ctx, t)
	stx, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.WithTx(stx).SwapVideoOriginalIfOwner(ctx, sqlcgen.SwapVideoOriginalIfOwnerParams{
		VideoID: video, OwnerID: f.giver, StorageKey: "new-key", SizeBytes: 2}); err != nil {
		t.Fatalf("held swap: %v", err)
	}
	done = blocked(t, func() error {
		_, err := f.st.OwnershipRepository().AcceptVideoOwnershipChange(ctx, f.acceptParams(change))
		return err
	})
	if err := stx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); err != nil {
		t.Errorf("accept queued behind a finished swap: %v", err)
	}
	if got := originalOf(video); got != "new-key" {
		t.Errorf("original = %q, want the swapped-in replacement", got)
	}
}

// TestSessionOpenRacesAnAccept: opening an upload or replacement session and
// accepting a transfer of the same video serialise on the video row, so the
// outcome is a transfer with no session, or a session with no transfer —
// never both.
func TestSessionOpenRacesAnAccept(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newRaceFixture(ctx, t)
	openParams := func(video uuid.UUID) sqlcgen.CreateUploadSessionParams {
		return sqlcgen.CreateUploadSessionParams{VideoID: video, ChannelID: f.giverCh, UserID: f.giver,
			Filename: "r.mp4", TotalSize: 10, ChunkSize: 5, ExpiresAt: time.Now().Add(time.Hour), Purpose: "replace"}
	}
	state := func(video, change uuid.UUID) (channel uuid.UUID, sessions int, status string) {
		t.Helper()
		if err := f.st.Pool.QueryRow(ctx, `SELECT channel_id FROM videos WHERE id = $1`, video).Scan(&channel); err != nil {
			t.Fatal(err)
		}
		if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM upload_sessions WHERE video_id = $1`, video).Scan(&sessions); err != nil {
			t.Fatal(err)
		}
		if err := f.st.Pool.QueryRow(ctx, `SELECT status FROM video_ownership_changes WHERE id = $1`, change).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return
	}

	// The accept holds the lock: the session open waits, then sees the video's
	// new channel and opens nothing.
	video, change := f.offer(ctx, t)
	tx := f.heldAccept(ctx, t, change)
	done := blocked(t, func() error {
		_, err := f.q.CreateUploadSession(ctx, openParams(video))
		return err
	})
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("session open queued behind the accept: err = %v, want no row", err)
	}
	if ch, n, st := state(video, change); ch != f.tCh || n != 0 || st != "accepted" {
		t.Errorf("accept first: channel=%s sessions=%d status=%s, want a transfer and no session", ch, n, st)
	}

	// The session open holds the lock: the accept waits, then sees the open
	// session and transfers nothing.
	video, change = f.offer(ctx, t)
	stx, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.WithTx(stx).CreateUploadSession(ctx, openParams(video)); err != nil {
		t.Fatalf("held session open: %v", err)
	}
	done = blocked(t, func() error {
		_, err := f.st.OwnershipRepository().AcceptVideoOwnershipChange(ctx, f.acceptParams(change))
		return err
	})
	if err := stx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, done); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("accept queued behind a session open: err = %v, want no row", err)
	}
	if ch, n, st := state(video, change); ch != f.giverCh || n != 1 || st != "pending" {
		t.Errorf("session first: channel=%s sessions=%d status=%s, want a session and no transfer", ch, n, st)
	}
}
