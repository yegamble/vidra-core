package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestMultiHandler_FansToAllHandlers(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelDebug})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(NewMultiHandler(h1, h2))
	logger.Info("hello world", "key", "value")

	if buf1.Len() == 0 {
		t.Error("expected handler 1 to receive log entry")
	}
	if buf2.Len() == 0 {
		t.Error("expected handler 2 to receive log entry")
	}

	// Both should contain the same message
	var entry1, entry2 map[string]interface{}
	if err := json.Unmarshal(buf1.Bytes(), &entry1); err != nil {
		t.Fatalf("handler 1 output not valid JSON: %v", err)
	}
	if err := json.Unmarshal(buf2.Bytes(), &entry2); err != nil {
		t.Fatalf("handler 2 output not valid JSON: %v", err)
	}
	if entry1["msg"] != "hello world" {
		t.Errorf("handler 1: expected msg='hello world', got %v", entry1["msg"])
	}
	if entry2["msg"] != "hello world" {
		t.Errorf("handler 2: expected msg='hello world', got %v", entry2["msg"])
	}
}

func TestMultiHandler_RespectsLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})

	logger := slog.New(NewMultiHandler(h))
	logger.Info("should not appear")

	if buf.Len() != 0 {
		t.Errorf("expected info to be filtered by warn handler, got: %s", buf.String())
	}

	logger.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("expected warn to be logged")
	}
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(NewMultiHandler(h)).With("service", "test")
	logger.Info("message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["service"] != "test" {
		t.Errorf("expected service=test in entry, got %v", entry["service"])
	}
}

func TestMultiHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(NewMultiHandler(h)).WithGroup("http")
	logger.Info("request", "method", "GET")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if httpGroup, ok := entry["http"].(map[string]interface{}); !ok {
		t.Errorf("expected http group in entry, got: %v", entry)
	} else if httpGroup["method"] != "GET" {
		t.Errorf("expected http.method=GET, got: %v", httpGroup["method"])
	}
}

func TestNewLoggerWithFile_StderrOnlyWhenNoDirSet(t *testing.T) {
	var stderrBuf bytes.Buffer
	cfg := LoggerConfig{
		Level:    "info",
		Format:   "json",
		LogDir:   "", // empty = stderr only
		Filename: "test.log",
		Rotation: RotationConfig{Enabled: true, MaxSizeMB: 12, MaxFiles: 5},
		Writer:   &stderrBuf,
	}

	logger, closer := NewLoggerWithFile(cfg)
	defer closer.Close()

	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	logger.Info("test message")
	if stderrBuf.Len() == 0 {
		t.Error("expected message written to stderr buffer")
	}
}

func TestNewLoggerWithFile_DualOutputWhenDirSet(t *testing.T) {
	dir := t.TempDir()
	var stderrBuf bytes.Buffer
	cfg := LoggerConfig{
		Level:    "info",
		Format:   "json",
		LogDir:   dir,
		Filename: "test.log",
		Rotation: RotationConfig{Enabled: false, MaxSizeMB: 12, MaxFiles: 5},
		Writer:   &stderrBuf,
	}

	logger, closer := NewLoggerWithFile(cfg)
	defer closer.Close()

	logger.Info("dual output test")

	// stderr buffer should have content
	if stderrBuf.Len() == 0 {
		t.Error("expected message written to stderr buffer")
	}

	// Closer should flush without error
	if err := closer.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}

func TestNewLoggerWithFile_TextFormatForDevelopment(t *testing.T) {
	var buf bytes.Buffer
	cfg := LoggerConfig{
		Level:    "info",
		Format:   "text",
		LogDir:   "",
		Filename: "test.log",
		Rotation: RotationConfig{Enabled: false},
		Writer:   &buf,
	}

	logger, closer := NewLoggerWithFile(cfg)
	defer closer.Close()

	logger.Info("text format test")
	output := buf.String()

	// text format should not be valid JSON
	var m map[string]interface{}
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &m) == nil {
		t.Error("expected text format output, got JSON")
	}
	if !strings.Contains(output, "text format test") {
		t.Errorf("expected message in output: %s", output)
	}
}
