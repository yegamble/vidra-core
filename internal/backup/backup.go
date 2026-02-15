package backup

import (
	"context"
	"io"
	"time"

	"athena/internal/domain"
)

type BackupStatus = domain.BackupStatus

const (
	StatusPending   = domain.BackupStatusPending
	StatusRunning   = domain.BackupStatusRunning
	StatusCompleted = domain.BackupStatusCompleted
	StatusFailed    = domain.BackupStatusFailed
)

type BackupTarget interface {
	Upload(ctx context.Context, reader io.Reader, path string) error

	Download(ctx context.Context, path string) (io.ReadCloser, error)

	List(ctx context.Context, prefix string) ([]BackupEntry, error)

	Delete(ctx context.Context, path string) error
}

type BackupEntry struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Checksum string    `json:"checksum,omitempty"`
}

type BackupJob = domain.BackupJob
type BackupResult = domain.BackupResult
