package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vidra-core/internal/database"

	_ "github.com/lib/pq"
)

var ErrRedisCLINotFound = errors.New("redis-cli not found in PATH")

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
	if _, err := os.Stat(dbPath); err == nil {
		if err := r.restoreDatabase(ctx, dbPath); err != nil {
			progressChan <- RestoreProgress{
				Stage: "error",
				Error: fmt.Sprintf("database restore failed: %v", err),
			}
			return fmt.Errorf("restoring database: %w", err)
		}
	} else {
		progressChan <- RestoreProgress{
			Stage:   "restoring_db",
			Message: "Skipping database restore (not included in backup)",
		}
	}

	progressChan <- RestoreProgress{
		Stage:    "restoring_redis",
		Progress: 0.7,
		Message:  "Restoring Redis...",
	}

	redisPath := filepath.Join(r.tempDir, "redis.rdb")
	if _, err := os.Stat(redisPath); err == nil {
		if err := r.restoreRedis(ctx, redisPath); err != nil {
			progressChan <- RestoreProgress{
				Stage:   "restoring_redis",
				Message: fmt.Sprintf("Warning: Redis restore failed: %v", err),
			}
		}
	}

	progressChan <- RestoreProgress{
		Stage:    "restoring_storage",
		Progress: 0.8,
		Message:  "Restoring storage files...",
	}

	storageArchive := filepath.Join(r.tempDir, "storage.tar.gz")
	if _, err := os.Stat(storageArchive); err == nil {
		if err := r.restoreStorage(ctx, storageArchive); err != nil {
			progressChan <- RestoreProgress{
				Stage:   "restoring_storage",
				Message: fmt.Sprintf("Warning: Storage restore failed: %v", err),
			}
		}
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

	const maxArchiveSize = 10 * 1024 * 1024 * 1024
	limitedReader := io.LimitReader(reader, maxArchiveSize)

	gzr, err := gzip.NewReader(limitedReader)
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

		if _, err := io.Copy(file, io.LimitReader(tr, 10*1024*1024*1024)); err != nil {
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

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "psql", r.DatabaseURL, "-f", dumpPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore failed: %w, output: %s", err, string(output))
	}

	return nil
}

func (r *RestoreManager) restoreRedis(ctx context.Context, rdbPath string) error {
	if _, err := exec.LookPath("redis-cli"); err != nil {
		return ErrRedisCLINotFound
	}

	redisURL := ""
	if r.BackupMgr != nil {
		redisURL = r.BackupMgr.RedisURL
	}

	out, err := exec.CommandContext(ctx, "redis-cli", "-u", redisURL, "CONFIG", "GET", "dir").Output()
	if err != nil {
		return fmt.Errorf("failed to query Redis data directory: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("unexpected output from CONFIG GET dir: %q", string(out))
	}
	redisDataDir := strings.TrimSpace(lines[len(lines)-1])
	if redisDataDir == "" {
		return fmt.Errorf("redis CONFIG GET dir returned empty directory")
	}

	src, err := os.Open(rdbPath)
	if err != nil {
		return fmt.Errorf("failed to open RDB file: %w", err)
	}
	defer src.Close()

	destPath := filepath.Join(redisDataDir, "dump.rdb")
	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination RDB file %s: %w", destPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy RDB file to %s: %w", destPath, err)
	}

	slog.Info(fmt.Sprintf("WARNING: RDB file copied to %s — restart Redis to load the snapshot (e.g. systemctl restart redis)", destPath))
	return nil
}

func (r *RestoreManager) restoreStorage(ctx context.Context, archivePath string) error {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open storage archive: %w", err)
	}
	defer archiveFile.Close()

	gzr, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		targetPath := strings.TrimPrefix(header.Name, "storage/")
		if targetPath == "" || targetPath == header.Name {
			continue
		}

		fullPath := filepath.Join(r.tempDir, "restored_storage", targetPath)

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				return fmt.Errorf("creating directory: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("creating parent directory: %w", err)
		}

		file, err := os.Create(fullPath)
		if err != nil {
			return fmt.Errorf("creating file: %w", err)
		}

		if _, err := io.Copy(file, io.LimitReader(tr, 10*1024*1024*1024)); err != nil {
			file.Close()
			return fmt.Errorf("extracting file: %w", err)
		}
		file.Close()
	}

	return nil
}

func (r *RestoreManager) runForwardMigrations(ctx context.Context) error {
	db, err := sql.Open("postgres", r.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	if err := database.RunMigrationsWithDB(ctx, db); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}
