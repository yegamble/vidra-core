package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type LocalBackend struct {
	BasePath string
}

func NewLocalBackend(basePath string) *LocalBackend {
	return &LocalBackend{
		BasePath: basePath,
	}
}

func (l *LocalBackend) safePath(path string) (string, error) {
	resolved := filepath.Clean(filepath.Join(l.BasePath, path))
	base := filepath.Clean(l.BasePath)
	if !strings.HasPrefix(resolved, base+string(os.PathSeparator)) && resolved != base {
		return "", fmt.Errorf("path traversal attempt: %s", path)
	}
	return resolved, nil
}

func (l *LocalBackend) Upload(ctx context.Context, reader io.Reader, path string) error {
	if err := os.MkdirAll(l.BasePath, 0755); err != nil {
		return fmt.Errorf("creating base directory: %w", err)
	}

	filePath, err := l.safePath(path)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("creating backup file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("writing backup data: %w", err)
	}

	return nil
}

func (l *LocalBackend) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	filePath, err := l.safePath(path)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening backup file: %w", err)
	}

	return file, nil
}

func (l *LocalBackend) List(ctx context.Context, prefix string) ([]BackupEntry, error) {
	entries := []BackupEntry{}

	err := filepath.Walk(l.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(l.BasePath, path)
		if err != nil {
			return err
		}

		if prefix == "" || strings.HasPrefix(filepath.Base(relPath), prefix) {
			entries = append(entries, BackupEntry{
				Path:    relPath,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("listing backups: %w", err)
	}

	return entries, nil
}

func (l *LocalBackend) Delete(ctx context.Context, path string) error {
	filePath, err := l.safePath(path)
	if err != nil {
		return err
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("deleting backup file: %w", err)
	}

	return nil
}
