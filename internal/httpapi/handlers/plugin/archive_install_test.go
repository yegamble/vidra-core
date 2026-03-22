package plugin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestZIP creates an in-memory ZIP archive containing the given files.
// files is a map of relative path -> file contents.
func buildTestZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		require.NoError(t, err, "create zip entry %s", name)
		_, err = f.Write(data)
		require.NoError(t, err, "write zip entry %s", name)
	}
	require.NoError(t, w.Close(), "close zip writer")
	return buf.Bytes()
}

// validPluginJSON returns minimal valid plugin.json content.
func validPluginJSON() []byte {
	return []byte(`{
		"name": "test-plugin",
		"version": "1.0.0",
		"author": "Test Author",
		"description": "A test plugin",
		"permissions": [],
		"hooks": []
	}`)
}

// TestArchive_ExtractManifest_ValidZIP verifies that a well-formed ZIP with plugin.json
// is parsed into the correct PluginInfo struct.
func TestArchive_ExtractManifest_ValidZIP(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": validPluginJSON(),
	})

	manifest, err := extractPluginManifest(zipData)
	require.NoError(t, err)
	assert.Equal(t, "test-plugin", manifest.Name)
	assert.Equal(t, "1.0.0", manifest.Version)
	assert.Equal(t, "Test Author", manifest.Author)
	assert.Equal(t, "A test plugin", manifest.Description)
}

// TestArchive_ExtractManifest_ManifestInSubdirectory verifies that plugin.json can
// be located in a subdirectory of the ZIP (e.g. test-plugin/plugin.json).
func TestArchive_ExtractManifest_ManifestInSubdirectory(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"test-plugin/plugin.json": validPluginJSON(),
		"test-plugin/main.js":     []byte("console.log('hello')"),
	})

	manifest, err := extractPluginManifest(zipData)
	require.NoError(t, err)
	assert.Equal(t, "test-plugin", manifest.Name)
}

// TestArchive_ExtractManifest_MissingManifest verifies that a ZIP without plugin.json
// returns an error.
func TestArchive_ExtractManifest_MissingManifest(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"main.js": []byte("console.log('no manifest')"),
	})

	_, err := extractPluginManifest(zipData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugin.json not found")
}

// TestArchive_ExtractManifest_InvalidJSON verifies that malformed plugin.json content
// returns a parse error.
func TestArchive_ExtractManifest_InvalidJSON(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": []byte(`{not valid json`),
	})

	_, err := extractPluginManifest(zipData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugin.json")
}

// TestArchive_ExtractManifest_MissingName verifies that plugin.json without a name
// returns a validation error.
func TestArchive_ExtractManifest_MissingName(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": []byte(`{"version":"1.0.0","author":"X","permissions":[],"hooks":[]}`),
	})

	_, err := extractPluginManifest(zipData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

// TestArchive_ExtractManifest_MissingVersion verifies that plugin.json without a version
// returns a validation error.
func TestArchive_ExtractManifest_MissingVersion(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": []byte(`{"name":"my-plugin","author":"X","permissions":[],"hooks":[]}`),
	})

	_, err := extractPluginManifest(zipData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

// TestArchive_ExtractManifest_MissingAuthor verifies that plugin.json without an author
// returns a validation error.
func TestArchive_ExtractManifest_MissingAuthor(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": []byte(`{"name":"my-plugin","version":"1.0.0","permissions":[],"hooks":[]}`),
	})

	_, err := extractPluginManifest(zipData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "author")
}

// TestArchive_ExtractManifest_NotZIP verifies that non-ZIP bytes return an error.
func TestArchive_ExtractManifest_NotZIP(t *testing.T) {
	_, err := extractPluginManifest([]byte("this is not a zip file"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ZIP")
}

// TestArchive_ExtractArchive_ValidZIP verifies that files from a valid ZIP are
// correctly extracted to the destination directory.
func TestArchive_ExtractArchive_ValidZIP(t *testing.T) {
	zipData := buildTestZIP(t, map[string][]byte{
		"plugin.json": validPluginJSON(),
		"main.js":     []byte("console.log('hello world')"),
		"lib/util.js": []byte("exports.helper = function() {}"),
	})

	destDir := t.TempDir()
	pluginDir, err := extractPluginArchive(zipData, destDir, "test-plugin")
	require.NoError(t, err)

	// Verify the returned plugin directory exists
	assert.DirExists(t, pluginDir)

	// Verify extracted files exist with correct content
	manifestContent, err := os.ReadFile(filepath.Join(pluginDir, "plugin.json"))
	require.NoError(t, err)
	assert.Contains(t, string(manifestContent), "test-plugin")

	mainContent, err := os.ReadFile(filepath.Join(pluginDir, "main.js"))
	require.NoError(t, err)
	assert.Equal(t, "console.log('hello world')", string(mainContent))

	libContent, err := os.ReadFile(filepath.Join(pluginDir, "lib/util.js"))
	require.NoError(t, err)
	assert.Equal(t, "exports.helper = function() {}", string(libContent))
}

// TestArchive_ExtractArchive_PathTraversal verifies that a ZIP containing path traversal
// entries (e.g. ../../../etc/passwd) is rejected to prevent directory traversal attacks.
func TestArchive_ExtractArchive_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Craft a malicious entry with path traversal
	f, err := w.Create("../../../evil.txt")
	require.NoError(t, err)
	_, err = f.Write([]byte("malicious content"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	zipData := buf.Bytes()

	destDir := t.TempDir()
	_, err = extractPluginArchive(zipData, destDir, "evil-plugin")
	require.Error(t, err, "path traversal attempt must be rejected")
	assert.Contains(t, err.Error(), "path")

	// Verify no files were written — the function aborts before any file write
	// (it may create the plugin directory, but no file content should exist)
	var fileCount int
	_ = filepath.Walk(destDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		fileCount++
		return nil
	})
	assert.Equal(t, 0, fileCount, "no files must be written when path traversal is detected")
}

// TestArchive_ExtractArchive_EmptyZIP verifies that an empty ZIP extracts without error
// and creates the plugin directory.
func TestArchive_ExtractArchive_EmptyZIP(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	require.NoError(t, w.Close())
	zipData := buf.Bytes()

	destDir := t.TempDir()
	pluginDir, err := extractPluginArchive(zipData, destDir, "empty-plugin")
	require.NoError(t, err)
	assert.DirExists(t, pluginDir)
}
