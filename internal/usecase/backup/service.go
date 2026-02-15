package backup

import (
	"context"

	"athena/internal/backup"
)

type Service struct {
	restoreManager *backup.RestoreManager
	target         backup.BackupTarget
	tempDir        string
}

func NewService(target backup.BackupTarget, tempDir string) *Service {
	return &Service{
		restoreManager: backup.NewRestoreManager(target, tempDir),
		target:         target,
		tempDir:        tempDir,
	}
}

func (s *Service) ListBackups(ctx context.Context) ([]backup.BackupEntry, error) {
	return s.target.List(ctx, "")
}

func (s *Service) TriggerBackup(ctx context.Context) error {
	return nil
}

func (s *Service) DeleteBackup(ctx context.Context, path string) error {
	return s.target.Delete(ctx, path)
}

func (s *Service) StartRestore(ctx context.Context, opts backup.RestoreOptions) (<-chan backup.RestoreProgress, error) {
	progressChan := make(chan backup.RestoreProgress, 10)

	go func() {
		_ = s.restoreManager.Restore(ctx, opts, progressChan)
	}()

	return progressChan, nil
}
