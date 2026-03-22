package importuc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"
	"athena/internal/storage"

	"github.com/google/uuid"
)

type VideoDownloader interface {
	ValidateURL(ctx context.Context, url string) error
	ExtractMetadata(ctx context.Context, url string) (*domain.ImportMetadata, error)
	Download(ctx context.Context, url string, importID string, progressCallback func(progress int, downloadedBytes, totalBytes int64)) (string, error)
}

type Service interface {
	ImportVideo(ctx context.Context, req *ImportRequest) (*domain.VideoImport, error)
	CancelImport(ctx context.Context, importID, userID string) error
	RetryImport(ctx context.Context, importID, userID string) error
	GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error)
	ListUserImports(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, int, error)
	ProcessPendingImports(ctx context.Context) error
	CleanupOldImports(ctx context.Context, daysOld int) (int64, error)
}

type ImportRequest struct {
	UserID         string
	ChannelID      *string
	SourceURL      string
	TargetPrivacy  string
	TargetCategory *string
}

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
	ytdlp         VideoDownloader
	cfg           *config.Config
	storageDir    string
	mu            sync.Mutex
	activeImports map[string]*importContext
	ctx           context.Context
	cancel        context.CancelFunc
}

type importContext struct {
	cancel context.CancelFunc
}

func NewService(
	importRepo ImportRepository,
	videoRepo port.VideoRepository,
	encodingRepo port.EncodingRepository,
	ytdlp VideoDownloader,
	cfg *config.Config,
	storageDir string,
) Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &service{
		importRepo:    importRepo,
		videoRepo:     videoRepo,
		encodingRepo:  encodingRepo,
		ytdlp:         ytdlp,
		cfg:           cfg,
		storageDir:    storageDir,
		activeImports: make(map[string]*importContext),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (s *service) ImportVideo(ctx context.Context, req *ImportRequest) (*domain.VideoImport, error) {
	if err := s.validateImportRequest(req); err != nil {
		return nil, err
	}

	todayCount, err := s.importRepo.CountByUserIDToday(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check daily quota: %w", err)
	}
	if todayCount >= 100 {
		return nil, domain.ErrImportQuotaExceeded
	}

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

	if err := s.ytdlp.ValidateURL(ctx, req.SourceURL); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrImportUnsupportedURL, err)
	}

	metadata, err := s.ytdlp.ExtractMetadata(ctx, req.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

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

	go s.processImport(s.ctx, imp.ID)

	return imp, nil
}

func (s *service) processImport(ctx context.Context, importID string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	s.activeImports[importID] = &importContext{cancel: cancel}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.activeImports, importID)
		s.mu.Unlock()
	}()

	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return
	}

	if err := imp.Start(); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		return
	}
	if err := s.importRepo.Update(ctx, imp); err != nil {
		return
	}

	videoPath, err := s.downloadVideo(ctx, imp)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		s.cleanupFiles(imp.ID)
		return
	}

	if err := imp.MarkProcessing(); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, err.Error())
		s.cleanupFiles(imp.ID)
		return
	}
	if err := s.importRepo.Update(ctx, imp); err != nil {
		s.cleanupFiles(imp.ID)
		return
	}

	video, err := s.createVideoFromImport(ctx, imp, videoPath)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to create video: %v", err))
		s.cleanupFiles(imp.ID)
		return
	}

	finalPath, err := s.moveToUploads(video.ID, videoPath)
	if err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to move file: %v", err))
		return
	}

	if err := s.createEncodingJob(ctx, video, finalPath); err != nil {
		_ = s.importRepo.MarkFailed(ctx, importID, fmt.Sprintf("failed to create encoding job: %v", err))
		return
	}

	if err := s.importRepo.MarkCompleted(ctx, importID, video.ID); err != nil {
		return
	}

	s.cleanupFiles(imp.ID)
}

func (s *service) downloadVideo(ctx context.Context, imp *domain.VideoImport) (string, error) {
	progressCallback := func(progress int, downloadedBytes, totalBytes int64) {
		_ = s.importRepo.UpdateProgress(ctx, imp.ID, progress, downloadedBytes)
	}

	videoPath, err := s.ytdlp.Download(ctx, imp.SourceURL, imp.ID, progressCallback)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	return videoPath, nil
}

func (s *service) createVideoFromImport(ctx context.Context, imp *domain.VideoImport, videoPath string) (*domain.Video, error) {
	metadata, err := imp.GetMetadata()
	if err != nil {
		return nil, err
	}

	fileInfo, err := os.Stat(videoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat video file: %w", err)
	}

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

	if len(metadata.Tags) > 0 {
		video.Tags = metadata.Tags
	}

	if err := s.videoRepo.Create(ctx, video); err != nil {
		return nil, fmt.Errorf("failed to create video: %w", err)
	}

	return video, nil
}

func (s *service) moveToUploads(videoID, sourcePath string) (string, error) {
	sp := storage.NewPaths(s.storageDir)

	ext := filepath.Ext(sourcePath)
	if ext == "" {
		ext = ".mp4"
	}

	destPath := sp.WebVideoFilePath(videoID, ext)

	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	if err := os.Rename(sourcePath, destPath); err != nil {
		if err := copyFile(sourcePath, destPath); err != nil {
			return "", fmt.Errorf("failed to copy file: %w", err)
		}
		_ = os.Remove(sourcePath)
	}

	return destPath, nil
}

func (s *service) createEncodingJob(ctx context.Context, video *domain.Video, sourceFilePath string) error {
	job := &domain.EncodingJob{
		VideoID:           video.ID,
		SourceFilePath:    sourceFilePath,
		Status:            domain.EncodingStatusPending,
		TargetResolutions: []string{"360p", "480p", "720p", "1080p"},
	}

	return s.encodingRepo.CreateJob(ctx, job)
}

func (s *service) cleanupFiles(importID string) {
	importDir := filepath.Join(s.storageDir, "imports", importID)
	_ = os.RemoveAll(importDir)
}

func (s *service) CancelImport(ctx context.Context, importID, userID string) error {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return err
	}

	if imp.UserID != userID {
		return fmt.Errorf("%w: import belongs to different user", domain.ErrForbidden)
	}

	if imp.Status.IsTerminal() {
		return fmt.Errorf("%w: cannot cancel import in terminal state: %s", domain.ErrBadRequest, imp.Status)
	}

	s.mu.Lock()
	if importCtx, exists := s.activeImports[importID]; exists {
		importCtx.cancel()
	}
	s.mu.Unlock()

	if err := imp.Cancel(); err != nil {
		return err
	}

	if err := s.importRepo.Update(ctx, imp); err != nil {
		return fmt.Errorf("failed to update import: %w", err)
	}

	s.cleanupFiles(importID)

	return nil
}

func (s *service) RetryImport(ctx context.Context, importID, userID string) error {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return err
	}

	if imp.UserID != userID {
		return fmt.Errorf("%w: import belongs to different user", domain.ErrForbidden)
	}

	if imp.Status != domain.ImportStatusFailed {
		return fmt.Errorf("%w: cannot retry import in state %s", domain.ErrBadRequest, imp.Status)
	}

	s.mu.Lock()
	if importCtx, exists := s.activeImports[importID]; exists {
		importCtx.cancel()
		delete(s.activeImports, importID)
	}
	s.mu.Unlock()

	imp.Status = domain.ImportStatusPending
	imp.VideoID = nil
	imp.ErrorMessage = nil
	imp.Progress = 0
	imp.DownloadedBytes = 0
	imp.StartedAt = nil
	imp.CompletedAt = nil
	imp.UpdatedAt = time.Now()

	if err := s.importRepo.Update(ctx, imp); err != nil {
		return fmt.Errorf("failed to update import: %w", err)
	}

	s.cleanupFiles(importID)
	go s.processImport(s.ctx, imp.ID)

	return nil
}

func (s *service) GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error) {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return nil, err
	}

	if imp.UserID != userID {
		return nil, fmt.Errorf("%w: import belongs to different user", domain.ErrForbidden)
	}

	return imp, nil
}

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

func (s *service) ProcessPendingImports(ctx context.Context) error {
	pending, err := s.importRepo.GetPending(ctx, 10)
	if err != nil {
		return fmt.Errorf("failed to get pending imports: %w", err)
	}

	for _, imp := range pending {
		s.mu.Lock()
		_, exists := s.activeImports[imp.ID]
		s.mu.Unlock()

		if !exists {
			go s.processImport(s.ctx, imp.ID)
		}
	}

	stuck, err := s.importRepo.GetStuckImports(ctx, 2)
	if err != nil {
		return fmt.Errorf("failed to get stuck imports: %w", err)
	}

	for _, imp := range stuck {
		_ = s.importRepo.MarkFailed(ctx, imp.ID, "import timed out after 2 hours")
		s.cleanupFiles(imp.ID)
	}

	return nil
}

func (s *service) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	return s.importRepo.CleanupOldImports(ctx, daysOld)
}

func (s *service) validateImportRequest(req *ImportRequest) error {
	if req.UserID == "" {
		return fmt.Errorf("%w: user_id is required", domain.ErrUnauthorized)
	}
	if req.SourceURL == "" {
		return fmt.Errorf("%w: source_url is required", domain.ErrBadRequest)
	}
	if err := domain.ValidateURLWithSSRFCheck(req.SourceURL); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrImportInvalidURL, err)
	}
	if req.TargetPrivacy == "" {
		req.TargetPrivacy = string(domain.PrivacyPrivate)
	}
	if err := domain.ValidatePrivacy(req.TargetPrivacy); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrBadRequest, err)
	}
	return nil
}

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
