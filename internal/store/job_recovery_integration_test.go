//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedRunningTranscodeJob creates the FK chain a transcode job needs and leaves
// one job in the exact state an OOM kill leaves behind: 'running', with an
// attempt already counted.
func seedRunningTranscodeJob(t *testing.T, st *Store) (jobID uuid.UUID, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	_, channelID, cleanupChain := seedUserChannel(t, st)

	var videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title) VALUES ($1, 'recovery') RETURNING id`,
		channelID,
	).Scan(&videoID); err != nil {
		cleanupChain()
		t.Fatalf("seed video: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO transcode_jobs (video_id, source_key, state, attempts)
		 VALUES ($1, 'originals/x.mp4', 'running', 1) RETURNING id`,
		videoID,
	).Scan(&jobID); err != nil {
		cleanupChain()
		t.Fatalf("seed transcode job: %v", err)
	}
	return jobID, cleanupChain
}

// TestRequeueRunningTranscodeJobsRecoversStrandedWork proves the boot-time
// recovery statement against real PostgreSQL: a job stranded in 'running' by an
// unclean shutdown comes back as 'pending' with its attempt counter advanced.
//
// The attempt increment is the part worth proving. Without it a genuinely
// poisonous job that kills the process would be requeued unchanged on every
// boot, loop forever, and never reach the workers' dead-letter cap.
func TestRequeueRunningTranscodeJobsRecoversStrandedWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	jobID, cleanup := seedRunningTranscodeJob(t, st)
	defer cleanup()

	n, err := st.Queries().RequeueRunningTranscodeJobs(ctx)
	if err != nil {
		t.Fatalf("RequeueRunningTranscodeJobs: %v", err)
	}
	if n < 1 {
		t.Fatalf("requeued %d rows, want at least the seeded one", n)
	}

	var state string
	var attempts int32
	if err := st.Pool.QueryRow(ctx,
		`SELECT state, attempts FROM transcode_jobs WHERE id = $1`, jobID,
	).Scan(&state, &attempts); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "pending" {
		t.Errorf("state = %q, want pending — a stranded job must return to the queue", state)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 — the abandoned run must count so a poisonous job still dead-letters", attempts)
	}

	// The row is claimable again: this is what was actually broken, since
	// transcode_jobs_active_video_idx counted the stranded 'running' row as live
	// and made every re-enqueue a silent no-op.
	rows, err := st.Queries().ClaimDueTranscodeJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimDueTranscodeJobs: %v", err)
	}
	var claimed bool
	for _, r := range rows {
		if r.ID == jobID {
			claimed = true
		}
	}
	if !claimed {
		t.Error("the requeued job was not claimable by the worker")
	}
}

// TestRequeueSyncingChannelSyncsReturnsThemToIdle covers the one queue with a
// different vocabulary: channel_syncs claims with 'syncing' and has no attempt
// counter, so an interrupted sync must come back as 'idle' (retryable on the
// next cadence) rather than 'failed' (which would surface a last_error that
// never happened).
func TestRequeueSyncingChannelSyncsReturnsThemToIdle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	userID, channelID, cleanup := seedUserChannel(t, st)
	defer cleanup()

	var syncID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channel_syncs (channel_id, user_id, external_channel_url, state)
		 VALUES ($1, $2, 'https://youtube.com/@recovery', 'syncing') RETURNING id`,
		channelID, userID,
	).Scan(&syncID); err != nil {
		t.Fatalf("seed channel sync: %v", err)
	}

	if _, err := st.Queries().RequeueSyncingChannelSyncs(ctx); err != nil {
		t.Fatalf("RequeueSyncingChannelSyncs: %v", err)
	}

	var state, lastError string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state, last_error FROM channel_syncs WHERE id = $1`, syncID,
	).Scan(&state, &lastError); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "idle" {
		t.Errorf("state = %q, want idle", state)
	}
	if lastError != "" {
		t.Errorf("last_error = %q, want empty — recovery must not invent a failure reason", lastError)
	}
}
