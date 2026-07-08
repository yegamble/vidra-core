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

// seedVideoRow seeds a user → channel → video FK chain and returns the video id
// plus a cleanup that cascades the whole chain via the users ON DELETE CASCADE.
func seedVideoRow(t *testing.T, st *Store) (uuid.UUID, func()) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"upl-"+suffix, "upl-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	cleanup := func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Upl') RETURNING id`,
		userID, "upl_"+suffix,
	).Scan(&channelID); err != nil {
		cleanup()
		t.Fatalf("seed channel: %v", err)
	}
	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'v', 'private', 'draft') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		cleanup()
		t.Fatalf("seed video: %v", err)
	}
	return videoID, cleanup
}

// TestUploadSessionQueriesPersist exercises the resumable-upload queries against
// real PostgreSQL: create, chunk upsert idempotency + ordering, state
// transition, expiry sweep selection, and the ON DELETE CASCADE from session to
// chunks.
func TestUploadSessionQueriesPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	videoID, cleanup := seedVideoRow(t, st)
	defer cleanup()

	// user_id = the video's owner; look it up.
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`SELECT c.owner_id FROM videos v JOIN channels c ON c.id = v.channel_id WHERE v.id = $1`, videoID,
	).Scan(&userID); err != nil {
		t.Fatalf("owner lookup: %v", err)
	}

	sess, err := q.CreateUploadSession(ctx, sqlcgen.CreateUploadSessionParams{
		VideoID:   videoID,
		UserID:    userID,
		Filename:  "clip.mp4",
		TotalSize: 40,
		ChunkSize: 16,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateUploadSession: %v", err)
	}
	if sess.State != "active" {
		t.Fatalf("new session state = %q, want active", sess.State)
	}

	got, err := q.GetUploadSession(ctx, sess.ID)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("GetUploadSession: %v", err)
	}

	// Upsert chunks out of order, then re-PUT chunk 0 with a corrected size — the
	// idempotent upsert must NOT create a duplicate row.
	for _, c := range []struct {
		n    int32
		size int64
	}{{2, 8}, {0, 99}, {1, 16}, {0, 16}} {
		if err := q.UpsertUploadChunk(ctx, sqlcgen.UpsertUploadChunkParams{UploadID: sess.ID, N: c.n, SizeBytes: c.size}); err != nil {
			t.Fatalf("UpsertUploadChunk %d: %v", c.n, err)
		}
	}
	chunks, err := q.ListUploadChunks(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListUploadChunks: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3 (re-PUT must not duplicate)", len(chunks))
	}
	if chunks[0].N != 0 || chunks[0].SizeBytes != 16 || chunks[1].N != 1 || chunks[2].N != 2 {
		t.Fatalf("chunks = %+v, want ascending [0:16 1:16 2:8]", chunks)
	}

	// State transition.
	if err := q.SetUploadSessionState(ctx, sqlcgen.SetUploadSessionStateParams{ID: sess.ID, State: "cancelled"}); err != nil {
		t.Fatalf("SetUploadSessionState: %v", err)
	}
	// A cancelled session is sweepable regardless of expiry.
	ids, err := q.ListSweepableUploadSessions(ctx, 50)
	if err != nil {
		t.Fatalf("ListSweepableUploadSessions: %v", err)
	}
	if !containsID(ids, sess.ID) {
		t.Fatalf("cancelled session not returned by sweep scan")
	}

	// Delete cascades to chunks.
	if err := q.DeleteUploadSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteUploadSession: %v", err)
	}
	if _, err := q.GetUploadSession(ctx, sess.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("session still present after delete: %v", err)
	}
	if left, _ := q.ListUploadChunks(ctx, sess.ID); len(left) != 0 {
		t.Fatalf("chunks not cascade-deleted: %d remain", len(left))
	}
}

// TestImportJobQueuePersists exercises the async URL-import queue against real
// PostgreSQL: single-active enqueue (partial unique index), claim → complete,
// re-enqueue after a finished job, and the reschedule/fail transitions.
func TestImportJobQueuePersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	videoID, cleanup := seedVideoRow(t, st)
	defer cleanup()

	job, err := q.EnqueueImportJob(ctx, sqlcgen.EnqueueImportJobParams{VideoID: videoID, Url: "https://example.com/a.mp4", Resolver: "direct"})
	if err != nil {
		t.Fatalf("EnqueueImportJob: %v", err)
	}
	if job.State != "pending" {
		t.Fatalf("new job state = %q, want pending", job.State)
	}

	// Single active per video: a second enqueue while pending is a no-op (no row).
	if _, err := q.EnqueueImportJob(ctx, sqlcgen.EnqueueImportJobParams{VideoID: videoID, Url: "https://example.com/b.mp4", Resolver: "direct"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second enqueue err = %v, want pgx.ErrNoRows (single active)", err)
	}

	// Claim (→ running) then complete.
	claimed, err := q.ClaimDueImportJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDueImportJobs: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claimed = %+v, want the one job", claimed)
	}
	if err := q.CompleteImportJob(ctx, job.ID); err != nil {
		t.Fatalf("CompleteImportJob: %v", err)
	}
	latest, err := q.GetLatestImportJobByVideo(ctx, videoID)
	if err != nil || latest.State != "done" {
		t.Fatalf("latest after complete = %q (%v), want done", latest.State, err)
	}

	// A finished job no longer blocks a re-import (single-active only covers
	// pending/running) → a fresh enqueue succeeds.
	reimport, err := q.EnqueueImportJob(ctx, sqlcgen.EnqueueImportJobParams{VideoID: videoID, Url: "https://example.com/c.mp4", Resolver: "direct"})
	if err != nil {
		t.Fatalf("re-enqueue after done: %v", err)
	}
	// Reschedule (retry) then fail (dead-letter) transitions.
	if err := q.RescheduleImportJob(ctx, sqlcgen.RescheduleImportJobParams{ID: reimport.ID, NextAttemptAt: time.Now().Add(time.Minute), Error: "could not fetch the URL"}); err != nil {
		t.Fatalf("RescheduleImportJob: %v", err)
	}
	if err := q.FailImportJob(ctx, sqlcgen.FailImportJobParams{ID: reimport.ID, Error: "could not fetch the URL"}); err != nil {
		t.Fatalf("FailImportJob: %v", err)
	}
	final, err := q.GetLatestImportJobByVideo(ctx, videoID)
	if err != nil {
		t.Fatalf("GetLatestImportJobByVideo: %v", err)
	}
	if final.State != "failed" || final.Attempts != 2 || final.Error == "" {
		t.Fatalf("final job = state:%q attempts:%d error:%q, want failed/2/non-empty", final.State, final.Attempts, final.Error)
	}
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
