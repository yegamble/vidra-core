package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"vidra-core/internal/domain"
	coreplugin "vidra-core/internal/plugin"
	"vidra-core/internal/repository"

	"github.com/google/uuid"
)

func downloadPluginArchive(pluginURL string) ([]byte, error) {
	resp, err := http.Get(pluginURL) //nolint:gosec // URL is validated by callers before download.
	if err != nil {
		return nil, fmt.Errorf("download plugin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download plugin: unexpected status %d", resp.StatusCode)
	}

	archive, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return nil, fmt.Errorf("read plugin archive: %w", err)
	}

	return archive, nil
}

func installPluginArchive(
	ctx context.Context,
	pluginRepo *repository.PluginRepository,
	pluginManager *coreplugin.Manager,
	archive []byte,
	overwrite bool,
) (*domain.PluginRecord, error) {
	if pluginRepo == nil || pluginManager == nil {
		return nil, fmt.Errorf("plugin installation dependencies are not configured")
	}

	manifest, err := extractPluginManifest(archive)
	if err != nil {
		return nil, err
	}

	if err := coreplugin.ValidatePermissions(manifest.Permissions); err != nil {
		return nil, fmt.Errorf("invalid permissions: %w", err)
	}

	existing, err := pluginRepo.GetByName(ctx, manifest.Name)
	switch err {
	case nil:
		if !overwrite {
			return nil, domain.ErrPluginAlreadyExists
		}
	case domain.ErrPluginNotFound:
		if overwrite {
			return nil, domain.ErrPluginNotFound
		}
		existing = nil
	default:
		return nil, fmt.Errorf("lookup existing plugin: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "plugin-install-*")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	pluginDir, err := extractPluginArchive(archive, tempDir, manifest.Name)
	if err != nil {
		return nil, fmt.Errorf("extract plugin: %w", err)
	}

	if err := os.MkdirAll(pluginManager.GetPluginDir(), 0750); err != nil {
		return nil, fmt.Errorf("prepare plugin directory: %w", err)
	}

	finalPath := filepath.Join(pluginManager.GetPluginDir(), manifest.Name)
	backupPath := finalPath + ".bak"
	_ = os.RemoveAll(backupPath)

	if overwrite {
		if _, statErr := os.Stat(finalPath); statErr == nil {
			if err := os.Rename(finalPath, backupPath); err != nil {
				return nil, fmt.Errorf("backup existing plugin: %w", err)
			}
		}
	} else if _, statErr := os.Stat(finalPath); statErr == nil {
		return nil, domain.ErrPluginAlreadyExists
	}

	restoreBackup := func() {
		_ = os.RemoveAll(finalPath)
		if _, statErr := os.Stat(backupPath); statErr == nil {
			_ = os.Rename(backupPath, finalPath)
		}
	}

	if err := os.Rename(pluginDir, finalPath); err != nil {
		restoreBackup()
		return nil, fmt.Errorf("move plugin into place: %w", err)
	}

	if err := pluginManager.LoadPlugin(filepath.Join(finalPath, "plugin.json")); err != nil {
		restoreBackup()
		return nil, fmt.Errorf("load plugin manifest: %w", err)
	}

	record := &domain.PluginRecord{
		ID:          uuid.New(),
		Name:        manifest.Name,
		Version:     manifest.Version,
		Author:      manifest.Author,
		Description: manifest.Description,
		Status:      domain.PluginStatusInstalled,
		Config:      manifest.Config,
		Permissions: manifest.Permissions,
		Hooks:       convertEventTypesToStrings(manifest.Hooks),
		InstallPath: finalPath,
		Checksum:    "",
	}

	if existing != nil {
		record.ID = existing.ID
		record.Status = existing.Status
		record.EnabledAt = existing.EnabledAt
		record.DisabledAt = existing.DisabledAt
		record.LastError = existing.LastError
		record.InstalledAt = existing.InstalledAt
		if len(record.Config) == 0 {
			record.Config = existing.Config
		}
		if err := pluginRepo.Update(ctx, record); err != nil {
			restoreBackup()
			return nil, fmt.Errorf("update plugin record: %w", err)
		}
	} else {
		if err := pluginRepo.Create(ctx, record); err != nil {
			restoreBackup()
			return nil, fmt.Errorf("create plugin record: %w", err)
		}
	}

	_ = os.RemoveAll(backupPath)
	return record, nil
}

func extractPluginManifest(zipData []byte) (*coreplugin.PluginInfo, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read ZIP: %w", err)
	}

	for _, f := range zipReader.File {
		if f.Name == "plugin.json" || strings.HasSuffix(f.Name, "/plugin.json") {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open plugin.json: %w", err)
			}
			defer func() { _ = rc.Close() }()

			var manifest coreplugin.PluginInfo
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				return nil, fmt.Errorf("failed to parse plugin.json: %w", err)
			}

			if manifest.Name == "" {
				return nil, fmt.Errorf("plugin name is required")
			}
			if manifest.Version == "" {
				return nil, fmt.Errorf("plugin version is required")
			}
			if manifest.Author == "" {
				return nil, fmt.Errorf("plugin author is required")
			}

			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("plugin.json not found in ZIP")
}

func extractPluginArchive(zipData []byte, destDir, pluginName string) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("failed to read ZIP: %w", err)
	}

	pluginDir := filepath.Join(destDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create plugin directory: %w", err)
	}

	const maxFileSize = 100 * 1024 * 1024

	for _, f := range zipReader.File {
		if strings.Contains(f.Name, "..") {
			return "", fmt.Errorf("invalid file path: %s", f.Name)
		}

		destPath := filepath.Clean(filepath.Join(pluginDir, f.Name))
		if !strings.HasPrefix(destPath, filepath.Clean(pluginDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid file path (path traversal): %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, f.Mode()); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
			return "", fmt.Errorf("failed to create parent directory: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open file in ZIP: %w", err)
		}

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()
			return "", fmt.Errorf("failed to create file: %w", err)
		}

		if _, err := io.Copy(destFile, io.LimitReader(rc, maxFileSize)); err != nil {
			_ = destFile.Close()
			_ = rc.Close()
			return "", fmt.Errorf("failed to write file: %w", err)
		}

		_ = destFile.Close()
		_ = rc.Close()
	}

	return pluginDir, nil
}

func convertEventTypesToStrings(events []coreplugin.EventType) []string {
	result := make([]string, len(events))
	for i, e := range events {
		result[i] = string(e)
	}
	return result
}
