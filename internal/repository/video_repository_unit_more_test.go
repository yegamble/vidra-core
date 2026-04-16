package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	selectVideoFieldsRegex      = `SELECT\s+id,\s+thumbnail_id,\s+title,\s+description,\s+duration,\s+views`
	selectVideoAliasRegex       = `SELECT\s+v\.id,\s+v\.thumbnail_id,\s+v\.title`
	selectChannelHydrationRegex = `(?s).*FROM channels c.*WHERE c\.id = ANY.*`
	updateVideoQueryRegex       = `UPDATE\s+videos\s+SET`
	insertVideoQueryRegex       = `INSERT\s+INTO\s+videos`
	deleteVideoQueryRegex       = `DELETE\s+FROM\s+videos\s+WHERE\s+id\s+=\s+\$1\s+AND\s+user_id\s+=\s+\$2`
	countByUserQueryRegex       = `SELECT\s+COUNT\(\*\)\s+FROM\s+videos\s+WHERE\s+user_id\s+=\s+\$1`
	countPublicVideoQueryRegex  = `(?s)SELECT COUNT\(\*\) FROM videos WHERE privacy = 'public'.*NOT EXISTS`
	countAllVideosQueryText     = `SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`
)

func newScanVideoRow(now time.Time, userID string, channelID uuid.UUID) *sqlmock.Rows {
	processedCIDsJSON, _ := json.Marshal(map[string]string{"720p": "cid-720"})
	metadataJSON, _ := json.Marshal(domain.VideoMetadata{Width: 1280, Height: 720})
	outputPathsJSON, _ := json.Marshal(map[string]string{"hls": "/out/hls"})

	return sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id", "channel_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
		"wait_transcoding",
	}).AddRow(
		uuid.New().String(), uuid.New().String(), "title", "desc", 123, int64(9),
		domain.PrivacyPublic, domain.StatusCompleted, now, userID, channelID.String(),
		"orig-cid", processedCIDsJSON, "thumb-cid",
		pq.Array([]string{"go", "test"}), nil, "en", int64(2048), "video/mp4", metadataJSON,
		now, now, outputPathsJSON, "/thumb.jpg", "/preview.jpg", false,
	)
}

func newVideoChannelRows(now time.Time, channelID uuid.UUID) *sqlmock.Rows {
	accountID := uuid.New()
	description := "Channel description"
	support := "Support"
	return sqlmock.NewRows([]string{
		"id", "account_id", "handle", "display_name", "description", "support",
		"is_local", "atproto_did", "atproto_pds_url",
		"avatar_filename", "avatar_ipfs_cid", "banner_filename", "banner_ipfs_cid",
		"followers_count", "following_count", "videos_count",
		"created_at", "updated_at",
	}).AddRow(
		channelID.String(), accountID.String(), "creator-handle", "Creator Display", description, support,
		true, nil, nil,
		nil, nil, nil, nil,
		42, 5, 7,
		now, now,
	)
}

func newMigrationVideoRow(now time.Time, userID string) *sqlmock.Rows {
	processedCIDsJSON, _ := json.Marshal(map[string]string{"1080p": "cid-1080"})
	metadataJSON, _ := json.Marshal(domain.VideoMetadata{Width: 1920, Height: 1080})
	outputPathsJSON, _ := json.Marshal(map[string]string{"hls": "/migr/hls"})
	s3URLsJSON, _ := json.Marshal(map[string]string{"hls": "s3://bucket/video.m3u8"})

	return sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views",
		"privacy", "status", "upload_date", "user_id",
		"original_cid", "processed_cids", "thumbnail_cid",
		"tags", "category_id", "language", "file_size", "mime_type", "metadata",
		"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
		"s3_urls", "storage_tier", "s3_migrated_at", "local_deleted",
	}).AddRow(
		uuid.New().String(), uuid.New().String(), "migrate", "migrate-desc", 240, int64(25),
		domain.PrivacyPublic, domain.StatusCompleted, now, userID,
		"orig-cid", processedCIDsJSON, "thumb-cid",
		pq.Array([]string{"migrate"}), nil, "en", int64(1024), "video/mp4", metadataJSON,
		now, now, outputPathsJSON, "/thumb.jpg", "/preview.jpg",
		s3URLsJSON, "hot", nil, false,
	)
}

func newRemoteVideoRow(now time.Time, userID string, remoteURI string) *sqlmock.Rows {
	processedCIDsJSON, _ := json.Marshal(map[string]string{"360p": "cid-360"})
	outputPathsJSON, _ := json.Marshal(map[string]string{"hls": "/remote/hls"})
	s3URLsJSON, _ := json.Marshal(map[string]string{})
	metadataJSON, _ := json.Marshal(domain.VideoMetadata{Width: 640, Height: 360})
	channelID := uuid.New()
	remoteActor := "https://remote.example/actors/u1"
	remoteVideoURL := "https://remote.example/videos/file.mp4"
	remoteDomain := "remote.example"
	remoteThumbnailURL := "https://remote.example/thumb.jpg"

	return sqlmock.NewRows([]string{
		"id", "thumbnail_id", "title", "description", "duration", "views", "privacy", "status",
		"upload_date", "user_id", "channel_id", "original_cid", "processed_cids", "thumbnail_cid",
		"output_paths", "s3_urls", "storage_tier", "s3_migrated_at", "local_deleted",
		"thumbnail_path", "preview_path", "tags", "category_id", "language",
		"file_size", "mime_type", "metadata",
		"is_remote", "remote_uri", "remote_actor_uri", "remote_video_url",
		"remote_instance_domain", "remote_thumbnail_url", "remote_last_synced_at",
		"created_at", "updated_at",
	}).AddRow(
		uuid.New().String(), uuid.New().String(), "remote-title", "remote-desc", 90, int64(3),
		domain.PrivacyPublic, domain.StatusCompleted, now, userID, channelID, "orig-cid", processedCIDsJSON, "thumb-cid",
		outputPathsJSON, s3URLsJSON, "hot", nil, false,
		"/thumb.jpg", "/preview.jpg", pq.Array([]string{"remote"}), nil, "en",
		int64(999), "video/mp4", metadataJSON,
		true, remoteURI, remoteActor, remoteVideoURL, remoteDomain, remoteThumbnailURL, now,
		now, now,
	)
}

func TestVideoRepository_Unit_Create_LegacySchemaAndError(t *testing.T) {
	t.Run("legacy schema insert", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()

		repo := NewVideoRepository(db)
		ctx := context.Background()
		now := time.Now()

		video := &domain.Video{
			ID:            uuid.New().String(),
			ThumbnailID:   uuid.New().String(),
			Title:         "legacy",
			Description:   "legacy-desc",
			Duration:      60,
			Views:         0,
			Privacy:       domain.PrivacyPublic,
			Status:        domain.StatusCompleted,
			UploadDate:    now,
			UserID:        uuid.New().String(),
			OriginalCID:   "cid-orig",
			ProcessedCIDs: map[string]string{"720p": "cid-720"},
			ThumbnailCID:  "cid-thumb",
			Tags:          []string{"legacy"},
			Language:      "en",
			FileSize:      500,
			MimeType:      "video/mp4",
			Metadata:      domain.VideoMetadata{Width: 640, Height: 360},
			CreatedAt:     now,
			UpdatedAt:     now,
			OutputPaths:   map[string]string{"hls": "/legacy/hls"},
			ThumbnailPath: "/thumb.jpg",
			PreviewPath:   "/preview.jpg",
		}

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'videos'
			  AND column_name = 'channel_id'
		)`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		processedCIDsJSON, _ := json.Marshal(video.ProcessedCIDs)
		metadataJSON, _ := json.Marshal(video.Metadata)
		outputPathsJSON, _ := json.Marshal(video.OutputPaths)

		mock.ExpectExec(insertVideoQueryRegex).
			WithArgs(
				video.ID, video.ThumbnailID, video.Title, video.Description, video.Duration, video.Views,
				video.Privacy, video.Status, video.UploadDate, video.UserID,
				video.OriginalCID, processedCIDsJSON, video.ThumbnailCID,
				pq.Array(video.Tags), video.CategoryID, video.Language, video.FileSize, video.MimeType, metadataJSON,
				video.CreatedAt, video.UpdatedAt,
				outputPathsJSON, video.ThumbnailPath, video.PreviewPath,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Create(ctx, video))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert failure returns domain error", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()

		repo := NewVideoRepository(db)
		ctx := context.Background()
		now := time.Now()

		video := &domain.Video{
			ID:          uuid.New().String(),
			ThumbnailID: uuid.New().String(),
			Title:       "error",
			Description: "error-desc",
			Duration:    60,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  now,
			UserID:      uuid.New().String(),
			ChannelID:   uuid.New(),
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'videos'
			  AND column_name = 'channel_id'
		)`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		mock.ExpectExec(insertVideoQueryRegex).WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, video)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "CREATE_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_GetByID_Branches(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		videoID := uuid.New().String()
		mock.ExpectQuery(selectVideoAliasRegex).WithArgs(videoID).WillReturnError(sql.ErrNoRows)

		video, err := repo.GetByID(context.Background(), videoID)
		require.Nil(t, video)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid uuid error maps to not found", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		videoID := "not-a-uuid"
		mock.ExpectQuery(selectVideoAliasRegex).WithArgs(videoID).
			WillReturnError(errors.New("invalid input syntax for type uuid"))

		video, err := repo.GetByID(context.Background(), videoID)
		require.Nil(t, video)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("simple query when hasChannelID is false", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := &videoRepository{db: db, tm: NewTransactionManager(db)}

		repo.schemaOnce.Do(func() {
			repo.hasChannelID = false
			repo.checkedSchema = true
		})

		videoID := uuid.New().String()
		userID := uuid.New().String()
		channelID := uuid.New()
		now := time.Now()
		processedCIDsJSON, _ := json.Marshal(map[string]string{"720p": "cid-720"})
		metadataJSON, _ := json.Marshal(domain.VideoMetadata{Width: 1280, Height: 720})
		outputPathsJSON, _ := json.Marshal(map[string]string{"hls": "/fallback/hls"})

		simpleRows := sqlmock.NewRows([]string{
			"id", "thumbnail_id", "title", "description", "duration", "views",
			"privacy", "status", "upload_date", "user_id", "channel_id",
			"original_cid", "processed_cids", "thumbnail_cid",
			"tags", "category_id", "language", "file_size", "mime_type", "metadata",
			"created_at", "updated_at", "output_paths", "thumbnail_path", "preview_path",
			"wait_transcoding",
			"cat_id", "cat_name", "cat_slug", "cat_desc", "cat_icon", "cat_color", "cat_order", "cat_active",
		}).AddRow(
			videoID, uuid.New().String(), "simple-title", "simple-desc", 140, int64(7),
			domain.PrivacyPublic, domain.StatusCompleted, now, userID, channelID.String(),
			"orig-cid", processedCIDsJSON, "thumb-cid",
			pq.Array([]string{"tag1"}), nil, "en", int64(4096), "video/mp4", metadataJSON,
			now, now, outputPathsJSON, "/thumb.jpg", "/preview.jpg", false,
			nil, nil, nil, nil, nil, nil, nil, nil,
		)

		mock.ExpectQuery(selectVideoAliasRegex).WithArgs(videoID).WillReturnRows(simpleRows)
		mock.ExpectQuery(selectChannelHydrationRegex).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(newVideoChannelRows(now, channelID))

		video, err := repo.GetByID(context.Background(), videoID)
		require.NoError(t, err)
		require.NotNil(t, video)
		assert.Equal(t, "simple-title", video.Title)
		require.NotNil(t, video.Channel)
		assert.Equal(t, "creator-handle", video.Channel.Handle)
		assert.Equal(t, "hot", video.StorageTier)
		assert.False(t, video.LocalDeleted)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_GetByUserID(t *testing.T) {
	t.Run("count failure", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		userID := uuid.New().String()
		mock.ExpectQuery(countByUserQueryRegex).WithArgs(userID).WillReturnError(errors.New("db down"))

		videos, total, err := repo.GetByUserID(context.Background(), userID, 10, 0)
		require.Nil(t, videos)
		require.Equal(t, int64(0), total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COUNT_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		userID := uuid.New().String()
		mock.ExpectQuery(countByUserQueryRegex).WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(selectVideoFieldsRegex).WithArgs(userID, 10, 0).WillReturnError(errors.New("query failed"))

		videos, total, err := repo.GetByUserID(context.Background(), userID, 10, 0)
		require.Nil(t, videos)
		require.Equal(t, int64(0), total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "QUERY_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		userID := uuid.New().String()
		now := time.Now()
		channelID := uuid.New()
		mock.ExpectQuery(countByUserQueryRegex).WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(selectVideoFieldsRegex).WithArgs(userID, 10, 0).
			WillReturnRows(newScanVideoRow(now, userID, channelID))
		mock.ExpectQuery(selectChannelHydrationRegex).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(newVideoChannelRows(now, channelID))

		videos, total, err := repo.GetByUserID(context.Background(), userID, 10, 0)
		require.NoError(t, err)
		require.Len(t, videos, 1)
		assert.Equal(t, int64(1), total)
		require.NotNil(t, videos[0].Channel)
		assert.Equal(t, "creator-handle", videos[0].Channel.Handle)
		assert.Equal(t, "Creator Display", videos[0].Channel.DisplayName)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_UpdateAndDelete(t *testing.T) {
	t.Run("update success", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		video := &domain.Video{
			ID:              uuid.New().String(),
			UserID:          uuid.New().String(),
			ChannelID:       uuid.New(),
			Title:           "updated-title",
			Description:     "updated-desc",
			Duration:        95,
			Privacy:         domain.PrivacyPublic,
			Tags:            []string{"go"},
			Language:        "en",
			Status:          domain.StatusCompleted,
			Metadata:        domain.VideoMetadata{Width: 1920, Height: 1080},
			UpdatedAt:       time.Now(),
			OutputPaths:     map[string]string{"source": "/static/web-videos/video.mp4"},
			ThumbnailPath:   "/thumb.jpg",
			PreviewPath:     "/preview.jpg",
			StorageTier:     "hot",
			LocalDeleted:    false,
			WaitTranscoding: true,
		}

		mock.ExpectExec(updateVideoQueryRegex).
			WithArgs(
				video.ID, video.Title, video.Description, video.Privacy,
				pq.Array(video.Tags), video.CategoryID, video.Language,
				video.Status, video.UpdatedAt, video.Duration, sqlmock.AnyArg(),
				sqlmock.AnyArg(), video.ThumbnailPath, video.PreviewPath, video.UserID,
				sqlmock.AnyArg(), video.StorageTier, video.S3MigratedAt, video.LocalDeleted, video.WaitTranscoding, video.ChannelID,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Update(context.Background(), video))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update not found", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		video := &domain.Video{ID: uuid.New().String(), UserID: uuid.New().String(), UpdatedAt: time.Now()}
		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(context.Background(), video)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VIDEO_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update rows affected error", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		video := &domain.Video{ID: uuid.New().String(), UserID: uuid.New().String(), UpdatedAt: time.Now()}
		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Update(context.Background(), video)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ROWS_AFFECTED_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete success", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		videoID := uuid.New().String()
		userID := uuid.New().String()
		mock.ExpectExec(deleteVideoQueryRegex).WithArgs(videoID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.Delete(context.Background(), videoID, userID))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete not found", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		videoID := uuid.New().String()
		userID := uuid.New().String()
		mock.ExpectExec(deleteVideoQueryRegex).WithArgs(videoID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(context.Background(), videoID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VIDEO_NOT_FOUND")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_ProcessingUpdates(t *testing.T) {
	t.Run("update processing info branches", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		videoID := uuid.New().String()
		output := map[string]string{"hls": "/hls/master.m3u8"}

		params := port.VideoProcessingParams{VideoID: videoID, Status: domain.StatusCompleted, OutputPaths: output, ThumbnailPath: "/thumb.jpg", PreviewPath: "/preview.jpg"}

		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UpdateProcessingInfo(context.Background(), params))

		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.UpdateProcessingInfo(context.Background(), params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VIDEO_NOT_FOUND")

		mock.ExpectExec(updateVideoQueryRegex).WillReturnError(errors.New("update failed"))
		err = repo.UpdateProcessingInfo(context.Background(), params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "UPDATE_PROCESSING_FAILED")

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update processing info with cids branches", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		videoID := uuid.New().String()
		output := map[string]string{"hls": "/hls/master.m3u8"}
		cids := map[string]string{"720p": "cid-720"}

		cidParams := port.VideoProcessingWithCIDsParams{
			VideoProcessingParams: port.VideoProcessingParams{VideoID: videoID, Status: domain.StatusCompleted, OutputPaths: output, ThumbnailPath: "/thumb.jpg", PreviewPath: "/preview.jpg"},
			ProcessedCIDs:         cids, ThumbnailCID: "thumb-cid", PreviewCID: "preview-cid",
		}

		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.UpdateProcessingInfoWithCIDs(context.Background(), cidParams))

		mock.ExpectExec(updateVideoQueryRegex).WillReturnResult(sqlmock.NewResult(0, 0))
		err := repo.UpdateProcessingInfoWithCIDs(context.Background(), cidParams)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "VIDEO_NOT_FOUND")

		mock.ExpectExec(updateVideoQueryRegex).WillReturnError(errors.New("update failed"))
		err = repo.UpdateProcessingInfoWithCIDs(context.Background(), cidParams)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "UPDATE_PROCESSING_FAILED")

		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_ListAndSearch(t *testing.T) {
	t.Run("list success with filters", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		now := time.Now()
		categoryID := uuid.New()
		channelID := uuid.New()

		req := &domain.VideoSearchRequest{
			CategoryID: &categoryID,
			Language:   "en",
			Sort:       "views",
			Order:      "asc",
			Limit:      10,
			Offset:     1,
		}

		mock.ExpectQuery(countPublicVideoQueryRegex).
			WithArgs(req.CategoryID, req.Language).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(selectVideoFieldsRegex).WillReturnRows(newScanVideoRow(now, uuid.New().String(), channelID))
		mock.ExpectQuery(selectChannelHydrationRegex).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(newVideoChannelRows(now, channelID))

		videos, total, err := repo.List(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, videos, 1)
		assert.Equal(t, int64(1), total)
		require.NotNil(t, videos[0].Channel)
		assert.Equal(t, "creator-handle", videos[0].Channel.Handle)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list query failure", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		mock.ExpectQuery(countPublicVideoQueryRegex).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(selectVideoFieldsRegex).WillReturnError(errors.New("query failed"))

		videos, total, err := repo.List(context.Background(), &domain.VideoSearchRequest{})
		require.Nil(t, videos)
		assert.Equal(t, int64(0), total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "QUERY_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("search success with relevance and filters", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		now := time.Now()
		categoryID := uuid.New()
		channelID := uuid.New()

		req := &domain.VideoSearchRequest{
			Query:      "federation",
			Tags:       []string{"go", "activitypub"},
			CategoryID: &categoryID,
			Language:   "en",
			Sort:       "relevance",
			Order:      "asc",
			Limit:      5,
			Offset:     2,
		}

		mock.ExpectQuery(countPublicVideoQueryRegex).
			WithArgs(req.Query, "%"+req.Query+"%", "%"+req.Query+"%", sqlmock.AnyArg(), req.CategoryID, req.Language).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery(selectVideoFieldsRegex).WillReturnRows(newScanVideoRow(now, uuid.New().String(), channelID))
		mock.ExpectQuery(selectChannelHydrationRegex).
			WithArgs(sqlmock.AnyArg()).
			WillReturnRows(newVideoChannelRows(now, channelID))

		videos, total, err := repo.Search(context.Background(), req)
		require.NoError(t, err)
		require.Len(t, videos, 1)
		assert.Equal(t, int64(1), total)
		require.NotNil(t, videos[0].Channel)
		assert.Equal(t, "Creator Display", videos[0].Channel.DisplayName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("search count failure", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		mock.ExpectQuery(countPublicVideoQueryRegex).
			WillReturnError(errors.New("count failed"))

		videos, total, err := repo.Search(context.Background(), &domain.VideoSearchRequest{Query: "q"})
		require.Nil(t, videos)
		assert.Equal(t, int64(0), total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COUNT_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoRepository_Unit_MigrationRemoteAndCount(t *testing.T) {
	t.Run("videos for migration success and query failure", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		now := time.Now()
		userID := uuid.New().String()

		mock.ExpectQuery(selectVideoFieldsRegex).WithArgs(10).WillReturnRows(newMigrationVideoRow(now, userID))
		videos, err := repo.GetVideosForMigration(context.Background(), 10)
		require.NoError(t, err)
		require.Len(t, videos, 1)

		mock.ExpectQuery(selectVideoFieldsRegex).WithArgs(10).WillReturnError(errors.New("query failed"))
		videos, err = repo.GetVideosForMigration(context.Background(), 10)
		require.Nil(t, videos)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "QUERY_FAILED")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get by remote uri no rows and success", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		now := time.Now()
		userID := uuid.New().String()
		remoteURI := "https://remote.example/videos/abc"

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT
			id, thumbnail_id, title, description, duration, views, privacy, status`)).
			WithArgs(remoteURI).
			WillReturnError(sql.ErrNoRows)

		video, err := repo.GetByRemoteURI(context.Background(), remoteURI)
		require.NoError(t, err)
		require.Nil(t, video)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT
			id, thumbnail_id, title, description, duration, views, privacy, status`)).
			WithArgs(remoteURI).
			WillReturnRows(newRemoteVideoRow(now, userID, remoteURI))

		video, err = repo.GetByRemoteURI(context.Background(), remoteURI)
		require.NoError(t, err)
		require.NotNil(t, video)
		assert.True(t, video.IsRemote)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create remote video success and error", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)
		now := time.Now()
		remoteURI := "https://remote.example/videos/xyz"
		remoteActor := "https://remote.example/actors/alice"
		remoteVideoURL := "https://cdn.remote.example/videos/xyz.mp4"
		remoteDomain := "remote.example"
		remoteThumb := "https://remote.example/thumb.jpg"

		video := &domain.Video{
			ID:                   uuid.New().String(),
			Title:                "remote-create",
			Description:          "remote-create-desc",
			Duration:             100,
			Privacy:              domain.PrivacyPublic,
			Status:               domain.StatusCompleted,
			UploadDate:           now,
			Tags:                 []string{"remote", "create"},
			Language:             "en",
			FileSize:             12345,
			Metadata:             domain.VideoMetadata{Width: 1280, Height: 720},
			IsRemote:             true,
			RemoteURI:            &remoteURI,
			RemoteActorURI:       &remoteActor,
			RemoteVideoURL:       &remoteVideoURL,
			RemoteInstanceDomain: &remoteDomain,
			RemoteThumbnailURL:   &remoteThumb,
			RemoteLastSyncedAt:   &now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}

		mock.ExpectExec(insertVideoQueryRegex).WillReturnResult(sqlmock.NewResult(1, 1))
		require.NoError(t, repo.CreateRemoteVideo(context.Background(), video))

		mock.ExpectExec(insertVideoQueryRegex).WillReturnError(errors.New("insert failed"))
		require.Error(t, repo.CreateRemoteVideo(context.Background(), video))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count success and error", func(t *testing.T) {
		db, mock := setupVideoMockDB(t)
		defer db.Close()
		repo := NewVideoRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(countAllVideosQueryText)).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
		count, err := repo.Count(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(42), count)

		mock.ExpectQuery(regexp.QuoteMeta(countAllVideosQueryText)).
			WillReturnError(errors.New("count failed"))
		count, err = repo.Count(context.Background())
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
