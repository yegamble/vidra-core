//go:build integration

// Integration test: proves the transcode job queue + playlist/rendition
// bookkeeping (migration 0039) against a REAL PostgreSQL (migrations applied).
// Run via `make test-integration` (backend-integration CI).
package transcode_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/transcode"
)

// seedVideo creates the user→channel→video FK chain and returns the video id.
func seedVideo(ctx context.Context, t *testing.T, q *sqlcgen.Queries) uuid.UUID {
	t.Helper()
	suf := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username: "tc_" + suf, Email: "tc_" + suf + "@example.test", PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{OwnerID: u.ID, Handle: "tcch" + suf, DisplayName: "Ch"})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{ChannelID: ch.ID, Title: "t", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	return v.ID
}

func TestTranscodeQueuePersistsClaimRescheduleComplete(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	videoID := seedVideo(ctx, t, q)
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	enqueue := func() {
		t.Helper()
		if err := q.EnqueueTranscodeJob(ctx, sqlcgen.EnqueueTranscodeJobParams{VideoID: videoID, SourceKey: srcKey}); err != nil {
			t.Fatalf("EnqueueTranscodeJob: %v", err)
		}
	}
	enqueue()
	enqueue() // idempotent while a live job exists (partial unique + DO NOTHING)

	claim := func() *sqlcgen.ClaimDueTranscodeJobsRow {
		t.Helper()
		rows, err := q.ClaimDueTranscodeJobs(ctx, 100)
		if err != nil {
			t.Fatalf("ClaimDueTranscodeJobs: %v", err)
		}
		var found *sqlcgen.ClaimDueTranscodeJobsRow
		for i := range rows {
			if rows[i].VideoID == videoID {
				if found != nil {
					t.Fatal("duplicate live jobs claimed for one video (enqueue not idempotent)")
				}
				found = &rows[i]
			}
		}
		return found
	}

	row := claim()
	if row == nil {
		t.Fatal("enqueued job not returned as due")
	}
	if row.SourceKey != srcKey || row.Attempts != 0 {
		t.Fatalf("claimed row = %+v, want source %q attempts 0", row, srcKey)
	}
	// A claimed job is 'running': not claimable again.
	if claim() != nil {
		t.Error("claimed (running) job should not be claimable twice")
	}

	// Reschedule into the future → pending again but not due.
	if err := q.RescheduleTranscodeJob(ctx, sqlcgen.RescheduleTranscodeJobParams{
		ID: row.ID, NextAttemptAt: time.Now().Add(time.Hour), LastError: "boom",
	}); err != nil {
		t.Fatalf("RescheduleTranscodeJob: %v", err)
	}
	if claim() != nil {
		t.Error("rescheduled (future) job should not be due")
	}

	// Reschedule into the past → due again; then complete it.
	if err := q.RescheduleTranscodeJob(ctx, sqlcgen.RescheduleTranscodeJobParams{
		ID: row.ID, NextAttemptAt: time.Now().Add(-time.Minute), LastError: "boom",
	}); err != nil {
		t.Fatalf("RescheduleTranscodeJob: %v", err)
	}
	row = claim()
	if row == nil {
		t.Fatal("past-rescheduled job should be due")
	}
	if row.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 after two reschedules", row.Attempts)
	}
	if err := q.CompleteTranscodeJob(ctx, row.ID); err != nil {
		t.Fatalf("CompleteTranscodeJob: %v", err)
	}
	if claim() != nil {
		t.Error("completed job should not be claimable")
	}
	// With no live job left, a fresh enqueue is possible again.
	enqueue()
	if claim() == nil {
		t.Error("re-enqueue after completion should produce a due job")
	}
}

func TestStreamingPlaylistAndRenditionsPersist(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	videoID := seedVideo(ctx, t, q)
	prefix := "streaming-playlists/" + videoID.String()

	// Upsert pending → ready transitions on one row.
	if _, err := q.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
		VideoID: videoID, MasterKey: "", State: "pending",
	}); err != nil {
		t.Fatalf("UpsertStreamingPlaylist(pending): %v", err)
	}
	sp, err := q.UpsertStreamingPlaylist(ctx, sqlcgen.UpsertStreamingPlaylistParams{
		VideoID: videoID, MasterKey: prefix + "/master.m3u8", State: "ready",
	})
	if err != nil {
		t.Fatalf("UpsertStreamingPlaylist(ready): %v", err)
	}
	if sp.State != "ready" || sp.MasterKey != prefix+"/master.m3u8" {
		t.Fatalf("upserted playlist = %+v", sp)
	}
	got, err := q.GetStreamingPlaylist(ctx, videoID)
	if err != nil || got.State != "ready" {
		t.Fatalf("GetStreamingPlaylist = (%+v, %v)", got, err)
	}

	// Renditions: create two, list tallest-first, replace wholesale.
	for _, r := range []struct{ h, w int32 }{{480, 854}, {720, 1280}} {
		if _, err := q.CreateVideoRendition(ctx, sqlcgen.CreateVideoRenditionParams{
			VideoID: videoID, Height: r.h, Width: r.w, KeyPrefix: prefix + "/x",
		}); err != nil {
			t.Fatalf("CreateVideoRendition(%d): %v", r.h, err)
		}
	}
	rends, err := q.ListVideoRenditions(ctx, videoID)
	if err != nil || len(rends) != 2 {
		t.Fatalf("ListVideoRenditions = (%d rows, %v), want 2", len(rends), err)
	}
	if rends[0].Height != 720 || rends[1].Height != 480 {
		t.Errorf("renditions not tallest-first: %d, %d", rends[0].Height, rends[1].Height)
	}
	if err := q.DeleteVideoRenditions(ctx, videoID); err != nil {
		t.Fatalf("DeleteVideoRenditions: %v", err)
	}
	if rends, _ := q.ListVideoRenditions(ctx, videoID); len(rends) != 0 {
		t.Errorf("renditions after delete = %d, want 0", len(rends))
	}
}

// fakeGateTranscoder satisfies transcode.Transcoder for the runtime-gate
// integration test (never fails; the gate is what's under test).
type fakeGateTranscoder struct{}

func (fakeGateTranscoder) Transcode(_ context.Context, videoID uuid.UUID, _ string) (media.HLSResult, error) {
	return media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"}, nil
}

// TestTranscodeRuntimeGateFlipIntegration proves — against the REAL
// transcode_jobs queue (the job-run observability surface) — that the
// transcoding_enabled runtime gate takes effect between two enqueues on ONE
// service instance, without any reconstruction/restart (config-parity W10):
// gate off → no job row is written and nothing is claimed; gate on → the
// enqueue lands, the worker picks it up, and the playlist bookkeeping runs.
func TestTranscodeRuntimeGateFlipIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	enabled := false
	svc := transcode.NewService(q, fakeGateTranscoder{},
		transcode.WithEnabledFunc(func() bool { return enabled }),
		transcode.WithConcurrencyFunc(func() int64 { return 2 }),
	)

	// Enqueue while disabled: silent no-op — no row lands in transcode_jobs.
	offVid := seedVideo(ctx, t, q)
	if err := svc.Enqueue(ctx, offVid, "web-videos/"+offVid.String()+".mp4"); err != nil {
		t.Fatalf("Enqueue while disabled: %v", err)
	}
	rows, err := q.ClaimDueTranscodeJobs(ctx, 500)
	if err != nil {
		t.Fatalf("ClaimDueTranscodeJobs: %v", err)
	}
	for _, r := range rows {
		if r.VideoID == offVid {
			t.Fatal("a job row was written while transcoding_enabled was off")
		}
		// Foreign rows claimed by this probe: put them back untouched.
		_ = q.RescheduleTranscodeJob(ctx, sqlcgen.RescheduleTranscodeJobParams{
			ID: r.ID, NextAttemptAt: time.Now(), LastError: "",
		})
	}

	// Runtime flip on the SAME instance: enqueue lands and the worker runs it.
	enabled = true
	onVid := seedVideo(ctx, t, q)
	if err := svc.Enqueue(ctx, onVid, "web-videos/"+onVid.String()+".mp4"); err != nil {
		t.Fatalf("Enqueue while enabled: %v", err)
	}
	if n, err := svc.DrainJobs(ctx, 500); err != nil || n < 1 {
		t.Fatalf("DrainJobs after enable = (%d, %v), want >= 1 completion", n, err)
	}
	if sp, ok := svc.Playlist(ctx, onVid); !ok || sp.State != transcode.PlaylistReady {
		t.Fatalf("playlist after gated drain = (%+v, %v), want ready", sp, ok)
	}

	// Gate off again: DrainJobs claims nothing (a pending backlog would wait).
	enabled = false
	if n, err := svc.DrainJobs(ctx, 500); err != nil || n != 0 {
		t.Fatalf("DrainJobs while disabled = (%d, %v), want (0, nil)", n, err)
	}
}
