package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYtDlp_ArgumentInjectionProtection(t *testing.T) {
	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "ytdlp_args.txt")
	mockScript := filepath.Join(tempDir, "mock_ytdlp.sh")

	// Script writes args to file and outputs "Title" to stdout to satisfy ValidateURL
	// For ExtractMetadata, it outputs empty JSON object {} to satisfy JSON parsing to some extent.
	content := `#!/bin/sh
echo "$@" > "` + argsFile + `"
echo '{"title": "test"}'
`
	err := os.WriteFile(mockScript, []byte(content), 0755)
	require.NoError(t, err)

	outputDir := t.TempDir()
	y := NewYtDlp(mockScript, outputDir)
	ctx := context.Background()
	testURL := "http://example.com"

	t.Run("ValidateURL uses delimiter", func(t *testing.T) {
		_ = y.ValidateURL(ctx, testURL)

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		argsStr := string(args)

		// We expect "-- http://example.com" to be present at the end
		assert.Contains(t, argsStr, "-- "+testURL, "ValidateURL should use '--' delimiter before URL")
	})

	t.Run("ExtractMetadata uses delimiter", func(t *testing.T) {
		_, _ = y.ExtractMetadata(ctx, testURL)

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		argsStr := string(args)

		assert.Contains(t, argsStr, "-- "+testURL, "ExtractMetadata should use '--' delimiter before URL")
	})

	t.Run("Download uses delimiter", func(t *testing.T) {
		_, _ = y.Download(ctx, testURL, "testID", nil)

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		argsStr := string(args)

		assert.Contains(t, argsStr, "-- "+testURL, "Download should use '--' delimiter before URL")
	})

	t.Run("DownloadThumbnail uses delimiter", func(t *testing.T) {
		_ = y.DownloadThumbnail(ctx, testURL, outputDir+"/thumb.jpg")

		args, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		argsStr := string(args)

		assert.Contains(t, argsStr, "-- "+testURL, "DownloadThumbnail should use '--' delimiter before URL")
	})
}
