package importuc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const importUnitUserID = "11111111-1111-1111-1111-111111111111"

func newRealService(
	importRepo ImportRepository,
	videoRepo port.VideoRepository,
	encodingRepo port.EncodingRepository,
	storageDir string,
) *service {
	return &service{
		importRepo:    importRepo,
		videoRepo:     videoRepo,
		encodingRepo:  encodingRepo,
		cfg:           &config.Config{StorageDir: storageDir},
		storageDir:    storageDir,
		activeImports: make(map[string]*importContext),
	}
}

func TestRealService_ValidateImportRequest(t *testing.T) {
	svc := newRealService(nil, nil, nil, t.TempDir())

	t.Run("missing user", func(t *testing.T) {
		err := svc.validateImportRequest(&ImportRequest{
			SourceURL: "https://8.8.8.8/video",
		})
		require.ErrorContains(t, err, "user_id is required")
	})

	t.Run("missing source url", func(t *testing.T) {
		err := svc.validateImportRequest(&ImportRequest{
			UserID: importUnitUserID,
		})
		require.ErrorContains(t, err, "source_url is required")
	})

	t.Run("invalid source url", func(t *testing.T) {
		err := svc.validateImportRequest(&ImportRequest{
			UserID:    importUnitUserID,
			SourceURL: "not-a-url",
		})
		require.Error(t, err)
	})

	t.Run("default privacy", func(t *testing.T) {
		req := &ImportRequest{
			UserID:    importUnitUserID,
			SourceURL: "https://8.8.8.8/video",
		}

		err := svc.validateImportRequest(req)
		require.NoError(t, err)
		require.Equal(t, string(domain.PrivacyPrivate), req.TargetPrivacy)
	})

	t.Run("invalid privacy", func(t *testing.T) {
		err := svc.validateImportRequest(&ImportRequest{
			UserID:        importUnitUserID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: "restricted",
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid privacy value")
	})
}

func TestRealService_ImportVideo_ValidationAndLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("fails request validation before repository calls", func(t *testing.T) {
		svc := newRealService(nil, nil, nil, t.TempDir())

		imp, err := svc.ImportVideo(ctx, &ImportRequest{
			UserID:    importUnitUserID,
			SourceURL: "bad-url",
		})

		require.Nil(t, imp)
		require.Error(t, err)
	})

	t.Run("quota exceeded", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		req := &ImportRequest{
			UserID:        importUnitUserID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: string(domain.PrivacyPrivate),
		}

		importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(100, nil)

		imp, err := svc.ImportVideo(ctx, req)

		require.Nil(t, imp)
		require.ErrorIs(t, err, domain.ErrImportQuotaExceeded)
		importRepo.AssertExpectations(t)
	})

	t.Run("rate limited by concurrent imports", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		req := &ImportRequest{
			UserID:        importUnitUserID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: string(domain.PrivacyPrivate),
		}

		importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(1, nil)
		importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(3, nil)
		importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(2, nil)

		imp, err := svc.ImportVideo(ctx, req)

		require.Nil(t, imp)
		require.ErrorIs(t, err, domain.ErrImportRateLimited)
		importRepo.AssertExpectations(t)
	})

	t.Run("daily quota check repository error", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		req := &ImportRequest{
			UserID:        importUnitUserID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: string(domain.PrivacyPrivate),
		}

		importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(0, errors.New("db down"))

		imp, err := svc.ImportVideo(ctx, req)

		require.Nil(t, imp)
		require.ErrorContains(t, err, "failed to check daily quota")
		importRepo.AssertExpectations(t)
	})

	t.Run("active import check repository error", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		req := &ImportRequest{
			UserID:        importUnitUserID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: string(domain.PrivacyPrivate),
		}

		importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(0, nil)
		importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(0, errors.New("db down"))

		imp, err := svc.ImportVideo(ctx, req)

		require.Nil(t, imp)
		require.ErrorContains(t, err, "failed to check active imports")
		importRepo.AssertExpectations(t)
	})
}

func TestRealService_CancelImport(t *testing.T) {
	ctx := context.Background()

	t.Run("unauthorized user", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		importID := "import-unauthorized"

		importRepo.On("GetByID", ctx, importID).Return(&domain.VideoImport{
			ID:     importID,
			UserID: "some-other-user",
			Status: domain.ImportStatusDownloading,
		}, nil)

		err := svc.CancelImport(ctx, importID, importUnitUserID)

		require.ErrorContains(t, err, "unauthorized")
		importRepo.AssertExpectations(t)
	})

	t.Run("terminal state cannot be cancelled", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		importID := "import-terminal"

		importRepo.On("GetByID", ctx, importID).Return(&domain.VideoImport{
			ID:     importID,
			UserID: importUnitUserID,
			Status: domain.ImportStatusCompleted,
		}, nil)

		err := svc.CancelImport(ctx, importID, importUnitUserID)

		require.ErrorContains(t, err, "terminal state")
		importRepo.AssertExpectations(t)
	})

	t.Run("successful cancellation cancels context and removes files", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		storageDir := t.TempDir()
		svc := newRealService(importRepo, nil, nil, storageDir)
		importID := "import-cancel-ok"

		importDir := filepath.Join(storageDir, "imports", importID)
		require.NoError(t, os.MkdirAll(importDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(importDir, "partial.mp4"), []byte("data"), 0o600))

		cancelCalled := false
		svc.activeImports[importID] = &importContext{
			cancel: func() { cancelCalled = true },
		}

		importRepo.On("GetByID", ctx, importID).Return(&domain.VideoImport{
			ID:     importID,
			UserID: importUnitUserID,
			Status: domain.ImportStatusDownloading,
		}, nil)
		importRepo.
			On("Update", ctx, mock.MatchedBy(func(imp *domain.VideoImport) bool {
				return imp != nil && imp.ID == importID && imp.Status == domain.ImportStatusCancelled
			})).
			Return(nil)

		err := svc.CancelImport(ctx, importID, importUnitUserID)

		require.NoError(t, err)
		require.True(t, cancelCalled)
		_, statErr := os.Stat(importDir)
		require.Error(t, statErr)
		require.True(t, os.IsNotExist(statErr))
		importRepo.AssertExpectations(t)
	})

	t.Run("update failure wraps repository error", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		importID := "import-update-error"

		importRepo.On("GetByID", ctx, importID).Return(&domain.VideoImport{
			ID:     importID,
			UserID: importUnitUserID,
			Status: domain.ImportStatusDownloading,
		}, nil)
		importRepo.On("Update", ctx, mock.AnythingOfType("*domain.VideoImport")).Return(errors.New("write failed"))

		err := svc.CancelImport(ctx, importID, importUnitUserID)

		require.ErrorContains(t, err, "failed to update import")
		importRepo.AssertExpectations(t)
	})
}

func TestRealService_ProcessPendingImports(t *testing.T) {
	ctx := context.Background()

	t.Run("get pending failure", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		importRepo.On("GetPending", ctx, 10).Return(([]*domain.VideoImport)(nil), errors.New("db down"))

		err := svc.ProcessPendingImports(ctx)

		require.ErrorContains(t, err, "failed to get pending imports")
		importRepo.AssertExpectations(t)
	})

	t.Run("get stuck imports failure", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		svc := newRealService(importRepo, nil, nil, t.TempDir())
		importRepo.On("GetPending", ctx, 10).Return([]*domain.VideoImport{}, nil)
		importRepo.On("GetStuckImports", ctx, 2).Return(([]*domain.VideoImport)(nil), errors.New("db down"))

		err := svc.ProcessPendingImports(ctx)

		require.ErrorContains(t, err, "failed to get stuck imports")
		importRepo.AssertExpectations(t)
	})

	t.Run("marks stuck imports failed and cleans files", func(t *testing.T) {
		importRepo := new(MockImportRepository)
		storageDir := t.TempDir()
		svc := newRealService(importRepo, nil, nil, storageDir)
		stuckID := "import-stuck"

		stuckDir := filepath.Join(storageDir, "imports", stuckID)
		require.NoError(t, os.MkdirAll(stuckDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(stuckDir, "chunk.bin"), []byte("data"), 0o600))

		importRepo.On("GetPending", ctx, 10).Return([]*domain.VideoImport{}, nil)
		importRepo.On("GetStuckImports", ctx, 2).Return([]*domain.VideoImport{
			{ID: stuckID, UserID: importUnitUserID, Status: domain.ImportStatusDownloading},
		}, nil)
		importRepo.On("MarkFailed", ctx, stuckID, "import timed out after 2 hours").Return(nil)

		err := svc.ProcessPendingImports(ctx)

		require.NoError(t, err)
		_, statErr := os.Stat(stuckDir)
		require.Error(t, statErr)
		require.True(t, os.IsNotExist(statErr))
		importRepo.AssertExpectations(t)
	})
}

func TestRealService_CreateVideoFromImport(t *testing.T) {
	ctx := context.Background()

	buildImport := func(channelID *string) *domain.VideoImport {
		imp := &domain.VideoImport{
			ID:            "import-1",
			UserID:        importUnitUserID,
			ChannelID:     channelID,
			SourceURL:     "https://8.8.8.8/video",
			TargetPrivacy: string(domain.PrivacyPublic),
		}
		require.NoError(t, imp.SetMetadata(&domain.ImportMetadata{
			Title:       "Imported title",
			Description: "Imported description",
			Duration:    42,
			Tags:        []string{"athena", "import"},
		}))
		return imp
	}

	t.Run("invalid channel id", func(t *testing.T) {
		videoRepo := new(MockVideoRepository)
		svc := newRealService(new(MockImportRepository), videoRepo, new(MockEncodingRepository), t.TempDir())
		filePath := filepath.Join(t.TempDir(), "video.mp4")
		require.NoError(t, os.WriteFile(filePath, []byte("video-data"), 0o600))

		invalidChannel := "not-a-uuid"
		_, err := svc.createVideoFromImport(ctx, buildImport(&invalidChannel), filePath)

		require.ErrorContains(t, err, "invalid channel_id")
		videoRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("repository create failure", func(t *testing.T) {
		videoRepo := new(MockVideoRepository)
		svc := newRealService(new(MockImportRepository), videoRepo, new(MockEncodingRepository), t.TempDir())
		filePath := filepath.Join(t.TempDir(), "video.mp4")
		require.NoError(t, os.WriteFile(filePath, []byte("video-data"), 0o600))

		channelID := uuid.NewString()
		videoRepo.On("Create", ctx, mock.AnythingOfType("*domain.Video")).Return(errors.New("insert failed"))

		_, err := svc.createVideoFromImport(ctx, buildImport(&channelID), filePath)

		require.ErrorContains(t, err, "failed to create video")
		videoRepo.AssertExpectations(t)
	})

	t.Run("success", func(t *testing.T) {
		videoRepo := new(MockVideoRepository)
		svc := newRealService(new(MockImportRepository), videoRepo, new(MockEncodingRepository), t.TempDir())
		filePath := filepath.Join(t.TempDir(), "video.mp4")
		require.NoError(t, os.WriteFile(filePath, []byte("video-data"), 0o600))

		channelID := uuid.NewString()
		videoRepo.On("Create", ctx, mock.MatchedBy(func(v *domain.Video) bool {
			return v != nil &&
				v.UserID == importUnitUserID &&
				v.Title == "Imported title" &&
				v.Description == "Imported description" &&
				v.Duration == 42 &&
				v.FileSize > 0 &&
				len(v.Tags) == 2
		})).Return(nil)

		video, err := svc.createVideoFromImport(ctx, buildImport(&channelID), filePath)

		require.NoError(t, err)
		require.NotNil(t, video)
		require.Equal(t, "test-video-id", video.ID)
		videoRepo.AssertExpectations(t)
	})
}

func TestRealService_FileHelpers(t *testing.T) {
	t.Run("moveToUploads sets default mp4 extension", func(t *testing.T) {
		storageDir := t.TempDir()
		svc := newRealService(new(MockImportRepository), new(MockVideoRepository), new(MockEncodingRepository), storageDir)

		srcPath := filepath.Join(t.TempDir(), "downloaded-video")
		require.NoError(t, os.WriteFile(srcPath, []byte("binary-video"), 0o600))

		destPath, err := svc.moveToUploads("video-123", srcPath)
		require.NoError(t, err)
		require.True(t, strings.HasSuffix(destPath, ".mp4"))

		data, err := os.ReadFile(destPath)
		require.NoError(t, err)
		require.Equal(t, "binary-video", string(data))
	})

	t.Run("createEncodingJob uses default target resolutions", func(t *testing.T) {
		ctx := context.Background()
		encodingRepo := new(MockEncodingRepository)
		svc := newRealService(new(MockImportRepository), new(MockVideoRepository), encodingRepo, t.TempDir())
		video := &domain.Video{ID: "video-enc"}

		encodingRepo.On("CreateJob", ctx, mock.MatchedBy(func(job *domain.EncodingJob) bool {
			return job != nil &&
				job.VideoID == "video-enc" &&
				job.SourceFilePath == "/tmp/source.mp4" &&
				job.Status == domain.EncodingStatusPending &&
				len(job.TargetResolutions) == 4
		})).Return(nil)

		err := svc.createEncodingJob(ctx, video, "/tmp/source.mp4")

		require.NoError(t, err)
		encodingRepo.AssertExpectations(t)
	})

	t.Run("copyFile copies bytes", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "source.txt")
		dst := filepath.Join(t.TempDir(), "dest.txt")
		require.NoError(t, os.WriteFile(src, []byte("copy-me"), 0o600))

		err := copyFile(src, dst)
		require.NoError(t, err)

		data, err := os.ReadFile(dst)
		require.NoError(t, err)
		require.Equal(t, "copy-me", string(data))
	})
}
