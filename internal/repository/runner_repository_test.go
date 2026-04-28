package repository

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRunnerRepository_Lifecycle_FullFlow exercises the real SQL behind the
// runner subsystem against a live Postgres instance — proving the de-stub
// claim from B14. It covers:
//
//   - Generate a registration token
//   - Register a runner against that token (new fields: version, ip, capabilities)
//   - Create an assignment, transition through accepted → running → completed
//   - HealthMetrics aggregates correctly
//   - ListAssignments filters by state and runnerId, with correct totals
//   - Delete runner cascades the assignment row
//
// Skipped without Postgres (testutil.SetupTestDB handles the skip).
func TestRunnerRepository_Lifecycle_FullFlow(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewRunnerRepository(testDB.DB)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)
	ctx := context.Background()

	// ── Setup: a user, a video, and an encoding job to assign ────────────────
	user := createTestUser(t, userRepo, ctx, "lifecycle_admin", "lifecycle@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Lifecycle Test Video")
	job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)

	// ── Step 1: Generate a registration token ─────────────────────────────────
	userUUID, parseErr := uuid.Parse(user.ID)
	require.NoError(t, parseErr)
	regToken, err := repo.CreateRegistrationToken(ctx, &userUUID, nil)
	require.NoError(t, err)
	require.NotEmpty(t, regToken.Token)

	// ── Step 2: Register a runner with the new fields ────────────────────────
	runner, err := repo.RegisterRunner(ctx, domain.RegisterRunnerInput{
		RegistrationToken: regToken.Token,
		Name:              "lifecycle-runner",
		Description:       "integration test runner",
		RunnerVersion:     "1.4.2",
		IPAddress:         "203.0.113.42",
		Capabilities:      map[string]any{"ffmpeg": "6.0", "gpu": "nvidia"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, runner.Token)
	require.Equal(t, "1.4.2", runner.RunnerVersion)
	require.Equal(t, "203.0.113.42", runner.IPAddress)

	// Reload runner from DB to confirm capabilities round-trip JSONB.
	loaded, err := repo.GetRunner(ctx, runner.ID)
	require.NoError(t, err)
	require.Equal(t, "1.4.2", loaded.RunnerVersion)
	require.Equal(t, "203.0.113.42", loaded.IPAddress)
	require.Equal(t, "nvidia", loaded.Capabilities["gpu"])
	require.Equal(t, "6.0", loaded.Capabilities["ffmpeg"])

	// ── Step 3: Create an assignment ─────────────────────────────────────────
	assignment, err := repo.CreateAssignment(ctx, runner.ID, job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RemoteRunnerJobStateAssigned, assignment.State)

	// ── Step 4: Touch runner to mark it online ───────────────────────────────
	require.NoError(t, repo.TouchRunner(ctx, runner.ID))

	// ── Step 5: Transition to accepted then running ──────────────────────────
	now := time.Now().UTC()
	assignment.State = domain.RemoteRunnerJobStateAccepted
	assignment.AcceptedAt = &now
	require.NoError(t, repo.UpdateAssignment(ctx, assignment))

	assignment.State = domain.RemoteRunnerJobStateRunning
	assignment.Progress = 50
	require.NoError(t, repo.UpdateAssignment(ctx, assignment))

	// ── Step 6: HealthMetrics — should show 1 online, 1 in-flight ────────────
	health, err := repo.HealthMetrics(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, health.TotalRunners)
	require.Equal(t, 1, health.OnlineRunners)
	require.Equal(t, 0, health.OfflineRunners)
	require.Equal(t, 1, health.JobsInFlight)
	require.Equal(t, 0, health.JobsFailed24h)

	// ── Step 7: Filter assignments by state=running, runnerId ────────────────
	items, total, err := repo.ListAssignments(ctx, domain.ListAssignmentsOpts{
		State: []domain.RemoteRunnerJobState{domain.RemoteRunnerJobStateRunning},
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, assignment.ID, items[0].ID)

	items, total, err = repo.ListAssignments(ctx, domain.ListAssignmentsOpts{
		RunnerID: &runner.ID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)

	// Filter that excludes the only assignment — empty result, total=0.
	items, total, err = repo.ListAssignments(ctx, domain.ListAssignmentsOpts{
		State: []domain.RemoteRunnerJobState{domain.RemoteRunnerJobStateFailed},
	})
	require.NoError(t, err)
	require.Equal(t, 0, total)
	require.Empty(t, items)

	// ── Step 8: Complete the assignment ──────────────────────────────────────
	completedAt := time.Now().UTC()
	assignment.State = domain.RemoteRunnerJobStateCompleted
	assignment.Progress = 100
	assignment.CompletedAt = &completedAt
	require.NoError(t, repo.UpdateAssignment(ctx, assignment))

	// HealthMetrics should now show 0 in-flight, 1 completed (avg may be 0
	// because completed_at - accepted_at is sub-millisecond in tests).
	health, err = repo.HealthMetrics(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, health.JobsInFlight)

	// ── Step 9: Pagination — start=0, count=1 ────────────────────────────────
	items, total, err = repo.ListAssignments(ctx, domain.ListAssignmentsOpts{Start: 0, Count: 1})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)

	// ── Step 10: Delete runner cascades ──────────────────────────────────────
	require.NoError(t, repo.DeleteRunner(ctx, runner.ID))

	// Runner is gone, assignment is gone (CASCADE).
	_, err = repo.GetRunner(ctx, runner.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)

	items, total, err = repo.ListAssignments(ctx, domain.ListAssignmentsOpts{})
	require.NoError(t, err)
	require.Equal(t, 0, total)
	require.Empty(t, items)
}

// TestRunnerRepository_HealthMetrics_EmptyDB confirms the aggregate query
// returns all zeros against a clean schema (no division-by-zero, no nulls).
func TestRunnerRepository_HealthMetrics_EmptyDB(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewRunnerRepository(testDB.DB)
	health, err := repo.HealthMetrics(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, health.TotalRunners)
	require.Equal(t, 0, health.OnlineRunners)
	require.Equal(t, 0, health.OfflineRunners)
	require.Equal(t, 0, health.JobsInFlight)
	require.Equal(t, 0, health.JobsFailed24h)
	require.Equal(t, int64(0), health.AvgCompletionMs)
}

// TestRunnerRepository_ListAssignments_StatePagination covers the SQL filter
// build with multiple states + LIMIT/OFFSET against real Postgres.
func TestRunnerRepository_ListAssignments_StatePagination(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewRunnerRepository(testDB.DB)
	encodingRepo := NewEncodingRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)
	ctx := context.Background()

	user := createTestUser(t, userRepo, ctx, "pgnr_admin_"+uuid.New().String()[:8], "pgnr-"+uuid.New().String()[:8]+"@example.com")

	userUUID, parseErr := uuid.Parse(user.ID)
	require.NoError(t, parseErr)
	regToken, err := repo.CreateRegistrationToken(ctx, &userUUID, nil)
	require.NoError(t, err)
	runner, err := repo.RegisterRunner(ctx, domain.RegisterRunnerInput{
		RegistrationToken: regToken.Token,
		Name:              "pagination-runner",
	})
	require.NoError(t, err)

	// Three assignments in different states. The encoding_jobs table has a
	// unique-active-job-per-video constraint, so each assignment needs its own
	// video to avoid the conflict.
	states := []domain.RemoteRunnerJobState{
		domain.RemoteRunnerJobStateRunning,
		domain.RemoteRunnerJobStateFailed,
		domain.RemoteRunnerJobStateAborted,
	}
	for i, st := range states {
		video := createTestVideo(t, videoRepo, ctx, user.ID, "Pagination Test "+string(rune('A'+i)))
		job := createTestEncodingJob(t, encodingRepo, ctx, video.ID)
		assignment, err := repo.CreateAssignment(ctx, runner.ID, job.ID)
		require.NoError(t, err)
		assignment.State = st
		require.NoError(t, repo.UpdateAssignment(ctx, assignment))
	}

	// Filter to two states (failed + aborted). total should be 2.
	items, total, err := repo.ListAssignments(ctx, domain.ListAssignmentsOpts{
		State: []domain.RemoteRunnerJobState{
			domain.RemoteRunnerJobStateFailed,
			domain.RemoteRunnerJobStateAborted,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, items, 2)

	// Pagination over the full set: count=1 should return 1 row but total=3.
	items, total, err = repo.ListAssignments(ctx, domain.ListAssignmentsOpts{Start: 0, Count: 1})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, items, 1)

	// Cleanup.
	require.NoError(t, repo.DeleteRunner(ctx, runner.ID))
}
