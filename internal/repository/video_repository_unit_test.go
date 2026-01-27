package repository

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoRepository_EnsureSchemaChecked_Unit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewVideoRepository(sqlxDB).(*videoRepository)
	ctx := context.Background()

	t.Run("channel_id column exists", func(t *testing.T) {
		// Reset sync.Once and state for subtest
		repo = NewVideoRepository(sqlxDB).(*videoRepository)

		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		repo.ensureSchemaChecked(ctx)

		assert.True(t, repo.hasChannelID)
		assert.True(t, repo.checkedSchema)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("channel_id column does not exist", func(t *testing.T) {
		// Reset sync.Once and state for subtest
		repo = NewVideoRepository(sqlxDB).(*videoRepository)

		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		repo.ensureSchemaChecked(ctx)

		assert.False(t, repo.hasChannelID)
		assert.True(t, repo.checkedSchema)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Create_LegacySchema_Unit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewVideoRepository(sqlxDB).(*videoRepository)
	ctx := context.Background()

	// Mock the schema check to return false (legacy schema)
	mock.ExpectQuery("SELECT EXISTS").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	video := &domain.Video{
		ID:            uuid.New().String(),
		ThumbnailID:   uuid.New().String(),
		Title:         "Legacy Video",
		Description:   "Created with legacy schema",
		Duration:      120, // int
		Views:         0,
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusQueued,
		UploadDate:    time.Now(),
		UserID:        uuid.New().String(),
		OriginalCID:   "cid1",
		ProcessedCIDs: map[string]string{"720p": "cid2"},
		ThumbnailCID:  "cid3",
		Tags:          []string{"test"},
		CategoryID:    nil, // Testing nullable handling
		Language:      "en",
		FileSize:      1024,
		MimeType:      "video/mp4",
		Metadata: domain.VideoMetadata{
			Width:  1920,
			Height: 1080,
		},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		OutputPaths:   map[string]string{"hls": "path/to/hls"},
		ThumbnailPath: "path/to/thumb",
		PreviewPath:   "path/to/preview",
	}

	// Expect INSERT without channel_id
	// Note: We use regex for the query matching.
	// The key is that the VALUES count matches the query for legacy schema.
	mock.ExpectExec("INSERT INTO videos").
		WithArgs(
			video.ID, video.ThumbnailID, video.Title, video.Description, video.Duration, video.Views,
			video.Privacy, video.Status, video.UploadDate, video.UserID,
			video.OriginalCID, anyJson(), video.ThumbnailCID,
			pq.Array(video.Tags), video.CategoryID, video.Language, video.FileSize, video.MimeType, anyJson(),
			video.CreatedAt, video.UpdatedAt,
			anyJson(), video.ThumbnailPath, video.PreviewPath,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.Create(ctx, video)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoRepository_GetByID_Fallback_Unit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewVideoRepository(sqlxDB).(*videoRepository)
	ctx := context.Background()

	videoID := uuid.New().String()

	// First query fails with "column does not exist"
	// The query includes s3_urls, storage_tier, etc.
	mock.ExpectQuery("SELECT v.id.*s3_urls").
		WithArgs(videoID).
		WillReturnError(errors.New("pq: column v.s3_urls does not exist"))

	// Fallback query (simpler) succeeds
	rows := sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
		"category_id_alias", "name", "slug", "description", "icon", "color", "display_order", "is_active",
	}).AddRow(
		videoID, uuid.New().String(), "Fallback Video", "Description", 100, 0,
		domain.PrivacyPublic, domain.StatusCompleted, time.Now(), uuid.New().String(),
		"cid1", "{}", "cid3",
		pq.Array([]string{"test"}), nil, "en", 1024, "video/mp4", "{}",
		time.Now(), time.Now(), "{}", "path", "path",
		nil, nil, nil, nil, nil, nil, nil, nil,
	)

	mock.ExpectQuery("SELECT v.id.*FROM videos v").
		WithArgs(videoID).
		WillReturnRows(rows)

	v, err := repo.GetByID(ctx, videoID)
	assert.NoError(t, err)
	assert.NotNil(t, v)
	assert.Equal(t, "Fallback Video", v.Title)
	assert.Equal(t, "hot", v.StorageTier) // Default value verified
	assert.False(t, v.LocalDeleted)       // Default value verified

	assert.NoError(t, mock.ExpectationsWereMet())
}

// Helper to match JSON arguments
type anyJsonMatcher struct{}

func (a anyJsonMatcher) Match(v driver.Value) bool {
	_, ok := v.([]byte)
	return ok
}

func anyJson() anyJsonMatcher {
	return anyJsonMatcher{}
}
