package repository

import (
	"vidra-core/internal/domain"
	"context"
	"encoding/json"

	"github.com/lib/pq"
)

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
