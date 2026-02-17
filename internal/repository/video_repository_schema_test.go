package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupVideoSchemaMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestVideoRepository_GetByID_UsesFullQueryWhenHasChannelID(t *testing.T) {
	db, mock := setupVideoSchemaMockDB(t)
	repo := &videoRepository{db: db, tm: NewTransactionManager(db)}

	repo.schemaOnce.Do(func() {
		repo.hasChannelID = true
		repo.checkedSchema = true
	})

	ctx := context.Background()
	videoID := "550e8400-e29b-41d4-a716-446655440000"

	mock.ExpectQuery(`s3_urls`).WillReturnError(sqlmock.ErrCancelled)

	_, err := repo.GetByID(ctx, videoID)
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoRepository_GetByID_NoFallbackWhenHasNoChannelID(t *testing.T) {
	db, mock := setupVideoSchemaMockDB(t)
	repo := &videoRepository{db: db, tm: NewTransactionManager(db)}

	repo.schemaOnce.Do(func() {
		repo.hasChannelID = false
		repo.checkedSchema = true
	})

	ctx := context.Background()
	videoID := "550e8400-e29b-41d4-a716-446655440000"

	mock.ExpectQuery("SELECT").WillReturnError(sqlmock.ErrCancelled)

	_, err := repo.GetByID(ctx, videoID)
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}
