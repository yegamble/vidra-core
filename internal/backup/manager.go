package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

func NewBackupManager(target BackupTarget, appVersion string, schemaVersion int64, databaseURL, redisURL string) *BackupManager {
	return &BackupManager{
		Target:        target,
		AppVersion:    appVersion,
		SchemaVersion: schemaVersion,
		DatabaseURL:   databaseURL,
		RedisURL:      redisURL,
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
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid backup manager: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "athena-backup-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	dbDumpPath := filepath.Join(tempDir, "database.sql")
	if err := m.dumpDatabase(ctx, dbDumpPath); err != nil {
		return nil, fmt.Errorf("database dump failed: %w", err)
	}

	manifest := NewManifest(m.AppVersion, m.SchemaVersion)
	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := m.writeManifest(manifest, manifestPath); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	archiveName := fmt.Sprintf("athena-backup-%s.tar.gz", time.Now().UTC().Format("2006-01-02-150405"))
	archivePath := filepath.Join(tempDir, archiveName)

	if err := m.createArchive(archivePath, tempDir, []string{"database.sql", "manifest.json"}); err != nil {
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

func (m *BackupManager) dumpDatabase(ctx context.Context, outputPath string) error {
	cmd := exec.CommandContext(ctx, "pg_dump", m.DatabaseURL, "-f", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %w, output: %s", err, string(output))
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
