package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type videoRepository struct {
	db *sqlx.DB
}

func NewVideoRepository(db *sqlx.DB) usecase.VideoRepository {
	return &videoRepository{db: db}
}

func (r *videoRepository) Create(ctx context.Context, v *domain.Video) error {
	query := `
        INSERT INTO videos (
            id, thumbnail_id, title, description, duration, views,
            privacy, status, upload_date, user_id,
            original_cid, processed_cids, thumbnail_cid,
            tags, category, language, file_size, mime_type, metadata,
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

	// Marshal JSON fields
	processedCIDsJSON, _ := json.Marshal(v.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(v.Metadata)
	outputPathsJSON, _ := json.Marshal(v.OutputPaths)

	_, err := r.db.ExecContext(ctx, query,
		v.ID, v.ThumbnailID, v.Title, v.Description, v.Duration, v.Views,
		v.Privacy, v.Status, v.UploadDate, v.UserID,
		v.OriginalCID, processedCIDsJSON, v.ThumbnailCID,
		pq.Array(v.Tags), v.Category, v.Language, v.FileSize, v.MimeType, metadataJSON,
		v.CreatedAt, v.UpdatedAt,
		outputPathsJSON, v.ThumbnailPath, v.PreviewPath,
	)
	if err != nil {
		return domain.NewDomainError("CREATE_FAILED", "Failed to create video")
	}
	return nil
}

func (r *videoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path
        FROM videos WHERE id = $1`

	var v domain.Video
	var processedCIDsJSON, metadataJSON, outputPathsJSON []byte
	var tags pq.StringArray

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
		&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
		&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
		&tags, &v.Category, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
		&v.CreatedAt, &v.UpdatedAt, &outputPathsJSON, &v.ThumbnailPath, &v.PreviewPath,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found")
		}
		return nil, domain.NewDomainError("GET_FAILED", "Failed to get video")
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
               tags, category, language, file_size, mime_type, metadata,
               created_at, updated_at
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
		var v domain.Video
		var processedCIDsJSON, metadataJSON []byte
		var tags pq.StringArray

		err := rows.Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.Category, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt,
		)
		if err != nil {
			return nil, 0, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
		}

		// Unmarshal JSON fields
		if len(processedCIDsJSON) > 0 {
			_ = json.Unmarshal(processedCIDsJSON, &v.ProcessedCIDs)
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &v.Metadata)
		}
		v.Tags = []string(tags)

		videos = append(videos, &v)
	}

	return videos, total, nil
}

func (r *videoRepository) Update(ctx context.Context, v *domain.Video) error {
	query := `
        UPDATE videos SET
            title = $2, description = $3, privacy = $4,
            tags = $5, category = $6, language = $7,
            status = $8, updated_at = $9
        WHERE id = $1 AND user_id = $10`

	result, err := r.db.ExecContext(ctx, query,
		v.ID, v.Title, v.Description, v.Privacy,
		pq.Array(v.Tags), v.Category, v.Language,
		v.Status, v.UpdatedAt, v.UserID,
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
	query := `DELETE FROM videos WHERE id = $1 AND user_id = $2`

	result, err := r.db.ExecContext(ctx, query, id, userID)
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

func (r *videoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	baseQuery := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category, language, file_size, mime_type, metadata,
               created_at, updated_at
        FROM videos 
        WHERE privacy = 'public' AND status = 'completed'`

	countQuery := `SELECT COUNT(*) FROM videos WHERE privacy = 'public' AND status = 'completed'`

	args := []interface{}{}
	argIndex := 1

	// Add filters
	if req.Category != "" {
		baseQuery += fmt.Sprintf(" AND category = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND category = $%d", argIndex)
		args = append(args, req.Category)
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
		var v domain.Video
		var processedCIDsJSON, metadataJSON []byte
		var tags pq.StringArray

		err := rows.Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.Category, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt,
		)
		if err != nil {
			return nil, 0, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
		}

		// Unmarshal JSON fields
		if len(processedCIDsJSON) > 0 {
			_ = json.Unmarshal(processedCIDsJSON, &v.ProcessedCIDs)
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &v.Metadata)
		}
		v.Tags = []string(tags)

		videos = append(videos, &v)
	}

	return videos, total, nil
}

func (r *videoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	baseQuery := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category, language, file_size, mime_type, metadata,
               created_at, updated_at
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
	if req.Category != "" {
		baseQuery += fmt.Sprintf(" AND category = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND category = $%d", argIndex)
		args = append(args, req.Category)
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
		case "title", "views", "upload_date", "duration", "relevance":
			if req.Sort == "relevance" && req.Query != "" {
				orderBy = "ts_rank(to_tsvector('english', title || ' ' || description), plainto_tsquery('english', '" + req.Query + "'))"
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
	err := r.db.QueryRowContext(ctx, countQuery, args[:len(args)-2]...).Scan(&total)
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
		var v domain.Video
		var processedCIDsJSON, metadataJSON []byte
		var tags pq.StringArray

		err := rows.Scan(
			&v.ID, &v.ThumbnailID, &v.Title, &v.Description, &v.Duration, &v.Views,
			&v.Privacy, &v.Status, &v.UploadDate, &v.UserID,
			&v.OriginalCID, &processedCIDsJSON, &v.ThumbnailCID,
			&tags, &v.Category, &v.Language, &v.FileSize, &v.MimeType, &metadataJSON,
			&v.CreatedAt, &v.UpdatedAt,
		)
		if err != nil {
			return nil, 0, domain.NewDomainError("SCAN_FAILED", "Failed to scan video row")
		}

		// Unmarshal JSON fields
		if len(processedCIDsJSON) > 0 {
			_ = json.Unmarshal(processedCIDsJSON, &v.ProcessedCIDs)
		}
		if len(metadataJSON) > 0 {
			_ = json.Unmarshal(metadataJSON, &v.Metadata)
		}
		v.Tags = []string(tags)

		videos = append(videos, &v)
	}

	return videos, total, nil
}
