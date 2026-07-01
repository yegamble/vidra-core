package observability

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLoggerRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "warn", "json")
	if err != nil {
		t.Fatalf("NewLogger error = %v", err)
	}
	logger.Info("below threshold")
	if buf.Len() != 0 {
		t.Errorf("info should be filtered at warn level, got %q", buf.String())
	}
	logger.Warn("at threshold")
	if buf.Len() == 0 {
		t.Error("warn should be emitted at warn level")
	}
}

func TestNewLoggerJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "info", "json")
	if err != nil {
		t.Fatalf("NewLogger error = %v", err)
	}
	logger.Info("hello", "k", "v")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("json format not parseable: %v (%q)", err, buf.String())
	}
	if rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("record = %v, want msg=hello k=v", rec)
	}
}

func TestNewLoggerTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "info", "text")
	if err != nil {
		t.Fatalf("NewLogger error = %v", err)
	}
	logger.Info("hello")
	out := buf.String()
	if json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("text format should not emit JSON, got %q", out)
	}
	if !strings.Contains(out, "msg=hello") || !strings.Contains(out, "level=INFO") {
		t.Errorf("text output %q missing msg=hello / level=INFO", out)
	}
}

func TestNewLoggerDefaultsEmptyToInfoJSON(t *testing.T) {
	var buf bytes.Buffer
	logger, err := NewLogger(&buf, "", "")
	if err != nil {
		t.Fatalf("NewLogger error = %v", err)
	}
	logger.Debug("filtered at info")
	if buf.Len() != 0 {
		t.Errorf("debug should be filtered at the default info level, got %q", buf.String())
	}
	logger.Info("emitted")
	if !json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("empty format should default to JSON, got %q", buf.String())
	}
}

func TestNewLoggerRejectsBadLevel(t *testing.T) {
	if _, err := NewLogger(io.Discard, "loud", "json"); err == nil {
		t.Fatal("NewLogger expected error for invalid level, got nil")
	}
}

func TestNewLoggerRejectsBadFormat(t *testing.T) {
	if _, err := NewLogger(io.Discard, "info", "yaml"); err == nil {
		t.Fatal("NewLogger expected error for invalid format, got nil")
	}
}

func TestParseLevel(t *testing.T) {
	valid := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for in, want := range valid {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseLevel("nope"); err == nil {
		t.Error("ParseLevel(nope) expected error, got nil")
	}
}
