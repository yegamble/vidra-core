// Package integration contains the E2E integration test for the PeerTube migration ETL pipeline.
// Run with:
//
//	docker compose --profile test-integration up -d peertube-source-db postgres-integration
//	TEST_INTEGRATION=true go test ./tests/integration/ -run TestMigrationETL -v -timeout 120s
package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/database"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
	ucmigration "vidra-core/internal/usecase/migration_etl"
)

const (
	// Integration Postgres (Vidra Core target).
	vidraIntegrationDSN = "postgres://integration_user:integration_password@localhost:15432/vidra_integration?sslmode=disable"
	// PeerTube source Postgres (seeded with test data).
	peertubeSourceDSN = "postgres://peertube:peertube_password@localhost:15433/peertube_prod?sslmode=disable"
)

func TestMigrationETL(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") != "true" {
		t.Skip("set TEST_INTEGRATION=true to run integration tests")
	}

	ctx := context.Background()

	// -----------------------------------------------------------------------
	// 1. Connect to both databases
	// -----------------------------------------------------------------------
	vidraDB, err := sqlx.Connect("postgres", vidraIntegrationDSN)
	require.NoError(t, err, "failed to connect to Vidra Core integration DB")
	defer vidraDB.Close()

	sourceDB, err := sqlx.Connect("postgres", peertubeSourceDSN)
	require.NoError(t, err, "failed to connect to PeerTube source DB")
	defer sourceDB.Close()

	// Verify seed data is present.
	var userCount int
	err = sourceDB.Get(&userCount, `SELECT COUNT(*) FROM "user"`)
	require.NoError(t, err)
	require.Equal(t, 3, userCount, "peertube source should have 3 users")

	// -----------------------------------------------------------------------
	// 2. Apply Vidra Core migrations to the integration DB
	// -----------------------------------------------------------------------
	err = database.RunMigrations(ctx, vidraDB)
	require.NoError(t, err, "failed to apply Vidra Core migrations")

	// -----------------------------------------------------------------------
	// 3. Create ETLService with real repos pointing at integration DB
	// -----------------------------------------------------------------------
	migrationRepo := repository.NewMigrationRepository(vidraDB)
	userRepo := repository.NewUserRepository(vidraDB)
	channelRepo := repository.NewChannelRepository(vidraDB)
	commentRepo := repository.NewCommentRepository(vidraDB)
	playlistRepo := repository.NewPlaylistRepository(vidraDB)
	captionRepo := repository.NewCaptionRepository(vidraDB)
	videoRepo := repository.NewVideoRepository(vidraDB)

	etlService := ucmigration.NewETLService(
		migrationRepo,
		userRepo,
		channelRepo,
		commentRepo,
		playlistRepo,
		captionRepo,
		videoRepo,
		nil, // IDMappingRepository - not used in integration test
	)

	// -----------------------------------------------------------------------
	// 4. Create a migration job with source DB connection details
	// -----------------------------------------------------------------------
	adminUserID := "integration-test-admin"
	req := &domain.MigrationRequest{
		SourceHost:       "peertube.example.com",
		SourceDBHost:     "localhost",
		SourceDBPort:     15433,
		SourceDBName:     "peertube_prod",
		SourceDBUser:     "peertube",
		SourceDBPassword: "peertube_password",
	}

	job, err := etlService.StartMigration(ctx, adminUserID, req)
	require.NoError(t, err, "StartMigration should succeed")
	require.NotEmpty(t, job.ID)

	// -----------------------------------------------------------------------
	// 5. Poll until job completes (async pipeline)
	// -----------------------------------------------------------------------
	var finalJob *domain.MigrationJob
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		finalJob, err = etlService.GetMigrationStatus(ctx, job.ID)
		require.NoError(t, err)
		if finalJob.Status.IsTerminal() || finalJob.Status == domain.MigrationStatusCompleted {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NotNil(t, finalJob, "job should have been fetched")
	require.Equal(t, domain.MigrationStatusCompleted, finalJob.Status,
		"job should complete; error: %v", finalJob.ErrorMessage)

	// -----------------------------------------------------------------------
	// 6. Assert stats
	// -----------------------------------------------------------------------
	var stats domain.MigrationStats
	err = json.Unmarshal(finalJob.StatsJSON, &stats)
	require.NoError(t, err, "should unmarshal stats")

	// Users: 3 total, 3 migrated
	assert.Equal(t, 3, stats.Users.Total, "users total")
	assert.Equal(t, 3, stats.Users.Migrated, "users migrated")
	assert.Equal(t, 0, stats.Users.Failed, "users failed")

	// Channels: 2 total, 2 migrated
	assert.Equal(t, 2, stats.Channels.Total, "channels total")
	assert.Equal(t, 2, stats.Channels.Migrated, "channels migrated")
	assert.Equal(t, 0, stats.Channels.Failed, "channels failed")

	// Videos: 3 total (remote=false), 3 migrated
	assert.Equal(t, 3, stats.Videos.Total, "videos total")
	assert.Equal(t, 3, stats.Videos.Migrated, "videos migrated")
	assert.Equal(t, 0, stats.Videos.Failed, "videos failed")

	// Comments: 4 total (none deleted), 4 migrated
	assert.Equal(t, 4, stats.Comments.Total, "comments total")
	assert.Equal(t, 4, stats.Comments.Migrated, "comments migrated")
	assert.Equal(t, 0, stats.Comments.Failed, "comments failed")

	// Playlists: 1 total, 1 migrated
	assert.Equal(t, 1, stats.Playlists.Total, "playlists total")
	assert.Equal(t, 1, stats.Playlists.Migrated, "playlists migrated")
	assert.Equal(t, 0, stats.Playlists.Failed, "playlists failed")

	// Captions: 2 total, 2 migrated
	assert.Equal(t, 2, stats.Captions.Total, "captions total")
	assert.Equal(t, 2, stats.Captions.Migrated, "captions migrated")
	assert.Equal(t, 0, stats.Captions.Failed, "captions failed")

	// -----------------------------------------------------------------------
	// 7. Verify data in Vidra Core database
	// -----------------------------------------------------------------------

	// Users
	vidraUsers, err := userRepo.List(ctx, 100, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(vidraUsers), 3, "at least 3 users in Vidra Core DB")

	// Verify user roles
	roleMap := make(map[string]domain.UserRole)
	for _, u := range vidraUsers {
		roleMap[u.Username] = u.Role
	}
	assert.Equal(t, domain.RoleAdmin, roleMap["admin_user"], "admin should have admin role")
	assert.Equal(t, domain.RoleUser, roleMap["alice"], "alice should have user role")

	// Bob should be inactive (blocked in PeerTube)
	for _, u := range vidraUsers {
		if u.Username == "bob" {
			assert.False(t, u.IsActive, "bob should be inactive (was blocked)")
		}
	}

	// Video count
	videoCount, err := videoRepo.Count(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, videoCount, int64(3), "at least 3 videos in Vidra Core DB")

	// Comment threading: find replies and verify they have parent IDs
	var commentRows []struct {
		ID       string  `db:"id"`
		ParentID *string `db:"parent_id"`
		Body     string  `db:"body"`
	}
	err = vidraDB.Select(&commentRows, `SELECT id, parent_id, body FROM comments ORDER BY created_at`)
	require.NoError(t, err)

	topLevelCount := 0
	replyCount := 0
	for _, c := range commentRows {
		if c.ParentID == nil {
			topLevelCount++
		} else {
			replyCount++
		}
	}
	assert.Equal(t, 2, topLevelCount, "should have 2 top-level comments")
	assert.Equal(t, 2, replyCount, "should have 2 reply comments")

	t.Logf("Migration ETL E2E test passed: %d users, %d channels, %d videos, %d comments (%d top-level, %d replies), %d playlists, %d captions",
		stats.Users.Migrated, stats.Channels.Migrated, stats.Videos.Migrated,
		stats.Comments.Migrated, topLevelCount, replyCount,
		stats.Playlists.Migrated, stats.Captions.Migrated)
}
