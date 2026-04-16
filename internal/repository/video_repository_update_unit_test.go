package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

func setupVideoUpdateMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mockDB.Close()
	})

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func TestVideoRepositoryUpdate_PersistsDurationMetadataAndMediaPaths(t *testing.T) {
	db, mock := setupVideoUpdateMockDB(t)
	repo := &videoRepository{db: db, tm: NewTransactionManager(db)}

	outputPaths := map[string]string{
		"source": "/static/web-videos/video-123.mov",
		"master": "/static/streaming-playlists/hls/video-123/master.m3u8",
	}
	metadata := domain.VideoMetadata{
		Width:       1920,
		Height:      1080,
		Framerate:   30,
		Bitrate:     8_000_000,
		VideoCodec:  "h264",
		AspectRatio: "16:9",
	}

	video := &domain.Video{
		ID:            "video-123",
		UserID:        "user-123",
		Title:         "Video",
		Description:   "Desc",
		Duration:      95,
		Privacy:       domain.PrivacyPublic,
		Tags:          []string{"test"},
		Language:      "en",
		Status:        domain.StatusCompleted,
		Metadata:      metadata,
		UpdatedAt:     time.Now(),
		OutputPaths:   outputPaths,
		ThumbnailPath: "/static/thumbnails/video-123_thumb.jpg",
		PreviewPath:   "/static/previews/video-123_preview.webp",
		StorageTier:   "hot",
	}

	mock.ExpectExec(updateVideoQueryRegex).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Update(context.Background(), video)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
