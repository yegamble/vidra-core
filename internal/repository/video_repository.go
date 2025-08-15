package repository

import (
    "athena/internal/domain"
    "athena/internal/usecase"
    "context"
    "fmt"
    "encoding/json"

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
            created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10,
            $11, $12, $13,
            $14, $15, $16, $17, $18, $19,
            $20, $21
        )`

    // Marshal JSON fields
    processedCIDsJSON, _ := json.Marshal(v.ProcessedCIDs)
    metadataJSON, _ := json.Marshal(v.Metadata)

    _, err := r.db.ExecContext(ctx, query,
        v.ID, v.ThumbnailID, v.Title, v.Description, v.Duration, v.Views,
        v.Privacy, v.Status, v.UploadDate, v.UserID,
        v.OriginalCID, processedCIDsJSON, v.ThumbnailCID,
        pq.Array(v.Tags), v.Category, v.Language, v.FileSize, v.MimeType, metadataJSON,
        v.CreatedAt, v.UpdatedAt,
    )
    if err != nil {
        return fmt.Errorf("failed to create video: %w", err)
    }
    return nil
}

// no-op
