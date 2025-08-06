package usecase

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/go-redis/redis/v8"
    "github.com/google/uuid"

    "gotube/internal/model"
    "gotube/internal/repository"
    "gotube/internal/service"
)

// VideoUsecase encapsulates video-related business logic: handling
// uploads, initiating transcoding, retrieving videos and renditions.
type VideoUsecase struct {
    Videos   repository.VideoRepository
    IPFS     *service.IPFSService
    IOTA     *service.IOTAService
    Redis    *redis.Client
    UploadDir string
}

// Upload handles saving an uploaded file, storing metadata, uploading
// to IPFS, enqueuing a transcode job, and returning the created video
// record. The caller provides the user ID, title, description, and a
// ReadCloser for the file content. The file is written to a unique
// temporary location on disk before processing.
func (uc *VideoUsecase) Upload(ctx context.Context, userID int64, title, description string, filename string, data []byte) (*model.Video, error) {
    // Ensure upload directory exists
    if err := os.MkdirAll(uc.UploadDir, 0755); err != nil {
        return nil, fmt.Errorf("create upload dir: %w", err)
    }
    // Generate unique filename to avoid collisions
    ext := filepath.Ext(filename)
    if ext == "" {
        ext = ".mp4"
    }
    uid := uuid.New().String()
    localPath := filepath.Join(uc.UploadDir, uid+ext)
    if err := os.WriteFile(localPath, data, 0644); err != nil {
        return nil, fmt.Errorf("write file: %w", err)
    }
    // Upload original to IPFS
    cid, err := uc.IPFS.AddFile(localPath)
    if err != nil {
        return nil, fmt.Errorf("ipfs add: %w", err)
    }
    // Persist video metadata
    now := time.Now()
    video := &model.Video{
        UserID:       userID,
        Title:        title,
        Description:  description,
        OriginalName: filename,
        IPFSCID:      cid,
        Status:       model.VideoStatusPending,
        CreatedAt:    now,
        UpdatedAt:    now,
    }
    if err := uc.Videos.Create(ctx, video); err != nil {
        return nil, fmt.Errorf("save video: %w", err)
    }
    // Enqueue transcode job (format: "videoID:localPath")
    jobPayload := fmt.Sprintf("%d:%s", video.ID, localPath)
    if err := uc.Redis.RPush(ctx, "transcode_jobs", jobPayload).Err(); err != nil {
        return nil, fmt.Errorf("enqueue job: %w", err)
    }
    // Optionally record IPFS CID in IOTA
    if uc.IOTA != nil {
        _ = uc.IOTA.WriteRecord(fmt.Sprintf("video %d uploaded with CID %s", video.ID, cid))
    }
    return video, nil
}

// Get returns a video by ID along with its renditions.
func (uc *VideoUsecase) Get(ctx context.Context, id int64) (*model.Video, []*model.VideoRendition, error) {
    video, err := uc.Videos.GetByID(ctx, id)
    if err != nil {
        return nil, nil, err
    }
    renditions, err := uc.Videos.ListRenditions(ctx, id)
    if err != nil {
        return nil, nil, err
    }
    return video, renditions, nil
}
