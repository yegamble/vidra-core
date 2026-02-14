package repository

import (
	"context"
	"encoding/json"
	"regexp"
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

func setupVideoMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	return sqlxDB, mock
}

func TestVideoRepository_Unit_Create(t *testing.T) {
	db, mock := setupVideoMockDB(t)
	defer db.Close()

	repo := NewVideoRepository(db)
	ctx := context.Background()

	now := time.Now()
	videoID := uuid.New().String()
	userID := uuid.New().String()
	channelID := uuid.New()

	video := &domain.Video{
		ID:            videoID,
		ThumbnailID:   uuid.New().String(),
		Title:         "Test Video",
		Description:   "Test Description",
		Duration:      120,
		Views:         0,
		Privacy:       "public",
		Status:        "completed",
		UploadDate:    now,
		UserID:        userID,
		ChannelID:     channelID,
		OriginalCID:   "cid1",
		ProcessedCIDs: map[string]string{"720p": "cid2"},
		ThumbnailCID:  "cid3",
		Tags:          []string{"test", "video"},
		CategoryID:    nil,
		Language:      "en",
		FileSize:      1024,
		MimeType:      "video/mp4",
		Metadata:      domain.VideoMetadata{Width: 1920, Height: 1080},
		CreatedAt:     now,
		UpdatedAt:     now,
		OutputPaths:   map[string]string{"hls": "/path"},
		ThumbnailPath: "/thumb.jpg",
		PreviewPath:   "/preview.jpg",
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'videos'
			  AND column_name = 'channel_id'
		)`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	processedCIDsJSON, _ := json.Marshal(video.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(video.Metadata)
	outputPathsJSON, _ := json.Marshal(video.OutputPaths)

	// Note: We use a regex for the query matching because whitespace might differ
	query := `INSERT INTO videos`

	mock.ExpectExec(regexp.QuoteMeta(query)).
		WithArgs(
			video.ID, video.ThumbnailID, video.Title, video.Description, video.Duration, video.Views,
			video.Privacy, video.Status, video.UploadDate, video.UserID,
			video.ChannelID,
			video.OriginalCID, processedCIDsJSON, video.ThumbnailCID,
			pq.Array(video.Tags), video.CategoryID, video.Language, video.FileSize, video.MimeType, metadataJSON,
			video.CreatedAt, video.UpdatedAt,
			outputPathsJSON, video.ThumbnailPath, video.PreviewPath,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Create(ctx, video)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoRepository_Unit_GetByID(t *testing.T) {
	db, mock := setupVideoMockDB(t)
	defer db.Close()

	repo := NewVideoRepository(db)
	ctx := context.Background()

	videoID := uuid.New().String()
	userID := uuid.New().String()
	categoryID := uuid.New()
	now := time.Now()

	expectedVideo := &domain.Video{
		ID:            videoID,
		ThumbnailID:   uuid.New().String(),
		Title:         "Fetched Video",
		Description:   "Fetched Description",
		Duration:      300,
		Views:         100,
		Privacy:       "public",
		Status:        "completed",
		UploadDate:    now,
		UserID:        userID,
		OriginalCID:   "orig-cid",
		ProcessedCIDs: map[string]string{"1080p": "cid-1080"},
		ThumbnailCID:  "thumb-cid",
		Tags:          []string{"go", "testing"},
		CategoryID:    &categoryID,
		Language:      "en",
		FileSize:      5000,
		MimeType:      "video/mp4",
		Metadata:      domain.VideoMetadata{Width: 1920, Height: 1080},
		CreatedAt:     now,
		UpdatedAt:     now,
		OutputPaths:   map[string]string{"hls": "/out/hls"},
		ThumbnailPath: "/out/thumb.jpg",
		PreviewPath:   "/out/preview.jpg",
		StorageTier:   "hot",
		S3URLs:        map[string]string{"hls": "s3://bucket/key"},
		LocalDeleted:  false,
		Category: &domain.VideoCategory{
			ID:           categoryID,
			Name:         "Tech",
			Slug:         "tech",
			DisplayOrder: 1,
			IsActive:     true,
		},
	}

	processedCIDsJSON, _ := json.Marshal(expectedVideo.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(expectedVideo.Metadata)
	outputPathsJSON, _ := json.Marshal(expectedVideo.OutputPaths)
	s3URLsJSON, _ := json.Marshal(expectedVideo.S3URLs)

	rows := sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id", "channel_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
		"s3_urls", "storage_tier", "s3_migrated_at", "local_deleted",
		"cat_id", "cat_name", "cat_slug", "cat_desc", "cat_icon", "cat_color", "cat_order", "cat_active",
	}).AddRow(
		expectedVideo.ID, expectedVideo.ThumbnailID, expectedVideo.Title, expectedVideo.Description, expectedVideo.Duration, expectedVideo.Views,
		expectedVideo.Privacy, expectedVideo.Status, expectedVideo.UploadDate, expectedVideo.UserID, uuid.New(),
		expectedVideo.OriginalCID, processedCIDsJSON, expectedVideo.ThumbnailCID,
		pq.Array(expectedVideo.Tags), expectedVideo.CategoryID, expectedVideo.Language, expectedVideo.FileSize, expectedVideo.MimeType, metadataJSON,
		expectedVideo.CreatedAt, expectedVideo.UpdatedAt, outputPathsJSON, expectedVideo.ThumbnailPath, expectedVideo.PreviewPath,
		s3URLsJSON, expectedVideo.StorageTier, nil, expectedVideo.LocalDeleted,
		expectedVideo.Category.ID, expectedVideo.Category.Name, expectedVideo.Category.Slug, nil, nil, nil, expectedVideo.Category.DisplayOrder, expectedVideo.Category.IsActive,
	)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT v.id, v.thumbnail_id, v.title`)).
		WithArgs(videoID).
		WillReturnRows(rows)

	result, err := repo.GetByID(ctx, videoID)
	require.NoError(t, err)
	assert.Equal(t, expectedVideo.ID, result.ID)
	assert.Equal(t, expectedVideo.Title, result.Title)
	assert.Equal(t, expectedVideo.ProcessedCIDs, result.ProcessedCIDs)
	assert.Equal(t, expectedVideo.Category.Name, result.Category.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVideoRepository_Unit_Count(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
		wantCount int64
		wantErr   bool
	}{
		{
			name: "success - zero videos",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`)).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name: "success - multiple videos",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`)).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
			},
			wantCount: 42,
			wantErr:   false,
		},
		{
			name: "success - large count",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`)).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1000000))
			},
			wantCount: 1000000,
			wantErr:   false,
		},
		{
			name: "database connection error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`)).
					WillReturnError(assert.AnError)
			},
			wantCount: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := setupVideoMockDB(t)
			defer db.Close()

			repo := NewVideoRepository(db)

			tt.setupMock(mock)

			count, err := repo.Count(ctx)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, int64(0), count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
