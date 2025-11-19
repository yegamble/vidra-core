package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		level     string
		wantJSON  bool
		wantLevel slog.Level
		logFunc   func(*slog.Logger)
	}{
		{
			name:      "production JSON logger",
			env:       "production",
			level:     "info",
			wantJSON:  true,
			wantLevel: slog.LevelInfo,
			logFunc:   func(l *slog.Logger) { l.Info("test message", "key", "value") },
		},
		{
			name:      "development text logger",
			env:       "development",
			level:     "debug",
			wantJSON:  false,
			wantLevel: slog.LevelDebug,
			logFunc:   func(l *slog.Logger) { l.Debug("test message", "key", "value") },
		},
		{
			name:      "error level logger",
			env:       "production",
			level:     "error",
			wantJSON:  true,
			wantLevel: slog.LevelError,
			logFunc:   func(l *slog.Logger) { l.Error("test message", "key", "value") },
		},
		{
			name:      "warn level logger",
			env:       "production",
			level:     "warn",
			wantJSON:  true,
			wantLevel: slog.LevelWarn,
			logFunc:   func(l *slog.Logger) { l.Warn("test message", "key", "value") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger(tt.env, tt.level, &buf)

			if logger == nil {
				t.Fatal("NewLogger returned nil")
			}

			// Log a test message at the appropriate level
			tt.logFunc(logger)
			output := buf.String()

			if tt.wantJSON {
				// Verify JSON format
				var logEntry map[string]interface{}
				if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
					t.Errorf("expected JSON output, got: %s", output)
				}

				if logEntry["msg"] != "test message" {
					t.Errorf("expected msg='test message', got: %v", logEntry["msg"])
				}

				if logEntry["key"] != "value" {
					t.Errorf("expected key='value', got: %v", logEntry["key"])
				}
			} else {
				// Verify human-readable format
				if !strings.Contains(output, "test message") {
					t.Errorf("expected 'test message' in output, got: %s", output)
				}
			}
		})
	}
}

func TestLoggerWithRequestContext(t *testing.T) {
	tests := []struct {
		name       string
		ctx        context.Context
		wantFields map[string]string
	}{
		{
			name: "request with ID",
			ctx:  ContextWithRequestID(context.Background(), "req-123"),
			wantFields: map[string]string{
				"request_id": "req-123",
			},
		},
		{
			name: "request with user ID",
			ctx:  ContextWithUserID(context.Background(), "user-456"),
			wantFields: map[string]string{
				"user_id": "user-456",
			},
		},
		{
			name: "request with video ID",
			ctx:  ContextWithVideoID(context.Background(), "video-789"),
			wantFields: map[string]string{
				"video_id": "video-789",
			},
		},
		{
			name: "request with IP address",
			ctx:  ContextWithIP(context.Background(), "192.168.1.1"),
			wantFields: map[string]string{
				"ip": "192.168.1.1",
			},
		},
		{
			name: "request with all fields",
			ctx: ContextWithIP(
				ContextWithVideoID(
					ContextWithUserID(
						ContextWithRequestID(context.Background(), "req-123"),
						"user-456",
					),
					"video-789",
				),
				"192.168.1.1",
			),
			wantFields: map[string]string{
				"request_id": "req-123",
				"user_id":    "user-456",
				"video_id":   "video-789",
				"ip":         "192.168.1.1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger("production", "info", &buf)

			LoggerFromContext(tt.ctx, logger).Info("test message")

			var logEntry map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
				t.Fatalf("failed to parse JSON log: %v", err)
			}

			for key, expectedValue := range tt.wantFields {
				if gotValue, ok := logEntry[key]; !ok {
					t.Errorf("expected field %s not found in log", key)
				} else if gotValue != expectedValue {
					t.Errorf("field %s: expected %q, got %q", key, expectedValue, gotValue)
				}
			}
		})
	}
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name      string
		logLevel  string
		logFunc   func(*slog.Logger, string)
		shouldLog bool
	}{
		{
			name:      "debug logged when level is debug",
			logLevel:  "debug",
			logFunc:   func(l *slog.Logger, msg string) { l.Debug(msg) },
			shouldLog: true,
		},
		{
			name:      "debug not logged when level is info",
			logLevel:  "info",
			logFunc:   func(l *slog.Logger, msg string) { l.Debug(msg) },
			shouldLog: false,
		},
		{
			name:      "info logged when level is info",
			logLevel:  "info",
			logFunc:   func(l *slog.Logger, msg string) { l.Info(msg) },
			shouldLog: true,
		},
		{
			name:      "warn logged when level is warn",
			logLevel:  "warn",
			logFunc:   func(l *slog.Logger, msg string) { l.Warn(msg) },
			shouldLog: true,
		},
		{
			name:      "info not logged when level is warn",
			logLevel:  "warn",
			logFunc:   func(l *slog.Logger, msg string) { l.Info(msg) },
			shouldLog: false,
		},
		{
			name:      "error logged when level is error",
			logLevel:  "error",
			logFunc:   func(l *slog.Logger, msg string) { l.Error(msg) },
			shouldLog: true,
		},
		{
			name:      "warn not logged when level is error",
			logLevel:  "error",
			logFunc:   func(l *slog.Logger, msg string) { l.Warn(msg) },
			shouldLog: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger("production", tt.logLevel, &buf)

			tt.logFunc(logger, "test message")

			output := buf.String()
			hasOutput := len(output) > 0

			if hasOutput != tt.shouldLog {
				t.Errorf("expected shouldLog=%v, got output: %s", tt.shouldLog, output)
			}
		})
	}
}

func TestStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Log with various structured fields
	logger.Info("operation completed",
		"duration_ms", 1234,
		"status_code", 200,
		"method", "POST",
		"path", "/api/v1/videos",
		"size_bytes", 1048576,
		"success", true,
	)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	expectedFields := map[string]interface{}{
		"msg":         "operation completed",
		"duration_ms": float64(1234),
		"status_code": float64(200),
		"method":      "POST",
		"path":        "/api/v1/videos",
		"size_bytes":  float64(1048576),
		"success":     true,
	}

	for key, expectedValue := range expectedFields {
		if gotValue, ok := logEntry[key]; !ok {
			t.Errorf("expected field %s not found", key)
		} else if gotValue != expectedValue {
			t.Errorf("field %s: expected %v, got %v", key, expectedValue, gotValue)
		}
	}
}

func TestSecurityRedaction(t *testing.T) {
	tests := []struct {
		name         string
		logData      map[string]interface{}
		redactedKeys []string
	}{
		{
			name: "password redacted",
			logData: map[string]interface{}{
				"username": "testuser",
				"password": "secret123",
			},
			redactedKeys: []string{"password"},
		},
		{
			name: "token redacted",
			logData: map[string]interface{}{
				"user_id": "123",
				"token":   "bearer-xyz",
			},
			redactedKeys: []string{"token"},
		},
		{
			name: "access_token redacted",
			logData: map[string]interface{}{
				"user_id":      "123",
				"access_token": "at_xyz",
			},
			redactedKeys: []string{"access_token"},
		},
		{
			name: "refresh_token redacted",
			logData: map[string]interface{}{
				"user_id":       "123",
				"refresh_token": "rt_xyz",
			},
			redactedKeys: []string{"refresh_token"},
		},
		{
			name: "api_key redacted",
			logData: map[string]interface{}{
				"service": "stripe",
				"api_key": "sk_live_xyz",
			},
			redactedKeys: []string{"api_key"},
		},
		{
			name: "secret redacted",
			logData: map[string]interface{}{
				"config": "app",
				"secret": "my-secret",
			},
			redactedKeys: []string{"secret"},
		},
		{
			name: "authorization header redacted",
			logData: map[string]interface{}{
				"method":        "GET",
				"authorization": "Bearer token123",
			},
			redactedKeys: []string{"authorization"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger("production", "info", &buf)

			// Convert map to slog attributes
			attrs := make([]interface{}, 0, len(tt.logData)*2)
			for k, v := range tt.logData {
				attrs = append(attrs, k, v)
			}

			logger.Info("test message", attrs...)

			output := buf.String()

			// Check that sensitive values are redacted
			for _, key := range tt.redactedKeys {
				originalValue := tt.logData[key].(string)
				if strings.Contains(output, originalValue) {
					t.Errorf("sensitive value for %s not redacted: %s", key, output)
				}

				// Should contain redacted marker
				if !strings.Contains(output, "[REDACTED]") && !strings.Contains(output, "***") {
					t.Errorf("expected redaction marker for %s in: %s", key, output)
				}
			}
		})
	}
}

func TestErrorLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	testErr := &customError{
		message: "database connection failed",
		code:    "DB_CONN_ERR",
	}

	logger.Error("operation failed",
		"error", testErr,
		"error_code", testErr.code,
		"retryable", true,
	)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	if logEntry["level"] != "ERROR" {
		t.Errorf("expected level=ERROR, got: %v", logEntry["level"])
	}

	if !strings.Contains(logEntry["error"].(string), "database connection failed") {
		t.Errorf("error message not found in log: %v", logEntry["error"])
	}

	if logEntry["error_code"] != "DB_CONN_ERR" {
		t.Errorf("expected error_code=DB_CONN_ERR, got: %v", logEntry["error_code"])
	}
}

func TestGlobalLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	SetGlobalLogger(logger)
	globalLogger := GetGlobalLogger()

	if globalLogger == nil {
		t.Fatal("global logger is nil")
	}

	globalLogger.Info("global test")

	output := buf.String()
	if !strings.Contains(output, "global test") {
		t.Errorf("global logger did not log message: %s", output)
	}
}

func TestLoggerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	logger.WithGroup("http").Info("request received",
		"method", "GET",
		"path", "/api/v1/videos",
	)

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	// Check if fields are grouped
	if httpGroup, ok := logEntry["http"].(map[string]interface{}); ok {
		if httpGroup["method"] != "GET" {
			t.Errorf("expected grouped method=GET, got: %v", httpGroup["method"])
		}
		if httpGroup["path"] != "/api/v1/videos" {
			t.Errorf("expected grouped path=/api/v1/videos, got: %v", httpGroup["path"])
		}
	} else {
		t.Errorf("expected http group in log entry")
	}
}

func TestLoggerWith(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	// Create a logger with common fields
	videoLogger := logger.With("video_id", "vid-123", "user_id", "user-456")

	videoLogger.Info("video uploaded")

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	if logEntry["video_id"] != "vid-123" {
		t.Errorf("expected video_id=vid-123, got: %v", logEntry["video_id"])
	}

	if logEntry["user_id"] != "user-456" {
		t.Errorf("expected user_id=user-456, got: %v", logEntry["user_id"])
	}
}

func TestTimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	logger.Info("test message")

	var logEntry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	// Check timestamp exists and is valid RFC3339
	if timestamp, ok := logEntry["time"].(string); ok {
		if timestamp == "" {
			t.Error("timestamp is empty")
		}
		// Could add RFC3339 parsing validation here
	} else {
		t.Error("timestamp field not found or not a string")
	}
}

func BenchmarkLogger(b *testing.B) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info("benchmark message",
			"request_id", "req-123",
			"user_id", "user-456",
			"duration_ms", 100,
			"status_code", 200,
		)
	}
}

func BenchmarkLoggerWithContext(b *testing.B) {
	var buf bytes.Buffer
	logger := NewLogger("production", "info", &buf)
	ctx := ContextWithRequestID(
		ContextWithUserID(context.Background(), "user-456"),
		"req-123",
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LoggerFromContext(ctx, logger).Info("benchmark message",
			"duration_ms", 100,
			"status_code", 200,
		)
	}
}

// Helper types for testing
type customError struct {
	message string
	code    string
}

func (e *customError) Error() string {
	return e.message
}
