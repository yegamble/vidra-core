package repository

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockIDMappingRepo(t *testing.T) (*MigrationIDMappingRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	sqlxDB := sqlx.NewDb(db, "postgres")
	return NewMigrationIDMappingRepository(sqlxDB), mock
}

func TestMigrationIDMapping_Upsert(t *testing.T) {
	tests := []struct {
		name    string
		mapping *domain.MigrationIDMapping
		wantErr bool
	}{
		{
			name: "upsert user mapping",
			mapping: &domain.MigrationIDMapping{
				JobID:      "job-123",
				EntityType: "user",
				PeertubeID: 42,
				VidraID:    "550e8400-e29b-41d4-a716-446655440000",
			},
			wantErr: false,
		},
		{
			name: "upsert channel mapping",
			mapping: &domain.MigrationIDMapping{
				JobID:      "job-123",
				EntityType: "channel",
				PeertubeID: 7,
				VidraID:    "660e8400-e29b-41d4-a716-446655440001",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newMockIDMappingRepo(t)

			mock.ExpectExec("INSERT INTO migration_id_mappings").
				WithArgs(tt.mapping.JobID, tt.mapping.EntityType, tt.mapping.PeertubeID, tt.mapping.VidraID).
				WillReturnResult(sqlmock.NewResult(0, 1))

			err := repo.Upsert(context.Background(), tt.mapping)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMigrationIDMapping_GetVidraID(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		peertubeID int
		wantID     string
		wantErr    bool
	}{
		{
			name:       "found user mapping",
			entityType: "user",
			peertubeID: 42,
			wantID:     "550e8400-e29b-41d4-a716-446655440000",
			wantErr:    false,
		},
		{
			name:       "not found",
			entityType: "user",
			peertubeID: 999,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, mock := newMockIDMappingRepo(t)

			if !tt.wantErr {
				rows := sqlmock.NewRows([]string{"vidra_id"}).AddRow(tt.wantID)
				mock.ExpectQuery("SELECT vidra_id FROM migration_id_mappings").
					WithArgs(tt.entityType, tt.peertubeID).
					WillReturnRows(rows)
			} else {
				mock.ExpectQuery("SELECT vidra_id FROM migration_id_mappings").
					WithArgs(tt.entityType, tt.peertubeID).
					WillReturnError(domain.ErrNotFound)
			}

			vidraID, err := repo.GetVidraID(context.Background(), tt.entityType, tt.peertubeID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantID, vidraID)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMigrationIDMapping_GetPeertubeID(t *testing.T) {
	repo, mock := newMockIDMappingRepo(t)

	rows := sqlmock.NewRows([]string{"peertube_id"}).AddRow(42)
	mock.ExpectQuery("SELECT peertube_id FROM migration_id_mappings").
		WithArgs("user", "550e8400-e29b-41d4-a716-446655440000").
		WillReturnRows(rows)

	ptID, err := repo.GetPeertubeID(context.Background(), "user", "550e8400-e29b-41d4-a716-446655440000")
	require.NoError(t, err)
	assert.Equal(t, 42, ptID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationIDMapping_ListByJobID(t *testing.T) {
	repo, mock := newMockIDMappingRepo(t)

	rows := sqlmock.NewRows([]string{"job_id", "entity_type", "peertube_id", "vidra_id", "created_at"}).
		AddRow("job-123", "user", 1, "uuid-1", time.Now()).
		AddRow("job-123", "user", 2, "uuid-2", time.Now()).
		AddRow("job-123", "channel", 10, "uuid-10", time.Now())

	mock.ExpectQuery("SELECT (.+) FROM migration_id_mappings WHERE job_id").
		WithArgs("job-123").
		WillReturnRows(rows)

	mappings, err := repo.ListByJobID(context.Background(), "job-123")
	require.NoError(t, err)
	assert.Len(t, mappings, 3)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationCheckpoint_Upsert(t *testing.T) {
	repo, mock := newMockIDMappingRepo(t)

	mock.ExpectExec("INSERT INTO migration_checkpoints").
		WithArgs("job-123", "users").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpsertCheckpoint(context.Background(), "job-123", "users")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrationCheckpoint_GetCompletedPhases(t *testing.T) {
	repo, mock := newMockIDMappingRepo(t)

	rows := sqlmock.NewRows([]string{"entity_type"}).
		AddRow("users").
		AddRow("channels")

	mock.ExpectQuery("SELECT entity_type FROM migration_checkpoints").
		WithArgs("job-123").
		WillReturnRows(rows)

	phases, err := repo.GetCompletedPhases(context.Background(), "job-123")
	require.NoError(t, err)
	assert.Equal(t, []string{"users", "channels"}, phases)
	assert.NoError(t, mock.ExpectationsWereMet())
}
