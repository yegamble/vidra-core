package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// TestRequestLogIncludesTraceID asserts the per-request log line carries the
// active span's trace_id/span_id (the logs↔traces correlation). We inject a span
// context onto the request as the OTel middleware would, so the test needs no
// exporter or collector.
func TestRequestLogIncludesTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	srv := New(testConfig(), nil, nil, WithLogger(logger))

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req = req.WithContext(trace.ContextWithSpanContext(req.Context(), sc))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var found bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if m["msg"] == "request" {
			found = true
			if m["trace_id"] != "0102030405060708090a0b0c0d0e0f10" {
				t.Errorf("trace_id = %v, want the injected trace id", m["trace_id"])
			}
			if m["span_id"] != "0102030405060708" {
				t.Errorf("span_id = %v, want the injected span id", m["span_id"])
			}
		}
	}
	if !found {
		t.Errorf("no request log line emitted; logs:\n%s", buf.String())
	}
}
