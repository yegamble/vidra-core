package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/lib/pq"
)

func (r *videoRepository) UpdateProcessingInfo(ctx context.Context, params port.VideoProcessingParams) error {
	query := `
        UPDATE videos SET
            status = $2,
            duration = $3,
            metadata = $4,
            output_paths = $5,
            thumbnail_path = $6,
            preview_path = $7,
            updated_at = NOW()
        WHERE id = $1`

	outJSON, _ := json.Marshal(params.OutputPaths)
	metadataJSON, _ := json.Marshal(params.Metadata)
	result, err := r.db.ExecContext(
		ctx,
		query,
		params.VideoID,
		params.Status,
		params.Duration,
		metadataJSON,
		outJSON,
		params.ThumbnailPath,
		params.PreviewPath,
	)
	if err != nil {
		return domain.NewDomainError("UPDATE_PROCESSING_FAILED", "Failed to update processing info")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found")
	}
	return nil
}

func (r *videoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, params port.VideoProcessingWithCIDsParams) error {
	query := `
        UPDATE videos SET
            status = $2,
            duration = $3,
            metadata = $4,
            output_paths = $5,
            thumbnail_path = $6,
            preview_path = $7,
            processed_cids = $8,
            thumbnail_cid = $9,
            updated_at = NOW()
        WHERE id = $1`

	outJSON, _ := json.Marshal(params.OutputPaths)
	metadataJSON, _ := json.Marshal(params.Metadata)
	cidsJSON, _ := json.Marshal(params.ProcessedCIDs)

	result, err := r.db.ExecContext(
		ctx,
		query,
		params.VideoID,
		params.Status,
		params.Duration,
		metadataJSON,
		outJSON,
		params.ThumbnailPath,
		params.PreviewPath,
		cidsJSON,
		params.ThumbnailCID,
	)
	if err != nil {
		return domain.NewDomainError("UPDATE_PROCESSING_FAILED", "Failed to update processing info with CIDs")
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found")
	}
	return nil
}

// AppendOutputPath atomically merges a single key-value pair into the video's
// output_paths JSONB column without overwriting existing entries.  This is safe
// to call from concurrent goroutines encoding different resolutions.
func (r *videoRepository) AppendOutputPath(ctx context.Context, videoID string, key string, path string) error {
	query := `
		UPDATE videos
		SET output_paths = COALESCE(output_paths, '{}'::jsonb) || $2::jsonb,
		    updated_at   = NOW()
		WHERE id = $1`

	patch := map[string]string{key: path}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal output path patch: %w", err)
	}

	result, err := r.db.ExecContext(ctx, query, videoID, patchJSON)
	if err != nil {
		return fmt.Errorf("append output path: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

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

	metadataJSON, _ := json.Marshal(video.Metadata)

	tags := pq.StringArray(video.Tags)

	_, err := r.db.ExecContext(ctx, query,
		video.ID, nil,
		video.Title, video.Description, video.Duration, video.Privacy, video.Status,
		video.UploadDate, tags, video.Language, video.FileSize, metadataJSON,
		video.IsRemote, video.RemoteURI, video.RemoteActorURI, video.RemoteVideoURL,
		video.RemoteInstanceDomain, video.RemoteThumbnailURL, video.RemoteLastSyncedAt,
		video.CreatedAt, video.UpdatedAt,
	)

	return err
}

// CreateBatch inserts multiple videos. If a transaction already exists in ctx,
// it is reused; otherwise a new transaction is started and committed.
func (r *videoRepository) CreateBatch(ctx context.Context, videos []*domain.Video) error {
	if tx := GetTxFromContext(ctx); tx != nil {
		for _, v := range videos {
			if err := r.Create(ctx, v); err != nil {
				return fmt.Errorf("batch create video %s: %w", v.ID, err)
			}
		}
		return nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := WithTx(ctx, tx)
	for _, v := range videos {
		if err := r.Create(txCtx, v); err != nil {
			return fmt.Errorf("batch create video %s: %w", v.ID, err)
		}
	}
	return tx.Commit()
}
