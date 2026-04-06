package backup

import (
	"context"
	"fmt"
	"log/slog"

	"vidra-core/internal/backup"
)

type Service struct {
	backupManager  *backup.BackupManager
	restoreManager *backup.RestoreManager
	target         backup.BackupTarget
	tempDir        string
}

func NewService(target backup.BackupTarget, tempDir string, backupManager *backup.BackupManager) *Service {
	return &Service{
		backupManager:  backupManager,
		restoreManager: backup.NewRestoreManager(target, tempDir),
		target:         target,
		tempDir:        tempDir,
	}
}

func (s *Service) ListBackups(ctx context.Context) ([]backup.BackupEntry, error) {
	return s.target.List(ctx, "")
}

func (s *Service) TriggerBackup(ctx context.Context) error {
	return s.TriggerBackupWithComponents(ctx, backup.NewBackupComponents())
}

func (s *Service) TriggerBackupWithComponents(ctx context.Context, components backup.BackupComponents) error {
	if s.backupManager == nil {
		return backup.ErrInvalidConfiguration
	}

	go func() {
		bgCtx := context.Background()
		if _, err := s.backupManager.CreateBackupWithComponents(bgCtx, components); err != nil {
			slog.Info(fmt.Sprintf("Backup failed: %v", err))
		} else {
			slog.Info("Backup completed successfully")
		}
	}()

	return nil
}

func (s *Service) DeleteBackup(ctx context.Context, path string) error {
	return s.target.Delete(ctx, path)
}

func (s *Service) StartRestore(ctx context.Context, opts backup.RestoreOptions) (<-chan backup.RestoreProgress, error) {
	progressChan := make(chan backup.RestoreProgress, 10)

	go func() {
		defer close(progressChan)
		if err := s.restoreManager.Restore(ctx, opts, progressChan); err != nil {
			slog.Info(fmt.Sprintf("Restore failed: %v", err))
			progressChan <- backup.RestoreProgress{
				Stage:   "error",
				Message: fmt.Sprintf("Restore failed: %v", err),
				Error:   err.Error(),
			}
		}
	}()

	return progressChan, nil
}
