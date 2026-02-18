package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidConfiguration = errors.New("invalid backup manager configuration")
	ErrBackupFailed         = errors.New("backup operation failed")
)

type BackupManager struct {
	Target        BackupTarget
	AppVersion    string
	SchemaVersion int64
	DatabaseURL   string
	RedisURL      string
	StoragePath   string
	Components    BackupComponents
}

func NewBackupManager(target BackupTarget, appVersion string, schemaVersion int64, databaseURL, redisURL, storagePath string) *BackupManager {
	return &BackupManager{
		Target:        target,
		AppVersion:    appVersion,
		SchemaVersion: schemaVersion,
		DatabaseURL:   databaseURL,
		RedisURL:      redisURL,
		StoragePath:   storagePath,
		Components:    NewBackupComponents(),
	}
}

func (m *BackupManager) Validate() error {
	if m.AppVersion == "" {
		return ErrInvalidConfiguration
	}

	if m.SchemaVersion < 0 {
		return ErrInvalidConfiguration
	}

	if m.Target == nil {
		return ErrInvalidConfiguration
	}

	return nil
}

func (m *BackupManager) CreateJob(ctx context.Context) (*BackupJob, error) {
	job := &BackupJob{
		ID:            uuid.New().String(),
		Status:        StatusPending,
		CreatedAt:     time.Now().UTC(),
		SchemaVersion: m.SchemaVersion,
	}

	return job, nil
}

func (m *BackupManager) CreateBackup(ctx context.Context) (*BackupResult, error) {
	return m.CreateBackupWithComponents(ctx, m.Components)
}

func (m *BackupManager) CreateBackupWithComponents(ctx context.Context, components BackupComponents) (*BackupResult, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid backup manager: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "athena-backup-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	filesToArchive := []string{}

	if components.IncludeDatabase {
		dbDumpPath := filepath.Join(tempDir, "database.sql")
		if err := m.dumpDatabase(ctx, dbDumpPath); err != nil {
			return nil, fmt.Errorf("database dump failed: %w", err)
		}
		filesToArchive = append(filesToArchive, "database.sql")
	}

	if components.IncludeRedis && m.RedisURL != "" {
		redisDumpPath := filepath.Join(tempDir, "redis.rdb")
		if err := m.dumpRedis(ctx, redisDumpPath); err != nil {
			slog.Warn("Redis backup failed", "error", err)
		} else {
			filesToArchive = append(filesToArchive, "redis.rdb")
		}
	}

	if components.IncludeStorage && m.StoragePath != "" {
		storageArchivePath := filepath.Join(tempDir, "storage.tar.gz")
		if err := m.archiveStorage(ctx, storageArchivePath, components.ExcludeDirs); err != nil {
			slog.Warn("Storage backup failed", "error", err)
		} else {
			filesToArchive = append(filesToArchive, "storage.tar.gz")
		}
	}

	manifest := NewManifest(m.AppVersion, m.SchemaVersion)
	manifest.Contents = filesToArchive
	manifest.ComponentsIncluded = components.GetIncludedComponents()
	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := m.writeManifest(manifest, manifestPath); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}
	filesToArchive = append(filesToArchive, "manifest.json")

	archiveName := fmt.Sprintf("athena-backup-%s.tar.gz", time.Now().UTC().Format("2006-01-02-150405"))
	archivePath := filepath.Join(tempDir, archiveName)

	if err := m.createArchive(archivePath, tempDir, filesToArchive); err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	if err := m.Target.Upload(ctx, archiveFile, archiveName); err != nil {
		return nil, fmt.Errorf("failed to upload backup: %w", err)
	}

	stat, err := archiveFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat archive: %w", err)
	}

	return &BackupResult{
		JobID:         uuid.New().String(),
		Success:       true,
		BytesSize:     stat.Size(),
		BackupPath:    archiveName,
		SchemaVersion: m.SchemaVersion,
		CompletedAt:   time.Now().UTC(),
		Message:       "Backup completed successfully",
	}, nil
}

func runDumpCommand(ctx context.Context, cmdName string, args []string, outputPath string, timeoutMinutes int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdName, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w, output: %s", cmdName, err, string(output))
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("dump file not created: %w", err)
	}
	if stat.Size() == 0 {
		return fmt.Errorf("dump file is empty")
	}

	return nil
}

func (m *BackupManager) dumpDatabase(ctx context.Context, outputPath string) error {
	return runDumpCommand(ctx, "pg_dump", []string{m.DatabaseURL, "-f", outputPath}, outputPath, 30)
}

func (m *BackupManager) dumpRedis(ctx context.Context, outputPath string) error {
	return runDumpCommand(ctx, "redis-cli", []string{"-u", m.RedisURL, "--rdb", outputPath}, outputPath, 30)
}

func (m *BackupManager) archiveStorage(ctx context.Context, outputPath string, excludeDirs []string) error {
	if _, err := os.Stat(m.StoragePath); os.IsNotExist(err) {
		return fmt.Errorf("storage path does not exist: %s", m.StoragePath)
	}

	archiveFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create storage archive: %w", err)
	}
	defer archiveFile.Close()

	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	err = filepath.Walk(m.StoragePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(m.StoragePath, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		if shouldExcludePath(relPath, excludeDirs) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.Join("storage", relPath)

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk storage directory: %w", err)
	}

	return nil
}

func shouldExcludePath(relPath string, excludeDirs []string) bool {
	for _, excludeDir := range excludeDirs {
		excludePath := filepath.Clean(excludeDir)
		cleanPath := filepath.Clean(relPath)

		if cleanPath == excludePath {
			return true
		}

		rel, err := filepath.Rel(excludePath, cleanPath)
		if err == nil && !filepath.IsAbs(rel) && len(rel) > 0 && rel[0] != '.' {
			return true
		}
	}
	return false
}

func (m *BackupManager) writeManifest(manifest *Manifest, path string) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest file: %w", err)
	}

	return nil
}

func (m *BackupManager) createArchive(archivePath, baseDir string, files []string) error {
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer archiveFile.Close()

	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for _, fileName := range files {
		filePath := filepath.Join(baseDir, fileName)
		if err := m.addFileToArchive(tarWriter, filePath, fileName); err != nil {
			return fmt.Errorf("failed to add %s to archive: %w", fileName, err)
		}
	}

	return nil
}

func (m *BackupManager) addFileToArchive(tarWriter *tar.Writer, filePath, nameInArchive string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    nameInArchive,
		Size:    stat.Size(),
		Mode:    int64(stat.Mode()),
		ModTime: stat.ModTime(),
	}

	if err := tarWriter.WriteHeader(header); err != nil {
		return err
	}

	if _, err := io.Copy(tarWriter, file); err != nil {
		return err
	}

	return nil
}
