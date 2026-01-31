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

// ensureSchemaChecked detects whether the current schema has a channel_id column
// on the videos table. It runs once per repository instance and caches the result
// for subsequent calls to avoid repeated information_schema lookups.
// Thread-safe using sync.Once.
func (r *videoRepository) ensureSchemaChecked(ctx context.Context) {
	r.schemaOnce.Do(func() {
		var has bool
		const q = `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'videos'
			  AND column_name = 'channel_id'
		)`
		// If query fails for any reason, default to false (legacy schema)
		_ = r.db.QueryRowContext(ctx, q).Scan(&has)
		r.hasChannelID = has
		r.checkedSchema = true
	})
}

// scanVideoRow is a helper function to scan a video row and reduce duplication
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

	// Unmarshal JSON fields
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
	// Get executor (either transaction from context or DB)
	exec := GetExecutor(ctx, r.db)

	// Backward-compatible defaults for older callers/tests that provide minimal fields.
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

	// Detect schema and choose compatible insert
	r.ensureSchemaChecked(ctx)

	var query string
	if r.hasChannelID {
		// New schema: channel_id is NOT NULL; populate from provided value or default channel
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
		// Legacy schema: no channel_id column yet
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

	// Marshal JSON fields
	processedCIDsJSON, _ := json.Marshal(v.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(v.Metadata)
	outputPathsJSON, _ := json.Marshal(v.OutputPaths)

	// If ChannelID is unset (zero UUID), pass NULL so COALESCE uses default channel
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
	query := `
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

	var v domain.Video
	var processedCIDsJSON, metadataJSON, outputPathsJSON, s3URLsJSON []byte
	var tags pq.StringArray
	var category domain.VideoCategory
	var categoryName, categorySlug sql.NullString
	var categoryDesc, categoryIcon, categoryColor sql.NullString
	var categoryOrder sql.NullInt64
	var categoryActive sql.NullBool
	var thumbnailPath, previewPath sql.NullString

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
		&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
		&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
		&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
		&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
		&s3URLsJSON, &v.StorageTier, &v.S3MigratedAt, &v.LocalDeleted,
		&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		// Also treat invalid UUID errors as "not found"
		errStr := err.Error()
		if strings.Contains(errStr, "invalid input syntax for type uuid") ||
			strings.Contains(errStr, "invalid UUID") {
			return nil, domain.ErrNotFound
		}

		// Handle missing columns gracefully - use simpler query without S3 fields
		if strings.Contains(errStr, "column") && strings.Contains(errStr, "does not exist") {
			// Try simpler query without S3 migration fields
			simpleQuery := `
				SELECT v.id, v.thumbnail_id, v.title, v.description, v.duration, v.views,
					   v.privacy, v.status, v.upload_date, v.user_id, v.channel_id,
					   v.original_cid, v.processed_cids, v.thumbnail_cid,
					   v.tags, v.category_id, v.language, v.file_size, v.mime_type, v.metadata,
					   v.created_at, v.updated_at, v.output_paths, v.thumbnail_path, v.preview_path,
					   c.id, c.name, c.slug, c.description, c.icon, c.color, c.display_order, c.is_active
				FROM videos v
				LEFT JOIN video_categories c ON v.category_id = c.id
				WHERE v.id = $1`

			err = r.db.QueryRowContext(ctx, simpleQuery, id).Scan(
				&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
				&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
				&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
				&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
				&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
				&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
			)

			if err != nil {
				if err == sql.ErrNoRows {
					return nil, domain.ErrNotFound
				}
				return nil, domain.NewDomainError("GET_FAILED", "Failed to get video")
			}

			// Set default values for missing S3 fields
			v.StorageTier = "hot"
			v.LocalDeleted = false
			// Continue to unmarshal JSON fields below
		} else {
			return nil, domain.NewDomainError("GET_FAILED", "Failed to get video")
		}
	}

	// Unmarshal JSON fields
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

	// Assign nullable string fields
	if thumbnailPath.Valid {
		v.ThumbnailPath = thumbnailPath.String
	}
	if previewPath.Valid {
		v.PreviewPath = previewPath.String
	}

	// Populate category if it exists
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

func (r *videoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	if len(ids) == 0 {
		return []*domain.Video{}, nil
	}

	query := `
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
        WHERE v.id = ANY($1)`

	// Try full query first
	rows, err := r.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		// Handle missing columns gracefully - try simpler query
		errStr := err.Error()
		if strings.Contains(errStr, "column") && strings.Contains(errStr, "does not exist") {
			simpleQuery := `
				SELECT v.id, v.thumbnail_id, v.title, v.description, v.duration, v.views,
					   v.privacy, v.status, v.upload_date, v.user_id, v.channel_id,
					   v.original_cid, v.processed_cids, v.thumbnail_cid,
					   v.tags, v.category_id, v.language, v.file_size, v.mime_type, v.metadata,
					   v.created_at, v.updated_at, v.output_paths, v.thumbnail_path, v.preview_path,
					   c.id, c.name, c.slug, c.description, c.icon, c.color, c.display_order, c.is_active
				FROM videos v
				LEFT JOIN video_categories c ON v.category_id = c.id
				WHERE v.id = ANY($1)`

			rows, err = r.db.QueryContext(ctx, simpleQuery, pq.Array(ids))
			if err != nil {
				return nil, domain.NewDomainError("QUERY_FAILED", "Failed to get videos by IDs")
			}
		} else {
			return nil, domain.NewDomainError("QUERY_FAILED", "Failed to get videos by IDs")
		}
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		var v domain.Video
		var processedCIDsJSON, metadataJSON, outputPathsJSON, s3URLsJSON []byte
		var tags pq.StringArray
		var category domain.VideoCategory
		var categoryName, categorySlug sql.NullString
		var categoryDesc, categoryIcon, categoryColor sql.NullString
		var categoryOrder sql.NullInt64
		var categoryActive sql.NullBool
		var thumbnailPath, previewPath sql.NullString

		// Attempt to scan assuming full query columns
		// We need to know which query ran.
		// Actually, standard sql.Rows.Scan requires matching column count.
		// The simple query returns fewer columns.

		columns, _ := rows.Columns()
		isSimple := len(columns) < 36 // Full query has 36 columns (approx)

		var err error
		if !isSimple {
			err = rows.Scan(
				&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
				&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
				&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
				&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
				&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
				&s3URLsJSON, &v.StorageTier, &v.S3MigratedAt, &v.LocalDeleted,
				&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
			)
		} else {
			err = rows.Scan(
				&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
				&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
				&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
				&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
				&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &thumbnailPath, &previewPath,
				&v.CategoryID, &categoryName, &categorySlug, &categoryDesc, &categoryIcon, &categoryColor, &categoryOrder, &categoryActive,
			)
			// Set defaults for missing fields
			v.StorageTier = "hot"
			v.LocalDeleted = false
		}

		if err != nil {
			return nil, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
		}

		// Unmarshal JSON fields
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

		// Assign nullable string fields
		if thumbnailPath.Valid {
			v.ThumbnailPath = thumbnailPath.String
		}
		if previewPath.Valid {
			v.PreviewPath = previewPath.String
		}

		// Populate category if it exists
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

		videos = append(videos, &v)
	}

	return videos, nil
}

func (r *videoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	// Get total count
	countQuery := `SELECT COUNT(*) FROM videos WHERE user_id = $1`
	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, userID).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count user videos")
	}

	// Get videos
	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path
        FROM videos
        WHERE user_id = $1
        ORDER BY upload_date DESC
        LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, domain.NewDomainError("QUERY_FAILED", "Failed to get videos by user id")
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		v, err := scanVideoRow(rows)
		if err != nil {
			return nil, 0, err
		}
		videos = append(videos, v)
	}

	return videos, total, nil
}

func (r *videoRepository) Update(ctx context.Context, v *domain.Video) error {
	// Get executor (either transaction from context or DB)
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

	// Marshal S3 URLs, ensuring we use empty object instead of null for nil maps
	var s3URLsJSON []byte
	var err error
	if v.S3URLs == nil {
		// Use empty JSON object to satisfy NOT NULL constraint
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
	// Get executor (either transaction from context or DB)
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

func (r *videoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	query := `
        UPDATE videos SET
            status = $2,
            output_paths = $3,
            thumbnail_path = $4,
            preview_path = $5,
            updated_at = NOW()
        WHERE id = $1`

	outJSON, _ := json.Marshal(outputPaths)
	result, err := r.db.ExecContext(ctx, query, videoID, status, outJSON, thumbnailPath, previewPath)
	if err != nil {
		return domain.NewDomainError("UPDATE_PROCESSING_FAILED", "Failed to update processing info")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found")
	}
	return nil
}

func (r *videoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	query := `
        UPDATE videos SET
            status = $2,
            output_paths = $3,
            thumbnail_path = $4,
            preview_path = $5,
            processed_cids = $6,
            thumbnail_cid = $7,
            updated_at = NOW()
        WHERE id = $1`

	outJSON, _ := json.Marshal(outputPaths)
	cidsJSON, _ := json.Marshal(processedCIDs)

	result, err := r.db.ExecContext(ctx, query, videoID, status, outJSON, thumbnailPath, previewPath, cidsJSON, thumbnailCID)
	if err != nil {
		return domain.NewDomainError("UPDATE_PROCESSING_FAILED", "Failed to update processing info with CIDs")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found")
	}
	return nil
}

func (r *videoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	baseQuery := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path
        FROM videos
        WHERE privacy = 'public' AND status = 'completed'`

	countQuery := `SELECT COUNT(*) FROM videos WHERE privacy = 'public' AND status = 'completed'`

	args := []interface{}{}
	argIndex := 1

	// Add filters
	if req.CategoryID != nil {
		baseQuery += fmt.Sprintf(" AND category_id = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND category_id = $%d", argIndex)
		args = append(args, req.CategoryID)
		argIndex++
	}

	if req.Language != "" {
		baseQuery += fmt.Sprintf(" AND language = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND language = $%d", argIndex)
		args = append(args, req.Language)
		argIndex++
	}

	// Add sorting
	orderBy := "upload_date"
	if req.Sort != "" {
		switch req.Sort {
		case "title", "views", "upload_date", "duration":
			orderBy = req.Sort
		}
	}

	direction := "DESC"
	if req.Order == "asc" {
		direction = "ASC"
	}

	baseQuery += fmt.Sprintf(" ORDER BY %s %s", orderBy, direction)

	// Add pagination
	limit := 20
	if req.Limit > 0 && req.Limit <= 100 {
		limit = req.Limit
	}
	offset := 0
	if req.Offset > 0 {
		offset = req.Offset
	}

	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, limit, offset)

	// Get total count
	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, args[:len(args)-2]...).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count videos")
	}

	// Get videos
	rows, err := r.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, domain.NewDomainError("QUERY_FAILED", "Failed to list videos")
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		v, err := scanVideoRow(rows)
		if err != nil {
			return nil, 0, err
		}
		videos = append(videos, v)
	}

	return videos, total, nil
}

func (r *videoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	baseQuery := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path
        FROM videos
        WHERE privacy = 'public' AND status = 'completed'`

	countQuery := `SELECT COUNT(*) FROM videos WHERE privacy = 'public' AND status = 'completed'`

	args := []interface{}{}
	argIndex := 1

	// Add search query using full-text search
	if req.Query != "" {
		searchCondition := fmt.Sprintf(` AND (
            to_tsvector('english', title || ' ' || description) @@ plainto_tsquery('english', $%d)
            OR title ILIKE $%d
            OR description ILIKE $%d
        )`, argIndex, argIndex+1, argIndex+2)

		baseQuery += searchCondition
		countQuery += searchCondition

		likeQuery := "%" + req.Query + "%"
		args = append(args, req.Query, likeQuery, likeQuery)
		argIndex += 3
	}

	// Add tag filtering
	if len(req.Tags) > 0 {
		tagCondition := fmt.Sprintf(" AND tags && $%d", argIndex)
		baseQuery += tagCondition
		countQuery += tagCondition
		args = append(args, pq.Array(req.Tags))
		argIndex++
	}

	// Add other filters
	if req.CategoryID != nil {
		baseQuery += fmt.Sprintf(" AND category_id = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND category_id = $%d", argIndex)
		args = append(args, req.CategoryID)
		argIndex++
	}

	if req.Language != "" {
		baseQuery += fmt.Sprintf(" AND language = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND language = $%d", argIndex)
		args = append(args, req.Language)
		argIndex++
	}

	// Add sorting
	orderBy := "upload_date"
	sortArgsCount := 0
	if req.Sort != "" {
		switch req.Sort {
		case "title", "views", "upload_date", "duration", "relevance":
			if req.Sort == "relevance" && req.Query != "" {
				orderBy = fmt.Sprintf("ts_rank(to_tsvector('english', title || ' ' || description), plainto_tsquery('english', $%d))", argIndex)
				args = append(args, req.Query)
				argIndex++
				sortArgsCount++
			} else {
				orderBy = req.Sort
			}
		}
	}

	direction := "DESC"
	if req.Order == "asc" {
		direction = "ASC"
	}

	baseQuery += fmt.Sprintf(" ORDER BY %s %s", orderBy, direction)

	// Add pagination
	limit := 20
	if req.Limit > 0 && req.Limit <= 100 {
		limit = req.Limit
	}
	offset := 0
	if req.Offset > 0 {
		offset = req.Offset
	}

	baseQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIndex, argIndex+1)
	args = append(args, limit, offset)

	// Get total count
	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, args[:len(args)-2-sortArgsCount]...).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count search results")
	}

	// Get videos
	rows, err := r.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, 0, domain.NewDomainError("QUERY_FAILED", "Failed to search videos")
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		v, err := scanVideoRow(rows)
		if err != nil {
			return nil, 0, err
		}
		videos = append(videos, v)
	}

	return videos, total, nil
}

// GetVideosForMigration returns videos that need to be migrated to S3
func (r *videoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path,
               COALESCE(s3_urls, '{}'::jsonb) as s3_urls,
               COALESCE(storage_tier, 'hot') as storage_tier,
               s3_migrated_at, COALESCE(local_deleted, false) as local_deleted
        FROM videos
        WHERE status = 'completed'
          AND (storage_tier = 'hot' OR storage_tier IS NULL)
          AND (s3_migrated_at IS NULL)
        ORDER BY upload_date DESC
        LIMIT $1`

	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, domain.NewDomainError("QUERY_FAILED", "Failed to get videos for migration")
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		var v domain.Video
		var processedCIDsJSON, metadataJSON, outputPathsJSON, s3URLsJSON []byte
		var tags pq.StringArray

		err := rows.Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.CategoryID, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &v.ThumbnailPath, &v.PreviewPath,
			&s3URLsJSON, &v.StorageTier, &v.S3MigratedAt, &v.LocalDeleted,
		)
		if err != nil {
			return nil, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
		}

		// Unmarshal JSON fields
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

		videos = append(videos, &v)
	}

	return videos, nil
}

// GetByRemoteURI retrieves a video by its remote ActivityPub URI
func (r *videoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	query := `
		SELECT 
			id, thumbnail_id, title, description, duration, views, privacy, status,
			upload_date, user_id, channel_id, original_cid, processed_cids, thumbnail_cid,
			output_paths, s3_urls, storage_tier, s3_migrated_at, local_deleted,
			thumbnail_path, preview_path, tags, category_id, language,
			file_size, mime_type, metadata, 
			is_remote, remote_uri, remote_actor_uri, remote_video_url,
			remote_instance_domain, remote_thumbnail_url, remote_last_synced_at,
			created_at, updated_at
		FROM videos
		WHERE remote_uri = $1 AND is_remote = true
	`

	var v domain.Video
	var processedCIDsJSON, outputPathsJSON, s3URLsJSON, metadataJSON []byte
	var tags pq.StringArray

	err := r.db.QueryRowContext(ctx, query, remoteURI).Scan(
		&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
		&v.Privacy, &v.Status, &v.UploadDate, &v.UserID, &v.ChannelID,
		&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID, &outputPathsJSON,
		&s3URLsJSON, &v.StorageTier, &v.S3MigratedAt, &v.LocalDeleted,
		&v.ThumbnailPath, &v.PreviewPath, &tags, &v.CategoryID, &v.Language,
		&v.FileSize, &v.MimeType, &metadataJSON,
		&v.IsRemote, &v.RemoteURI, &v.RemoteActorURI, &v.RemoteVideoURL,
		&v.RemoteInstanceDomain, &v.RemoteThumbnailURL, &v.RemoteLastSyncedAt,
		&v.CreatedAt, &v.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	// Unmarshal JSON fields
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

	return &v, nil
}

// CreateRemoteVideo creates a new remote video record (from federated instance)
func (r *videoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	query := `
		INSERT INTO videos (
			id, thumbnail_id, title, description, duration, privacy, status,
			upload_date, tags, language, file_size, metadata,
			is_remote, remote_uri, remote_actor_uri, remote_video_url,
			remote_instance_domain, remote_thumbnail_url, remote_last_synced_at,
			created_at, updated_at
		) VALUES (
			$1, COALESCE($2, gen_random_uuid()), $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19,
			$20, $21
		)
	`

	// Marshal JSON fields
	metadataJSON, _ := json.Marshal(video.Metadata)

	// Convert tags to pq.Array
	tags := pq.StringArray(video.Tags)

	_, err := r.db.ExecContext(ctx, query,
		video.ID, nil, // thumbnail_id will be generated
		video.Title, video.Description, video.Duration, video.Privacy, video.Status,
		video.UploadDate, tags, video.Language, video.FileSize, metadataJSON,
		video.IsRemote, video.RemoteURI, video.RemoteActorURI, video.RemoteVideoURL,
		video.RemoteInstanceDomain, video.RemoteThumbnailURL, video.RemoteLastSyncedAt,
		video.CreatedAt, video.UpdatedAt,
	)

	return err
}
