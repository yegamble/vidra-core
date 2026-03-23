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

type BackupComponents struct {
	IncludeDatabase bool     `json:"include_database"`
	IncludeRedis    bool     `json:"include_redis"`
	IncludeStorage  bool     `json:"include_storage"`
	ExcludeDirs     []string `json:"exclude_dirs,omitempty"`
}

func NewBackupComponents() BackupComponents {
	return BackupComponents{
		IncludeDatabase: true,
		IncludeRedis:    true,
		IncludeStorage:  true,
		ExcludeDirs:     []string{},
	}
}

func (c BackupComponents) GetIncludedComponents() []string {
	var components []string
	if c.IncludeDatabase {
		components = append(components, "database")
	}
	if c.IncludeRedis {
		components = append(components, "redis")
	}
	if c.IncludeStorage {
		components = append(components, "storage")
	}
	return components
}

type BackupJob = domain.BackupJob
type BackupResult = domain.BackupResult
