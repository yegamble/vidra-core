package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ratelimit"
)

// TestDMAttachmentDocKind proves the D6 office-document allowlist end-to-end: a
// .docx upload is accepted (201) with kind "doc", and a stray type is still 415.
func TestDMAttachmentDocKind(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, _, _, convID := startDMConversation(t, srv)

	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	rec := uploadAttachment(srv, convID, "report.docx", docx, "PK\x03\x04docx-bytes", adaTok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("docx upload = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var up uploadAttachmentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &up)
	if up.Kind != "doc" {
		t.Fatalf("docx kind = %q, want doc", up.Kind)
	}

	// A spreadsheet too (different office MIME → same coarse kind).
	const xlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	if r := uploadAttachment(srv, convID, "book.xlsx", xlsx, "PK\x03\x04xlsx", adaTok); r.Code != http.StatusCreated {
		t.Fatalf("xlsx upload = %d, want 201; body=%s", r.Code, r.Body.String())
	}

	// Stray type stays 415.
	if r := uploadAttachment(srv, convID, "a.tar", "application/x-tar", "x", adaTok); r.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("tar upload = %d, want 415; body=%s", r.Code, r.Body.String())
	}
}

// TestDMSendTooManyAttachments proves the per-message count cap is a 422 field
// error at the request-validation boundary (before any service call).
func TestDMSendTooManyAttachments(t *testing.T) {
	srv := videoServer(t)
	adaTok, _, _, _, convID := startDMConversation(t, srv)

	ids := make([]string, 31)
	for i := range ids {
		ids[i] = `"` + uuid.NewString() + `"`
	}
	body := `{"body":"too many","attachment_ids":[` + strings.Join(ids, ",") + `]}`
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/conversations/"+convID+"/messages", body, adaTok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("31-attachment send = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// fakeAttachCounter is an in-memory fixed-window counter for the rate-limit test.
type fakeAttachCounter struct{ counts map[string]int64 }

func (f *fakeAttachCounter) Incr(_ context.Context, key string, w time.Duration) (int64, time.Duration, error) {
	if f.counts == nil {
		f.counts = map[string]int64{}
	}
	f.counts[key]++
	return f.counts[key], w, nil
}

// invokeAttachLimit runs the attachment rate-limit middleware for one user id and
// returns the resulting status code and the Retry-After header.
func invokeAttachLimit(s *Server, e *echo.Echo, id uuid.UUID) (int, string) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/x/attachments", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(ctxKeyUserID, id)
	c.Set(ctxKeyRole, "user")
	h := s.attachmentUploadRateLimit()(func(c echo.Context) error { return c.NoContent(http.StatusCreated) })
	if err := h(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec.Code, rec.Header().Get("Retry-After")
}

// TestAttachmentUploadRateLimit proves the per-user upload limiter: the (N+1)-th
// upload in a window is 429 with Retry-After, and a different user has an
// independent budget (keyed per user, not per IP).
func TestAttachmentUploadRateLimit(t *testing.T) {
	lim := ratelimit.NewLimiter(&fakeAttachCounter{}, 2, time.Minute)
	s := &Server{attachmentLimit: lim, logger: slog.Default()}
	e := echo.New()
	ada, bob := uuid.New(), uuid.New()

	// Ada: two allowed, the third denied.
	if code, _ := invokeAttachLimit(s, e, ada); code != http.StatusCreated {
		t.Fatalf("ada upload #1 = %d, want 201", code)
	}
	if code, _ := invokeAttachLimit(s, e, ada); code != http.StatusCreated {
		t.Fatalf("ada upload #2 = %d, want 201", code)
	}
	code, retry := invokeAttachLimit(s, e, ada)
	if code != http.StatusTooManyRequests {
		t.Fatalf("ada upload #3 = %d, want 429", code)
	}
	if retry == "" {
		t.Error("429 missing Retry-After header")
	}

	// Bob's budget is independent (different key).
	if code, _ := invokeAttachLimit(s, e, bob); code != http.StatusCreated {
		t.Fatalf("bob upload #1 = %d, want 201 (independent per-user budget)", code)
	}
}

// TestAttachmentUploadRateLimitNoopWhenUnset proves the middleware is a passthrough
// when no attachment limiter is configured (the unit-test default), so it never
// interferes with servers that do not wire it.
func TestAttachmentUploadRateLimitNoopWhenUnset(t *testing.T) {
	s := &Server{logger: slog.Default()}
	e := echo.New()
	for i := 0; i < 100; i++ {
		if code, _ := invokeAttachLimit(s, e, uuid.New()); code != http.StatusCreated {
			t.Fatalf("passthrough upload #%d = %d, want 201", i, code)
		}
	}
}
