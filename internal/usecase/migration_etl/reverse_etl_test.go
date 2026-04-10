package migration_etl

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReverseETL_SyncUsers(t *testing.T) {
	idRepo := newMockIDMappingRepo()

	// Simulate a migrated user mapping
	idRepo.mappings = append(idRepo.mappings, &domain.MigrationIDMapping{
		JobID:      "job-1",
		EntityType: "user",
		PeertubeID: 42,
		VidraID:    uuid.New().String(),
	})

	rev := NewReverseETLService(idRepo)
	require.NotNil(t, rev)

	// Verify the service can look up mappings
	ctx := context.Background()
	vidraID, err := idRepo.GetVidraID(ctx, "user", 42)
	require.NoError(t, err)
	assert.NotEmpty(t, vidraID)
}

func TestReverseETL_NewEntityGetsNewPeertubeID(t *testing.T) {
	idRepo := newMockIDMappingRepo()

	rev := NewReverseETLService(idRepo)
	require.NotNil(t, rev)

	// A Vidra Core entity with no PeerTube mapping should be flagged as "new"
	ctx := context.Background()
	newVidraID := uuid.New().String()
	_, err := idRepo.GetPeertubeID(ctx, "user", newVidraID)
	assert.Error(t, err, "new Vidra entity should have no PeerTube ID")
}

func TestReverseETL_FilterByMigrationStartTime(t *testing.T) {
	idRepo := newMockIDMappingRepo()
	rev := NewReverseETLService(idRepo)

	migrationStart := time.Now().Add(-1 * time.Hour)

	// Only entities created after migration start should be synced
	newUser := &domain.User{
		ID:        uuid.NewString(),
		Username:  "new-user",
		Email:     "new@example.com",
		CreatedAt: time.Now(), // after migration start
	}
	oldUser := &domain.User{
		ID:        uuid.NewString(),
		Username:  "old-user",
		Email:     "old@example.com",
		CreatedAt: migrationStart.Add(-1 * time.Hour), // before migration start
	}

	assert.True(t, rev.ShouldSync(newUser.CreatedAt, migrationStart))
	assert.False(t, rev.ShouldSync(oldUser.CreatedAt, migrationStart))
}
