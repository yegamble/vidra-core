package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

// fakeStorageWrite pins a write verdict without a store.
type fakeStorageWrite struct{ st storage.WriteStatus }

func (f fakeStorageWrite) Status() storage.WriteStatus { return f.st }

// A24's first finding, as a unit test: a write-denied credential and a full
// bucket both reached the uploader as `500 internal_error / "an unexpected error
// occurred"`, because 503 and 500 are both 5xx and the central handler scrubs
// every 5xx message it has no stable code for. The class is what makes the
// difference actionable — "grant the key write permission" and "free some space"
// are not the same instruction — and it has to survive the scrubber to be worth
// anything.
func TestClassifiedStorageRefusalsAnswer503WithTheirClass(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	_ = srv.Handler()

	for _, tc := range []struct {
		name    string
		class   storage.ErrorClass
		mustSay string
	}{
		{"write denied", storage.ClassWriteDenied, "not write to it"},
		{"quota exceeded", storage.ClassQuotaExceeded, "no room left"},
		{"unreachable", storage.ClassUnreachable, "cannot reach its media store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The shape a real refusal arrives in: the backend's own wrapped
			// sentence, key and provider text and all.
			err := &storage.Error{
				Class: tc.class,
				Op:    "put",
				Err:   fmt.Errorf(`storage: s3: put %q: Access Denied.`, "web-videos/2f1c6a9e-0000-4000-8000-000000000000.mp4"),
			}
			rec := httptest.NewRecorder()
			c := srv.echo.NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/videos/x/file", nil), rec)
			srv.echo.HTTPErrorHandler(err, c)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil {
				t.Fatalf("decode: %v (body=%s)", jerr, rec.Body.String())
			}
			if body.Error.Code != "storage_unavailable" {
				t.Errorf("code = %q, want storage_unavailable", body.Error.Code)
			}
			if !strings.Contains(body.Error.Message, tc.mustSay) {
				t.Errorf("message = %q; it must say %q", body.Error.Message, tc.mustSay)
			}
			if strings.Contains(body.Error.Message, "unexpected error") {
				t.Errorf("the scrubber ate the message: %q", body.Error.Message)
			}
			// The client-facing half must carry nothing about the store.
			for _, leak := range []string{"web-videos", "2f1c6a9e", "s3", "Access Denied", ".mp4"} {
				if strings.Contains(body.Error.Message, leak) {
					t.Errorf("the response body leaks %q: %s", leak, body.Error.Message)
				}
			}
		})
	}
}

// The other half of A24's finding: the api's request logger wrote the FULL
// storage key into its error line, where the worker's job logger writes
// `[redacted-key]` for the very same failure. The seam is reused, not forked.
func TestFiveHundredLogLineRedactsTheStorageKey(t *testing.T) {
	var buf bytes.Buffer
	srv := New(testConfig(), nil, nil, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
	_ = srv.Handler()

	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(httptest.NewRequest(http.MethodPost, "/api/v1/videos/x/file", nil), rec)
	srv.echo.HTTPErrorHandler(fmt.Errorf(`storage: s3: put %q: Access Denied.`,
		"web-videos/2f1c6a9e-0000-4000-8000-000000000000.mp4"), c)

	line := buf.String()
	if line == "" {
		t.Fatal("a 500 produced no operator log line at all")
	}
	if strings.Contains(line, "web-videos/2f1c6a9e") {
		t.Errorf("the api log line still carries the full storage key: %s", line)
	}
	if !strings.Contains(line, "[redacted-key]") {
		t.Errorf("the key was neither kept nor redacted through the shared seam: %s", line)
	}
	// The cause must survive the redaction, or the line is useless.
	if !strings.Contains(line, "Access Denied") {
		t.Errorf("the redaction ate the cause: %s", line)
	}
}

// The storage component: down when the store will not take a write, and — per
// this repo's readiness convention, where only PostgreSQL takes an instance out
// of rotation — degraded-with-200 rather than a 503.
func TestStorageComponentIsDownButDoesNotFailReadiness(t *testing.T) {
	srv := New(testConfig(), nil, nil, WithStorageWriteHealth(fakeStorageWrite{st: storage.WriteStatus{
		Probed: true,
		Class:  storage.ClassWriteDenied,
	}}))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a store that refuses writes still serves every read, and 503ing on a SHARED bucket's refusal would empty every replica out of rotation at once", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if got := body.Components["storage"].Status; got != "down" {
		t.Errorf("storage component = %q, want down", got)
	}
	if body.Status != "degraded" {
		t.Errorf("overall = %q, want degraded — a body that reads ok beside a down component is one an operator scans past", body.Status)
	}
	if msg := body.Components["storage"].Error; !strings.Contains(msg, "not write to it") {
		t.Errorf("the down component says %q, which does not name the fix", msg)
	}
}

func TestStorageComponentStatesThatAreNotFaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		health storageWriteHealth
		want   string
	}{
		{"never wired", nil, "not_configured"},
		{"wired, not yet probed", fakeStorageWrite{}, "not_configured"},
		{"writable", fakeStorageWrite{st: storage.WriteStatus{Probed: true, OK: true}}, "ok"},
		{"writable but cannot delete", fakeStorageWrite{st: storage.WriteStatus{Probed: true, OK: true, Leaked: true}}, "degraded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := []Option{}
			if tc.health != nil {
				opts = append(opts, WithStorageWriteHealth(tc.health))
			}
			srv := New(testConfig(), nil, nil, opts...)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			var body readinessResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if got := body.Components["storage"].Status; got != tc.want {
				t.Errorf("storage component = %q, want %q", got, tc.want)
			}
		})
	}
}
