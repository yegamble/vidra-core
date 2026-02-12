package repository_test

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

// setupMockDB helper creates a mock DB and repository
func setupMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock, *repository.VideoAnalyticsRepository) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	t.Cleanup(func() {
		mockDB.Close()
	})

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	repo := repository.NewVideoAnalyticsRepository(sqlxDB)
	return sqlxDB, mock, repo
}

// TestUpsertActiveViewersBatch_QueryCount verifies that the batch method uses a single query
func TestUpsertActiveViewersBatch_QueryCount(t *testing.T) {
	_, mock, repo := setupMockDB(t)

	// Create a batch of viewers
	viewers := make([]*domain.ActiveViewer, 5)
	for i := 0; i < 5; i++ {
		viewers[i] = &domain.ActiveViewer{
			ID:            uuid.New(),
			VideoID:       uuid.New(),
			SessionID:     uuid.New().String(),
			UserID:        nil,
			LastHeartbeat: time.Now(),
			CreatedAt:     time.Now(),
		}
	}

	// Expect a single query (batch insert)
	// The query logic is yet to be implemented, but we expect it to start with INSERT INTO
	// and use placeholders.
	// Since we haven't implemented it yet, this expectation is what we WANT to see.
	// Once implemented, we will verify the regex matches the actual implementation.
	mock.ExpectExec("INSERT INTO video_active_viewers").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 5))

	// Call the batch method
	err := repo.UpsertActiveViewersBatch(context.Background(), viewers)
	assert.NoError(t, err)

	// Verify that expectations were met
	err = mock.ExpectationsWereMet()
	assert.NoError(t, err)
}
