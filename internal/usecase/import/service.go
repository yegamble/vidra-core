package importuc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/importer"
	"athena/internal/port"
	"athena/internal/storage"

	"github.com/google/uuid"
)

// Service defines the import service interface
type Service interface {
	ImportVideo(ctx context.Context, req *ImportRequest) (*domain.VideoImport, error)
	CancelImport(ctx context.Context, importID, userID string) error
	GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error)
	ListUserImports(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, int, error)
	ProcessPendingImports(ctx context.Context) error
	CleanupOldImports(ctx context.Context, daysOld int) (int64, error)
}

// ImportRequest represents a video import request
type ImportRequest struct {
	UserID         string
	ChannelID      *string
	SourceURL      string
	TargetPrivacy  string
	TargetCategory *string
}

// ImportRepository defines repository methods needed by the service
type ImportRepository interface {
	Create(ctx context.Context, imp *domain.VideoImport) error
	GetByID(ctx context.Context, importID string) (*domain.VideoImport, error)
	GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, error)
	CountByUserID(ctx context.Context, userID string) (int, error)
	CountByUserIDAndStatus(ctx context.Context, userID string, status domain.ImportStatus) (int, error)
	CountByUserIDToday(ctx context.Context, userID string) (int, error)
	GetPending(ctx context.Context, limit int) ([]*domain.VideoImport, error)
	Update(ctx context.Context, imp *domain.VideoImport) error
	UpdateProgress(ctx context.Context, importID string, progress int, downloadedBytes int64) error
	MarkFailed(ctx context.Context, importID string, errorMessage string) error
	MarkCompleted(ctx context.Context, importID string, videoID string) error
	Delete(ctx context.Context, importID string) error
	CleanupOldImports(ctx context.Context, daysOld int) (int64, error)
	GetStuckImports(ctx context.Context, hoursStuck int) ([]*domain.VideoImport, error)
}

type service struct {
	importRepo    ImportRepository
	videoRepo     port.VideoRepository
	encodingRepo  port.EncodingRepository
	ytdlp         *importer.YtDlp
	cfg           *config.Config
	storageDir    string
	mu            sync.Mutex
	activeImports map[string]*importContext
}

type importContext struct {
	cancel context.CancelFunc
}

// NewService creates a new import service
func NewService(
	importRepo ImportRepository,
	videoRepo port.VideoRepository,
	encodingRepo port.EncodingRepository,
	ytdlp *importer.YtDlp,
	cfg *config.Config,
	storageDir string,
) Service {
	return &service{
		importRepo:    importRepo,
		videoRepo:     videoRepo,
		encodingRepo:  encodingRepo,
		ytdlp:         ytdlp,
		cfg:           cfg,
		storageDir:    storageDir,
		activeImports: make(map[string]*importContext),
	}
}

// ImportVideo starts a new video import
func (s *service) ImportVideo(ctx context.Context, req *ImportRequest) (*domain.VideoImport, error) {
	// Validate request
	if err := s.validateImportRequest(req); err != nil {
		return nil, err
	}

	// Check daily quota (100 imports per day per user)
	todayCount, err := s.importRepo.CountByUserIDToday(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check daily quota: %w", err)
	}
	if todayCount >= 100 {
		return nil, domain.ErrImportQuotaExceeded
	}

	// Check concurrent imports (max 5 per user)
	activeCount, err := s.importRepo.CountByUserIDAndStatus(ctx, req.UserID, domain.ImportStatusDownloading)
	if err != nil {
		return nil, fmt.Errorf("failed to check active imports: %w", err)
	}
	processingCount, err := s.importRepo.CountByUserIDAndStatus(ctx, req.UserID, domain.ImportStatusProcessing)
	if err != nil {
		return nil, fmt.Errorf("failed to check processing imports: %w", err)
	}
	if activeCount+processingCount >= 5 {
		return nil, domain.ErrImportRateLimited
	}

	// Validate URL with yt-dlp (quick check)
	if err := s.ytdlp.ValidateURL(ctx, req.SourceURL); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrImportUnsupportedURL, err)
	}

	// Extract metadata
	metadata, err := s.ytdlp.ExtractMetadata(ctx, req.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Create import record
	imp := &domain.VideoImport{
		UserID:         req.UserID,
		ChannelID:      req.ChannelID,
		SourceURL:      req.SourceURL,
		Status:         domain.ImportStatusPending,
		TargetPrivacy:  req.TargetPrivacy,
		TargetCategory: req.TargetCategory,
	}

	if err := imp.SetMetadata(metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}

	if err := s.importRepo.Create(ctx, imp); err != nil {
		return nil, fmt.Errorf("failed to create import: %w", err)
	}

	// Start background processing
	go s.processImport(context.Background(), imp.ID)

	return imp, nil
}

// processImport processes a single import in the background
func (s *service) processImport(ctx context.Context, importID string) {
	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register active import
	s.mu.Lock()
	s.activeImports[importID] = &importContext{cancel: cancel}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.activeImports, importID)
		s.mu.Unlock()
	}()

	// Get import
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return
	}

	// Start download
	if err := imp.Start(); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		return
	}
	if err := s.importRepo.Update(ctx, imp); err != nil {
		return
	}

	// Download video
	videoPath, err := s.downloadVideo(ctx, imp)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		s.cleanupFiles(imp.ID)
		return
	}

	// Mark as processing (encoding)
	if err := imp.MarkProcessing(); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		s.cleanupFiles(imp.ID)
		return
	}
	if err := s.importRepo.Update(ctx, imp); err != nil {
		s.cleanupFiles(imp.ID)
		return
	}

	// Create video record
	video, err := s.createVideoFromImport(ctx, imp, videoPath)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to create video: %v", err))
		s.cleanupFiles(imp.ID)
		return
	}

	// Move file to uploads directory
	finalPath, err := s.moveToUploads(video.ID, videoPath)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to move file: %v", err))
		return
	}

	// Create encoding job with the source file path
	if err := s.createEncodingJob(ctx, video, finalPath); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to create encoding job: %v", err))
		return
	}

	// Mark import as completed
	if err := s.importRepo.MarkCompleted(ctx, importID, video.ID); err != nil {
		return
	}

	// Cleanup temporary files
	s.cleanupFiles(imp.ID)
}

// downloadVideo downloads the video using yt-dlp
func (s *service) downloadVideo(ctx context.Context, imp *domain.VideoImport) (string, error) {
	progressCallback := func(progress int, downloadedBytes, totalBytes int64) {
		_ = s.importRepo.UpdateProgress(context.Background(), imp.ID, progress, downloadedBytes)
	}

	videoPath, err := s.ytdlp.Download(ctx, imp.SourceURL, imp.ID, progressCallback)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	return videoPath, nil
}

// createVideoFromImport creates a video record from import metadata
func (s *service) createVideoFromImport(ctx context.Context, imp *domain.VideoImport, videoPath string) (*domain.Video, error) {
	metadata, err := imp.GetMetadata()
	if err != nil {
		return nil, err
	}

	// Get file info
	fileInfo, err := os.Stat(videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat video file: %w", err)
	}

	// Parse channel ID if provided
	var channelUUID uuid.UUID
	if imp.ChannelID != nil {
		parsed, err := uuid.Parse(*imp.ChannelID)
		if err != nil {
			return nil, fmt.Errorf("invalid channel_id: %w", err)
		}
		channelUUID = parsed
	}

	video := &domain.Video{
		UserID:      imp.UserID,
		ChannelID:   channelUUID,
		Title:       metadata.Title,
		Description: metadata.Description,
		Privacy:     domain.Privacy(imp.TargetPrivacy),
		Status:      domain.StatusQueued,
		FileSize:    fileInfo.Size(),
		Duration:    metadata.Duration,
	}

	// Set tags if available
	if len(metadata.Tags) > 0 {
		video.Tags = metadata.Tags
	}

	if err := s.videoRepo.Create(ctx, video); err != nil {
		return nil, fmt.Errorf("failed to create video: %w", err)
	}

	return video, nil
}

// moveToUploads moves the downloaded file to the uploads directory
func (s *service) moveToUploads(videoID, sourcePath string) (string, error) {
	sp := storage.NewPaths(s.storageDir)

	// Determine file extension
	ext := filepath.Ext(sourcePath)
	if ext == "" {
		ext = ".mp4" // Default to mp4
	}

	destPath := sp.WebVideoFilePath(videoID, ext)

	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Move file
	if err := os.Rename(sourcePath, destPath); err != nil {
		// If rename fails (cross-device), copy then delete
		if err := copyFile(sourcePath, destPath); err != nil {
			return "", fmt.Errorf("failed to copy file: %w", err)
		}
		_ = os.Remove(sourcePath)
	}

	return destPath, nil
}

// createEncodingJob creates an encoding job for the imported video
func (s *service) createEncodingJob(ctx context.Context, video *domain.Video, sourceFilePath string) error {
	job := &domain.EncodingJob{
		VideoID:           video.ID,
		SourceFilePath:    sourceFilePath,
		Status:            domain.EncodingStatusPending,
		TargetResolutions: []string{"360p", "480p", "720p", "1080p"}, // Default resolutions
	}

	return s.encodingRepo.CreateJob(ctx, job)
}

// cleanupFiles removes temporary import files
func (s *service) cleanupFiles(importID string) {
	importDir := filepath.Join(s.storageDir, "imports", importID)
	_ = os.RemoveAll(importDir)
}

// CancelImport cancels an in-progress import
func (s *service) CancelImport(ctx context.Context, importID, userID string) error {
	// Get import
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return err
	}

	// Check ownership
	if imp.UserID != userID {
		return fmt.Errorf("unauthorized: import belongs to different user")
	}

	// Check if cancellable
	if imp.Status.IsTerminal() {
		return fmt.Errorf("cannot cancel import in terminal state: %s", imp.Status)
	}

	// Cancel context if active
	s.mu.Lock()
	if importCtx, exists := s.activeImports[importID]; exists {
		importCtx.cancel()
	}
	s.mu.Unlock()

	// Update status
	if err := imp.Cancel(); err != nil {
		return err
	}

	if err := s.importRepo.Update(ctx, imp); err != nil {
		return fmt.Errorf("failed to update import: %w", err)
	}

	// Cleanup files
	s.cleanupFiles(importID)

	return nil
}

// GetImport retrieves an import by ID
func (s *service) GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error) {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return nil, err
	}

	// Check ownership
	if imp.UserID != userID {
		return nil, fmt.Errorf("unauthorized: import belongs to different user")
	}

	return imp, nil
}

// ListUserImports lists imports for a user with pagination
func (s *service) ListUserImports(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, int, error) {
	imports, err := s.importRepo.GetByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := s.importRepo.CountByUserID(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	return imports, totalCount, nil
}

// ProcessPendingImports processes pending imports (called by background worker)
func (s *service) ProcessPendingImports(ctx context.Context) error {
	pending, err := s.importRepo.GetPending(ctx, 10)
	if err != nil {
		return fmt.Errorf("failed to get pending imports: %w", err)
	}

	for _, imp := range pending {
		// Check if already processing
		s.mu.Lock()
		_, exists := s.activeImports[imp.ID]
		s.mu.Unlock()

		if !exists {
			go s.processImport(context.Background(), imp.ID)
		}
	}

	// Check for stuck imports
	stuck, err := s.importRepo.GetStuckImports(ctx, 2) // 2 hours timeout
	if err != nil {
		return fmt.Errorf("failed to get stuck imports: %w", err)
	}

	for _, imp := range stuck {
		_ = s.importRepo.MarkFailed(ctx, imp.ID, "import timed out after 2 hours")
		s.cleanupFiles(imp.ID)
	}

	return nil
}

// CleanupOldImports removes old completed/failed imports
func (s *service) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	return s.importRepo.CleanupOldImports(ctx, daysOld)
}

// validateImportRequest validates an import request
func (s *service) validateImportRequest(req *ImportRequest) error {
	if req.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if req.SourceURL == "" {
		return fmt.Errorf("source_url is required")
	}
	if err := domain.ValidateURL(req.SourceURL); err != nil {
		return err
	}
	if req.TargetPrivacy == "" {
		req.TargetPrivacy = string(domain.PrivacyPrivate) // Default to private
	}
	if err := domain.ValidatePrivacy(req.TargetPrivacy); err != nil {
		return err
	}
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = sourceFile.Close()
	}()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = destFile.Close()
	}()

	if _, err := destFile.ReadFrom(sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}
