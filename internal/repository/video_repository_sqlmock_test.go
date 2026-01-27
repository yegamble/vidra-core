package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
)

func TestVideoRepository_GetByID_ChannelID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewVideoRepository(sqlxDB)

	videoID := uuid.New().String()
	channelID := uuid.New()
	userID := uuid.New().String()

	// Mock rows matching NEW implementation (with channel_id)
	rows := sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
		"s3_urls", "storage_tier", "s3_migrated_at", "local_deleted",
		"channel_id", // Added channel_id
		"category_id_join", "name", "slug", "description", "icon", "color", "display_order", "is_active",
	}).AddRow(
		videoID, "thumb-1", "Test Video", "Desc", 120, 0,
		"public", "completed", time.Now(), userID,
		"cid-1", "{}", "thumb-cid",
		"{tag1}", nil, "en", 1000, "video/mp4", "{}",
		time.Now(), time.Now(), "{}", "tpath", "ppath",
		"{}", "hot", nil, false,
		channelID, // Provide the channelID
		nil, nil, nil, nil, nil, nil, nil, nil,
	)

	// We expect the query to be executed
	mock.ExpectQuery(`SELECT v.id, v.thumbnail_id, .* FROM videos v .*`).
		WithArgs(videoID).
		WillReturnRows(rows)

	ctx := context.Background()
	video, err := repo.GetByID(ctx, videoID)
	assert.NoError(t, err)
	assert.NotNil(t, video)

	// This should now PASS
	assert.Equal(t, channelID, video.ChannelID, "ChannelID should be populated")
}
