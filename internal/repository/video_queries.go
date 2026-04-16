package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"vidra-core/internal/domain"

	"github.com/lib/pq"
)

const publicVideoReadyClause = `(
        status = 'completed'
        OR (
            COALESCE(wait_transcoding, false) = false
            AND NULLIF(COALESCE(output_paths->>'source', ''), '') IS NOT NULL
        )
        OR COALESCE(processed_cids, '{}'::jsonb) <> '{}'::jsonb
    )`

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

	rows, err := r.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
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

		columns, _ := rows.Columns()
		isSimple := len(columns) < 36

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
			v.StorageTier = "hot"
			v.LocalDeleted = false
		}

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

		videos = append(videos, &v)
	}

	return videos, nil
}

func (r *videoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	countQuery := `SELECT COUNT(*) FROM videos WHERE user_id = $1`
	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, userID).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count user videos")
	}

	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path,
               wait_transcoding
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

func (r *videoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	baseQuery := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path,
               wait_transcoding
        FROM videos
        WHERE privacy = 'public' AND ` + publicVideoReadyClause + `
          AND NOT EXISTS (SELECT 1 FROM video_blacklist vb WHERE vb.video_id = videos.id)`

	countQuery := `SELECT COUNT(*) FROM videos WHERE privacy = 'public' AND ` + publicVideoReadyClause + ` AND NOT EXISTS (SELECT 1 FROM video_blacklist vb WHERE vb.video_id = videos.id)`

	args := []interface{}{}
	argIndex := 1

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

	if req.ChannelID != nil {
		baseQuery += fmt.Sprintf(" AND channel_id = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND channel_id = $%d", argIndex)
		args = append(args, req.ChannelID)
		argIndex++
	}

	if req.AccountID != nil {
		baseQuery += fmt.Sprintf(" AND user_id = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, req.AccountID)
		argIndex++
	}

	if req.Host != "" {
		baseQuery += fmt.Sprintf(" AND remote_instance_domain = $%d", argIndex)
		countQuery += fmt.Sprintf(" AND remote_instance_domain = $%d", argIndex)
		args = append(args, req.Host)
		argIndex++
	}

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

	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, args[:len(args)-2]...).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count videos")
	}

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
               created_at, updated_at, output_paths, thumbnail_path, preview_path,
               wait_transcoding
        FROM videos
        WHERE privacy = 'public' AND ` + publicVideoReadyClause + `
          AND NOT EXISTS (SELECT 1 FROM video_blacklist vb WHERE vb.video_id = videos.id)`

	countQuery := `SELECT COUNT(*) FROM videos WHERE privacy = 'public' AND ` + publicVideoReadyClause + ` AND NOT EXISTS (SELECT 1 FROM video_blacklist vb WHERE vb.video_id = videos.id)`

	args := []interface{}{}
	argIndex := 1

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

	if len(req.Tags) > 0 {
		tagCondition := fmt.Sprintf(" AND tags && $%d", argIndex)
		baseQuery += tagCondition
		countQuery += tagCondition
		args = append(args, pq.Array(req.Tags))
		argIndex++
	}

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

	var total int64
	err := r.db.QueryRowContext(ctx, countQuery, args[:len(args)-2-sortArgsCount]...).Scan(&total)
	if err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count search results")
	}

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

func (r *videoRepository) GetByChannelID(ctx context.Context, channelID string, limit, offset int) ([]*domain.Video, int64, error) {
	countQuery := `SELECT COUNT(*) FROM videos WHERE channel_id = $1`
	var total int64
	if err := r.db.QueryRowContext(ctx, countQuery, channelID).Scan(&total); err != nil {
		return nil, 0, domain.NewDomainError("COUNT_FAILED", "Failed to count channel videos")
	}

	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at, output_paths, thumbnail_path, preview_path,
               wait_transcoding
        FROM videos
        WHERE channel_id = $1
        ORDER BY upload_date DESC
        LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, channelID, limit, offset)
	if err != nil {
		return nil, 0, domain.NewDomainError("QUERY_FAILED", "Failed to get videos by channel id")
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
