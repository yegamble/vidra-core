package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestLoggerTagsFactory_DefaultTags(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	lTags := LoggerTagsFactory("ap", "video")
	logger.Info("test message", lTags()...)

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	tags, ok := entry["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags array in log entry, got: %v", entry["tags"])
	}

	if len(tags) != 2 || tags[0] != "ap" || tags[1] != "video" {
		t.Errorf("expected tags=[ap video], got: %v", tags)
	}
}

func TestLoggerTagsFactory_MergesExtraTags(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	lTags := LoggerTagsFactory("ap")
	logger.Info("test message", lTags("video", "update")...)

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	tags, ok := entry["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags array, got: %v", entry["tags"])
	}

	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(tags), tags)
	}
	// Should be: ap, video, update
	tagMap := map[string]bool{}
	for _, tag := range tags {
		tagMap[tag.(string)] = true
	}
	for _, want := range []string{"ap", "video", "update"} {
		if !tagMap[want] {
			t.Errorf("expected tag %q in output, got tags: %v", want, tags)
		}
	}
}

func TestLoggerTagsFactory_EmptyDefaultTags(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	lTags := LoggerTagsFactory()
	logger.Info("test message", lTags("http")...)

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	tags, ok := entry["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags array, got: %v", entry["tags"])
	}
	if len(tags) != 1 || tags[0] != "http" {
		t.Errorf("expected tags=[http], got: %v", tags)
	}
}

func TestLoggerTagsFactory_NoTagsProducesEmptySlice(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	lTags := LoggerTagsFactory()
	logger.Info("test message", lTags()...)

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// When no tags at all, tags key should either be absent or empty array
	if tags, exists := entry["tags"]; exists {
		if arr, ok := tags.([]interface{}); ok && len(arr) != 0 {
			t.Errorf("expected empty tags array, got: %v", tags)
		}
	}
}
