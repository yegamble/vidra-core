package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type videoRepository struct {
	db            *sqlx.DB
	tm            *TransactionManager
	schemaOnce    sync.Once
	hasChannelID  bool
	checkedSchema bool
}

func NewVideoRepository(db *sqlx.DB) usecase.VideoRepository {
	return &videoRepository{
		db: db,
		tm: NewTransactionManager(db),
	}
}

func (r *videoRepository) ensureSchemaChecked(ctx context.Context) {
	r.schemaOnce.Do(func() {
		var has bool
		const q = `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'videos'
			  AND column_name = 'channel_id'
		)`
		_ = r.db.QueryRowContext(ctx, q).Scan(&has)
		r.hasChannelID = has
		r.checkedSchema = true
	})
}

func scanVideoRow(rows *sql.Rows) (*domain.Video, error) {
	var v domain.Video
	var processedCIDsJSON, metadataJSON, outputPathsJSON []byte
	var tags pq.StringArray

	err := rows.Scan(
		&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
		&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
		&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
		&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
		&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &v.ThumbnailPath, &v.PreviewPath,
	)
	if err != nil {
		return nil, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
	}

	if len(processedCIDsJSON) > 0 {
		_ = json.Unmarshal(processedCIDsJSON, &v.ProcessedCIDs)
	}
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &v.Metadata)
	}
	if len(outputPathsJSON) > 0 {
		_ = json.Unmarshal(outputPathsJSON, &v.OutputPaths)
	}
	v.Tags = []string(tags)

	return &v, nil
}

func (r *videoRepository) Create(ctx context.Context, v *domain.Video) error {
	exec := GetExecutor(ctx, r.db)

	now := time.Now()
	if strings.TrimSpace(v.ID) == "" {
		v.ID = uuid.NewString()
	}
	if strings.TrimSpace(v.ThumbnailID) == "" {
		v.ThumbnailID = uuid.NewString()
	}
	if v.Privacy == "" {
		v.Privacy = domain.PrivacyPublic
	}
	if v.Status == "" {
		v.Status = domain.StatusQueued
	}
	if v.UploadDate.IsZero() {
		v.UploadDate = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = now
	}
	if v.ProcessedCIDs == nil {
		v.ProcessedCIDs = map[string]string{}
	}
	if v.OutputPaths == nil {
		v.OutputPaths = map[string]string{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}

	r.ensureSchemaChecked(ctx)

	var query string
	if r.hasChannelID {
		query = `
        INSERT INTO videos (
            id, thumbnail_id, title, description, duration, views,
            privacy, status, upload_date, user_id,
            channel_id,
            original_cid, processed_cids, thumbnail_cid,
            tags, category_id, language, file_size, mime_type, metadata,
            created_at, updated_at,
            output_paths, thumbnail_path, preview_path
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10,
            COALESCE($11::uuid, (
                SELECT c.id FROM channels c WHERE c.account_id = $10::uuid ORDER BY c.created_at ASC LIMIT 1
            )),
            $12, $13, $14,
            $15, $16, $17, $18, $19, $20,
            $21, $22,
            $23, $24, $25
        )`
	} else {
		query = `
        INSERT INTO videos (
            id, thumbnail_id, title, description, duration, views,
            privacy, status, upload_date, user_id,
            original_cid, processed_cids, thumbnail_cid,
            tags, category_id, language, file_size, mime_type, metadata,
            created_at, updated_at,
            output_paths, thumbnail_path, preview_path
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10,
            $11, $12, $13,
            $14, $15, $16, $17, $18, $19,
            $20, $21,
            $22, $23, $24
        )`
	}

	processedCIDsJSON, _ := json.Marshal(v.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(v.Metadata)
	outputPathsJSON, _ := json.Marshal(v.OutputPaths)

	var channelIDParam interface{}
	if v.ChannelID == uuid.Nil {
		channelIDParam = nil
	} else {
		channelIDParam = v.ChannelID
	}

	var err error
	if r.hasChannelID {
		_, err = exec.ExecContext(ctx, query,
			v.ID, v.ThumbnailID, v.Title, v.Description, v.Duration, v.Views,
			v.Privacy, v.Status, v.UploadDate, v.UserID,
			channelIDParam,
			v.OriginalCID, processedCIDsJSON, v.ThumbnailCID,
			pq.Array(v.Tags), v.CategoryID, v.Language, v.FileSize, v.MimeType, metadataJSON,
			v.CreatedAt, v.UpdatedAt,
			outputPathsJSON, v.ThumbnailPath, v.PreviewPath,
		)
	} else {
		_, err = exec.ExecContext(ctx, query,
			v.ID, v.ThumbnailID, v.Title, v.Description, v.Duration, v.Views,
			v.Privacy, v.Status, v.UploadDate, v.UserID,
			v.OriginalCID, processedCIDsJSON, v.ThumbnailCID,
			pq.Array(v.Tags), v.CategoryID, v.Language, v.FileSize, v.MimeType, metadataJSON,
			v.CreatedAt, v.UpdatedAt,
			outputPathsJSON, v.ThumbnailPath, v.PreviewPath,
		)
	}
	if err != nil {
		return domain.NewDomainError("CREATE_FAILED", fmt.Sprintf("Failed to create video: %v", err))
	}
	return nil
}

func (r *videoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	r.ensureSchemaChecked(ctx)

	var query string
	if r.hasChannelID {
		query = `
        SELECT v.id, v.thumbnail_id, v.title, v.description, v.duration, v.views,
               v.privacy, v.status, v.upload_date, v.user_id, v.channel_id,
               v.original_cid, v.processed_cids, v.thumbnail_cid,
               v.tags, v.category_id, v.language, v.file_size, v.mime_type, v.metadata,
               v.created_at, v.updated_at, v.output_paths, v.thumbnail_path, v.preview_path,
               COALESCE(v.s3_urls, '{}'::jsonb) as s3_urls,
               COALESCE(v.storage_tier, 'hot') as storage_tier,
               v.s3_migrated_at,
               COALESCE(v.local_deleted, false) as local_deleted,
               c.id, c.name, c.slug, c.description, c.icon, c.color, c.display_order, c.is_active
        FROM videos v
        LEFT JOIN video_categories c ON v.category_id = c.id
        WHERE v.id = $1`
	} else {
		query = `
        SELECT v.id, v.thumbnail_id, v.title, v.description, v.duration, v.views,
               v.privacy, v.status, v.upload_date, v.user_id, v.channel_id,
               v.original_cid, v.processed_cids, v.thumbnail_cid,
               v.tags, v.category_id, v.language, v.file_size, v.mime_type, v.metadata,
               v.created_at, v.updated_at, v.output_paths, v.thumbnail_path, v.preview_path,
               c.id, c.name, c.slug, c.description, c.icon, c.color, c.display_order, c.is_active
        FROM videos v
        LEFT JOIN video_categories c ON v.category_id = c.id
        WHERE v.id = $1`
	}

	var v domain.Video
	var processedCIDsJSON, metadataJSON, outputPathsJSON, s3URLsJSON []byte
	var tags pq.StringArray
	var category domain.VideoCategory
	var categoryName, categorySlug sql.NullString
	var categoryDesc, categoryIcon, categoryColor sql.NullString
	var categoryOrder sql.NullInt64
	var categoryActive sql.NullBool
	var thumbnailPath, previewPath sql.NullString

	var err error
	if r.hasChannelID {
		err = r.db.QueryRowContext(ctx, query, id).Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
			&s3URLsJSON, &v.StorageTier, &v.S3MigratedAt, &v.LocalDeleted,
			&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
		)
	} else {
		err = r.db.QueryRowContext(ctx, query, id).Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
			&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
		)
		v.StorageTier = "hot"
		v.LocalDeleted = false
	}

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		errStr := err.Error()
		if strings.Contains(errStr, "invalid input syntax for type uuid") ||
			strings.Contains(errStr, "invalid UUID") {
			return nil, domain.ErrNotFound
		}
		return nil, domain.NewDomainError("GET_FAILED", "Failed to get video")
	}

	if len(processedCIDsJSON) > 0 {
		_ = json.Unmarshal(processedCIDsJSON, &v.ProcessedCIDs)
	}
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &v.Metadata)
	}
	if len(outputPathsJSON) > 0 {
		_ = json.Unmarshal(outputPathsJSON, &v.OutputPaths)
	}
	if len(s3URLsJSON) > 0 {
		_ = json.Unmarshal(s3URLsJSON, &v.S3URLs)
	}
	v.Tags = []string(tags)

	if thumbnailPath.Valid {
		v.ThumbnailPath = thumbnailPath.String
	}
	if previewPath.Valid {
		v.PreviewPath = previewPath.String
	}

	if v.CategoryID != nil {
		category.ID = *v.CategoryID
		category.Name = categoryName.String
		category.Slug = categorySlug.String
		if categoryDesc.Valid {
			category.Description = &categoryDesc.String
		}
		if categoryIcon.Valid {
			category.Icon = &categoryIcon.String
		}
		if categoryColor.Valid {
			category.Color = &categoryColor.String
		}
		category.DisplayOrder = int(categoryOrder.Int64)
		category.IsActive = categoryActive.Bool
		v.Category = &category
	}

	return &v, nil
}

func (r *videoRepository) Update(ctx context.Context, v *domain.Video) error {
	exec := GetExecutor(ctx, r.db)

	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = time.Now()
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if v.StorageTier == "" {
		v.StorageTier = "hot"
	}

	var s3URLsJSON []byte
	var err error
	if v.S3URLs == nil {
		s3URLsJSON = []byte("{}")
	} else {
		s3URLsJSON, err = json.Marshal(v.S3URLs)
		if err != nil {
			return domain.NewDomainError("JSON_MARSHAL_FAILED", "Failed to marshal S3 URLs")
		}
	}

	query := `
        UPDATE videos SET
            title = $2, description = $3, privacy = $4,
            tags = $5, category_id = $6, language = $7,
            status = $8, updated_at = $9,
            s3_urls = $11, storage_tier = $12,
            s3_migrated_at = $13, local_deleted = $14
        WHERE id = $1 AND user_id = $10`

	result, err := exec.ExecContext(ctx, query,
		v.ID, v.Title, v.Description, v.Privacy,
		pq.Array(v.Tags), v.CategoryID, v.Language,
		v.Status, v.UpdatedAt, v.UserID,
		s3URLsJSON, v.StorageTier, v.S3MigratedAt, v.LocalDeleted,
	)
	if err != nil {
		return domain.NewDomainError("UPDATE_FAILED", "Failed to update video")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return domain.NewDomainError("ROWS_AFFECTED_FAILED", "Failed to get rows affected")
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found or unauthorized")
	}

	return nil
}

func (r *videoRepository) Delete(ctx context.Context, id string, userID string) error {
	exec := GetExecutor(ctx, r.db)

	query := `DELETE FROM videos WHERE id = $1 AND user_id = $2`

	result, err := exec.ExecContext(ctx, query, id, userID)
	if err != nil {
		return domain.NewDomainError("DELETE_FAILED", "Failed to delete video")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return domain.NewDomainError("ROWS_AFFECTED_FAILED", "Failed to get rows affected")
	}
	if rowsAffected == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found or unauthorized")
	}

	return nil
}

// GetVideoQuotaUsed returns the total bytes used by a user's videos via SUM(file_size).
func (r *videoRepository) GetVideoQuotaUsed(ctx context.Context, userID string) (int64, error) {
	exec := GetExecutor(ctx, r.db)

	var total int64
	query := `SELECT COALESCE(SUM(file_size), 0) FROM videos WHERE user_id = $1`
	if err := exec.QueryRowContext(ctx, query, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("computing video quota for user %s: %w", userID, err)
	}
	return total, nil
}
