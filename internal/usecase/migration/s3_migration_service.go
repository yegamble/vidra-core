package migration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/storage"

	"github.com/sirupsen/logrus"
)

// VideoRepository defines the interface for video data access
type VideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	Update(ctx context.Context, video *domain.Video) error
	GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error)
}

// S3MigrationService handles migration of videos from local storage to S3
type S3MigrationService struct {
	s3Backend   storage.StorageBackend
	videoRepo   VideoRepository
	storagePath storage.Paths
	logger      *logrus.Logger
	deleteLocal bool
}

// Config holds configuration for S3 migration
type Config struct {
	S3Backend   storage.StorageBackend
	VideoRepo   VideoRepository
	StoragePath storage.Paths
	Logger      *logrus.Logger
	DeleteLocal bool // Whether to delete local files after successful migration
}

// NewS3MigrationService creates a new S3 migration service
func NewS3MigrationService(cfg Config) *S3MigrationService {
	if cfg.Logger == nil {
		cfg.Logger = logrus.New()
	}

	return &S3MigrationService{
		s3Backend:   cfg.S3Backend,
		videoRepo:   cfg.VideoRepo,
		storagePath: cfg.StoragePath,
		logger:      cfg.Logger,
		deleteLocal: cfg.DeleteLocal,
	}
}

// MigrateVideo migrates a single video from local storage to S3
func (s *S3MigrationService) MigrateVideo(ctx context.Context, videoID string) error {
	s.logger.WithField("video_id", videoID).Info("Starting S3 migration for video")

	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	if video.StorageTier == "cold" && video.S3MigratedAt != nil {
		s.logger.WithField("video_id", videoID).Info("Video already migrated to S3")
		return nil
	}

	s3URLs, filesToDelete, err := s.migrateVideoVariants(ctx, videoID, video)
	if err != nil {
		return err
	}

	hlsCleanup, err := s.migrateHLSIfPresent(ctx, videoID)
	if err != nil {
		return err
	}
	filesToDelete = append(filesToDelete, hlsCleanup...)

	filesToDelete = append(filesToDelete, s.migrateAssets(ctx, videoID, video)...)

	if err := s.markVideoMigrated(ctx, video, s3URLs); err != nil {
		return err
	}

	s.cleanupLocalFiles(ctx, video, filesToDelete)

	s.logger.WithField("video_id", videoID).Info("Successfully completed S3 migration")
	return nil
}

// migrateVideoVariants uploads each video output variant to S3.
func (s *S3MigrationService) migrateVideoVariants(ctx context.Context, videoID string, video *domain.Video) (map[string]string, []string, error) {
	s3URLs := make(map[string]string)
	var filesToDelete []string

	for variant, localPath := range video.OutputPaths {
		if localPath == "" {
			continue
		}

		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			s.logger.WithFields(logrus.Fields{
				"video_id": videoID,
				"variant":  variant,
				"path":     localPath,
			}).Warn("Local file not found, skipping")
			continue
		}

		s3Key := s.generateS3Key(videoID, variant, localPath)
		contentType := s.getContentType(localPath)
		if err := s.s3Backend.UploadFile(ctx, s3Key, localPath, contentType); err != nil {
			return nil, nil, fmt.Errorf("failed to upload variant %s to S3: %w", variant, err)
		}

		s3URL := s.s3Backend.GetURL(s3Key)
		s3URLs[variant] = s3URL

		if s.deleteLocal {
			filesToDelete = append(filesToDelete, localPath)
		}

		s.logger.WithFields(logrus.Fields{
			"video_id": videoID,
			"variant":  variant,
			"s3_url":   s3URL,
		}).Info("Successfully uploaded variant to S3")
	}

	return s3URLs, filesToDelete, nil
}

// migrateHLSIfPresent migrates HLS playlists and segments if the directory exists.
func (s *S3MigrationService) migrateHLSIfPresent(ctx context.Context, videoID string) ([]string, error) {
	hlsDir := s.storagePath.HLSVideoDir(videoID)
	if _, err := os.Stat(hlsDir); err != nil {
		return nil, nil
	}

	hlsFiles, err := s.migrateHLSFiles(ctx, videoID, hlsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to migrate HLS files: %w", err)
	}

	if s.deleteLocal {
		return hlsFiles, nil
	}
	return nil, nil
}

// migrateAssets migrates thumbnail and preview images, returning paths eligible for deletion.
func (s *S3MigrationService) migrateAssets(ctx context.Context, videoID string, video *domain.Video) []string {
	var filesToDelete []string

	if video.ThumbnailPath != "" {
		if err := s.migrateThumbnail(ctx, videoID, video.ThumbnailPath); err != nil {
			s.logger.WithError(err).Warn("Failed to migrate thumbnail")
		} else if s.deleteLocal {
			filesToDelete = append(filesToDelete, video.ThumbnailPath)
		}
	}

	if video.PreviewPath != "" {
		if err := s.migratePreview(ctx, videoID, video.PreviewPath); err != nil {
			s.logger.WithError(err).Warn("Failed to migrate preview")
		} else if s.deleteLocal {
			filesToDelete = append(filesToDelete, video.PreviewPath)
		}
	}

	return filesToDelete
}

// markVideoMigrated updates the video record to reflect S3 migration.
func (s *S3MigrationService) markVideoMigrated(ctx context.Context, video *domain.Video, s3URLs map[string]string) error {
	video.S3URLs = s3URLs
	video.StorageTier = "cold"
	now := time.Now()
	video.S3MigratedAt = &now
	video.LocalDeleted = false

	if err := s.videoRepo.Update(ctx, video); err != nil {
		return fmt.Errorf("failed to update video record: %w", err)
	}
	return nil
}

// cleanupLocalFiles deletes local files after a successful migration if configured.
func (s *S3MigrationService) cleanupLocalFiles(ctx context.Context, video *domain.Video, filesToDelete []string) {
	if !s.deleteLocal || len(filesToDelete) == 0 {
		return
	}

	if err := s.deleteLocalFiles(filesToDelete); err != nil {
		s.logger.WithError(err).Error("Failed to delete local files after migration")
		return
	}

	video.LocalDeleted = true
	if err := s.videoRepo.Update(ctx, video); err != nil {
		s.logger.WithError(err).Error("Failed to update local_deleted flag")
	}
}

// MigrateBatch migrates multiple videos to S3
func (s *S3MigrationService) MigrateBatch(ctx context.Context, batchSize int) (int, error) {
	videos, err := s.videoRepo.GetVideosForMigration(ctx, batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed to get videos for migration: %w", err)
	}

	migrated := 0
	for _, video := range videos {
		if err := s.MigrateVideo(ctx, video.ID); err != nil {
			s.logger.WithError(err).WithField("video_id", video.ID).Error("Failed to migrate video")
			continue
		}
		migrated++
	}

	s.logger.WithField("migrated", migrated).Info("Batch migration completed")
	return migrated, nil
}

// migrateHLSFiles migrates HLS playlists and segments to S3
func (s *S3MigrationService) migrateHLSFiles(ctx context.Context, videoID, hlsDir string) ([]string, error) {
	var uploadedFiles []string

	err := filepath.Walk(hlsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path from HLS directory
		relPath, err := filepath.Rel(hlsDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Generate S3 key for HLS file
		s3Key := fmt.Sprintf("videos/%s/hls/%s", videoID, relPath)

		// Upload to S3
		contentType := s.getContentType(path)
		if err := s.s3Backend.UploadFile(ctx, s3Key, path, contentType); err != nil {
			return fmt.Errorf("failed to upload HLS file %s: %w", path, err)
		}

		uploadedFiles = append(uploadedFiles, path)
		s.logger.WithFields(logrus.Fields{
			"video_id": videoID,
			"file":     relPath,
		}).Debug("Uploaded HLS file to S3")

		return nil
	})

	if err != nil {
		return nil, err
	}

	return uploadedFiles, nil
}

// migrateThumbnail migrates a thumbnail to S3
func (s *S3MigrationService) migrateThumbnail(ctx context.Context, videoID, thumbnailPath string) error {
	if _, err := os.Stat(thumbnailPath); os.IsNotExist(err) {
		return fmt.Errorf("thumbnail file not found: %s", thumbnailPath)
	}

	s3Key := fmt.Sprintf("videos/%s/thumbnail%s", videoID, filepath.Ext(thumbnailPath))
	return s.s3Backend.UploadFile(ctx, s3Key, thumbnailPath, "image/jpeg")
}

// migratePreview migrates a preview to S3
func (s *S3MigrationService) migratePreview(ctx context.Context, videoID, previewPath string) error {
	if _, err := os.Stat(previewPath); os.IsNotExist(err) {
		return fmt.Errorf("preview file not found: %s", previewPath)
	}

	s3Key := fmt.Sprintf("videos/%s/preview%s", videoID, filepath.Ext(previewPath))
	return s.s3Backend.UploadFile(ctx, s3Key, previewPath, "image/webp")
}

// deleteLocalFiles deletes local files after successful migration
func (s *S3MigrationService) deleteLocalFiles(files []string) error {
	var errors []string

	for _, file := range files {
		if err := os.Remove(file); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", file, err))
			s.logger.WithError(err).WithField("file", file).Error("Failed to delete local file")
		} else {
			s.logger.WithField("file", file).Debug("Deleted local file")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to delete some files: %s", strings.Join(errors, "; "))
	}

	return nil
}

// generateS3Key generates an S3 key for a video file
func (s *S3MigrationService) generateS3Key(videoID, variant, localPath string) string {
	ext := filepath.Ext(localPath)
	return fmt.Sprintf("videos/%s/%s%s", videoID, variant, ext)
}

// getContentType determines the content type based on file extension
func (s *S3MigrationService) getContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/mp2t"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
