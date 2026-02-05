package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateURL_Security_ArgInjection(t *testing.T) {
	tmpDir, mockBin, logFile := setupMockYtDlp(t)
	defer os.RemoveAll(tmpDir)

	y := NewYtDlp(mockBin, tmpDir)
	maliciousURL := "-malicious-flag"

	// 1. ValidateURL
	// Ignore error as we only care about args passed to the command
	_ = y.ValidateURL(context.Background(), maliciousURL)

	checkArgs(t, logFile, maliciousURL, "ValidateURL")
}

func TestExtractMetadata_Security_ArgInjection(t *testing.T) {
	tmpDir, mockBin, logFile := setupMockYtDlp(t)
	defer os.RemoveAll(tmpDir)

	y := NewYtDlp(mockBin, tmpDir)
	maliciousURL := "-malicious-flag"

	_, _ = y.ExtractMetadata(context.Background(), maliciousURL)

	checkArgs(t, logFile, maliciousURL, "ExtractMetadata")
}

func TestDownload_Security_ArgInjection(t *testing.T) {
	tmpDir, mockBin, logFile := setupMockYtDlp(t)
	defer os.RemoveAll(tmpDir)

	y := NewYtDlp(mockBin, tmpDir)
	maliciousURL := "-malicious-flag"

	// Download requires an importID
	_, _ = y.Download(context.Background(), maliciousURL, "test-import-id", nil)

	checkArgs(t, logFile, maliciousURL, "Download")
}

func TestDownloadThumbnail_Security_ArgInjection(t *testing.T) {
	tmpDir, mockBin, logFile := setupMockYtDlp(t)
	defer os.RemoveAll(tmpDir)

	y := NewYtDlp(mockBin, tmpDir)
	maliciousURL := "-malicious-flag"

	_ = y.DownloadThumbnail(context.Background(), maliciousURL, filepath.Join(tmpDir, "thumb.jpg"))

	checkArgs(t, logFile, maliciousURL, "DownloadThumbnail")
}

func setupMockYtDlp(t *testing.T) (string, string, string) {
	tmpDir, err := os.MkdirTemp("", "ytdlp_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	mockBin := filepath.Join(tmpDir, "yt-dlp-mock")
	logFile := filepath.Join(tmpDir, "args.txt")

	// Script: log args to file, output valid-ish data to stdout to prevent early exits
	// We use >> to append, but since we create a new dir per test, it starts empty.
	scriptContent := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
if echo "$@" | grep -q -- "--dump-json"; then
    echo '{"title": "Mock", "duration": 10}'
elif echo "$@" | grep -q -- "--get-title"; then
    echo "Mock Title"
fi
exit 0
`, logFile)

	if err := os.WriteFile(mockBin, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write mock binary: %v", err)
	}

	return tmpDir, mockBin, logFile
}

func checkArgs(t *testing.T, logFile, maliciousURL, funcName string) {
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("[%s] failed to read args log: %v", funcName, err)
	}
	argsStr := string(content)

	// We expect: ... -- -malicious-flag
	expected := "-- " + maliciousURL
	if !strings.Contains(argsStr, expected) {
		t.Errorf("[%s] Security vulnerability: URL not escaped with '--'. Args: %s", funcName, argsStr)
	}
}
