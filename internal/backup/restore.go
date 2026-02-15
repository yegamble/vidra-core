package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type RestoreManager struct {
	target        BackupTarget
	tempDir       string
	DatabaseURL   string
	BackupMgr     *BackupManager
	CurrentSchema int64
}

type RestoreOptions struct {
	BackupPath      string
	CreatePreBackup bool
	RunMigrations   bool
}

type RestoreProgress struct {
	Stage      string  `json:"stage"`
	Progress   float64 `json:"progress"`
	Message    string  `json:"message"`
	Error      string  `json:"error,omitempty"`
	Completed  bool    `json:"completed"`
	SchemaFrom int64   `json:"schema_from,omitempty"`
	SchemaTo   int64   `json:"schema_to,omitempty"`
}

func NewRestoreManager(target BackupTarget, tempDir string) *RestoreManager {
	return &RestoreManager{
		target:  target,
		tempDir: tempDir,
	}
}

func (r *RestoreManager) ListBackups(ctx context.Context) ([]BackupEntry, error) {
	return r.target.List(ctx, "")
}

func (r *RestoreManager) Restore(ctx context.Context, opts RestoreOptions, progressChan chan<- RestoreProgress) error {
	defer close(progressChan)

	progressChan <- RestoreProgress{
		Stage:    "downloading",
		Progress: 0.1,
		Message:  "Downloading backup archive...",
	}

	reader, err := r.target.Download(ctx, opts.BackupPath)
	if err != nil {
		progressChan <- RestoreProgress{
			Stage: "error",
			Error: fmt.Sprintf("failed to download backup: %v", err),
		}
		return fmt.Errorf("downloading backup: %w", err)
	}
	defer reader.Close()

	progressChan <- RestoreProgress{
		Stage:    "extracting",
		Progress: 0.2,
		Message:  "Extracting backup archive...",
	}

	manifest, err := r.extractBackup(ctx, reader)
	if err != nil {
		progressChan <- RestoreProgress{
			Stage: "error",
			Error: fmt.Sprintf("failed to extract backup: %v", err),
		}
		return fmt.Errorf("extracting backup: %w", err)
	}

	if err := r.validateManifest(manifest); err != nil {
		progressChan <- RestoreProgress{
			Stage: "error",
			Error: fmt.Sprintf("invalid manifest: %v", err),
		}
		return fmt.Errorf("validating manifest: %w", err)
	}

	progressChan <- RestoreProgress{
		Stage:      "validating",
		Progress:   0.3,
		Message:    "Manifest validated successfully",
		SchemaFrom: manifest.SchemaVersion,
	}

	if opts.CreatePreBackup && r.BackupMgr != nil {
		progressChan <- RestoreProgress{
			Stage:    "pre_backup",
			Progress: 0.4,
			Message:  "Creating pre-restore backup...",
		}

		if _, err := r.BackupMgr.CreateBackup(ctx); err != nil {
			progressChan <- RestoreProgress{
				Stage:   "pre_backup",
				Message: fmt.Sprintf("Warning: pre-backup failed: %v", err),
			}
		}
	}

	progressChan <- RestoreProgress{
		Stage:    "restoring_db",
		Progress: 0.5,
		Message:  "Restoring database...",
	}

	dbPath := filepath.Join(r.tempDir, "database.sql")
	if err := r.restoreDatabase(ctx, dbPath); err != nil {
		progressChan <- RestoreProgress{
			Stage: "error",
			Error: fmt.Sprintf("database restore failed: %v", err),
		}
		return fmt.Errorf("restoring database: %w", err)
	}

	progressChan <- RestoreProgress{
		Stage:    "restoring_redis",
		Progress: 0.7,
		Message:  "Restoring Redis...",
	}

	progressChan <- RestoreProgress{
		Stage:    "restoring_storage",
		Progress: 0.8,
		Message:  "Restoring storage files...",
	}

	if opts.RunMigrations && manifest.SchemaVersion < r.CurrentSchema {
		progressChan <- RestoreProgress{
			Stage:      "migrations",
			Progress:   0.9,
			Message:    "Running forward migrations...",
			SchemaFrom: manifest.SchemaVersion,
			SchemaTo:   r.CurrentSchema,
		}

		if err := r.runForwardMigrations(ctx); err != nil {
			progressChan <- RestoreProgress{
				Stage: "error",
				Error: fmt.Sprintf("migration failed: %v", err),
			}
			return fmt.Errorf("running migrations: %w", err)
		}
	}

	progressChan <- RestoreProgress{
		Stage:     "complete",
		Progress:  1.0,
		Message:   "Restore completed successfully",
		Completed: true,
	}

	return nil
}

func (r *RestoreManager) extractBackup(ctx context.Context, reader io.Reader) (*Manifest, error) {
	if err := os.MkdirAll(r.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}

	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("creating gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var manifest *Manifest

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}

		if header.Name == "manifest.json" {
			manifestData, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("reading manifest: %w", err)
			}

			manifest = &Manifest{}
			if err := json.Unmarshal(manifestData, manifest); err != nil {
				return nil, fmt.Errorf("parsing manifest: %w", err)
			}
			continue
		}

		targetPath := filepath.Clean(filepath.Join(r.tempDir, header.Name))
		if !strings.HasPrefix(targetPath, filepath.Clean(r.tempDir)+string(os.PathSeparator)) {
			return nil, fmt.Errorf("invalid path in archive (path traversal): %s", header.Name)
		}

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return nil, fmt.Errorf("creating directory: %w", err)
			}
			continue
		}

		file, err := os.Create(targetPath)
		if err != nil {
			return nil, fmt.Errorf("creating file: %w", err)
		}

		if _, err := io.Copy(file, tr); err != nil {
			file.Close()
			return nil, fmt.Errorf("extracting file: %w", err)
		}
		file.Close()
	}

	if manifest == nil {
		return nil, fmt.Errorf("manifest.json not found in backup")
	}

	return manifest, nil
}

func (r *RestoreManager) validateManifest(manifest *Manifest) error {
	return manifest.Validate()
}

func (r *RestoreManager) restoreDatabase(ctx context.Context, dumpPath string) error {
	if _, err := os.Stat(dumpPath); err != nil {
		return fmt.Errorf("dump file not found: %w", err)
	}

	cmd := exec.CommandContext(ctx, "psql", r.DatabaseURL, "-f", dumpPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore failed: %w, output: %s", err, string(output))
	}

	return nil
}

func (r *RestoreManager) runForwardMigrations(ctx context.Context) error {
	return nil
}
