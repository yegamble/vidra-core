package messaging

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// TestAttachmentLimitsAreDecidedValues locks the DECIDED (2026-07-07) Messenger-
// parity numbers so a stray edit cannot silently loosen them (messaging-v2.md D6).
func TestAttachmentLimitsAreDecidedValues(t *testing.T) {
	if MaxAttachmentBytes != 104857600 {
		t.Errorf("MaxAttachmentBytes = %d, want 104857600 (100 MiB)", MaxAttachmentBytes)
	}
	if MaxAttachmentsPerMessage != 30 {
		t.Errorf("MaxAttachmentsPerMessage = %d, want 30", MaxAttachmentsPerMessage)
	}
}

// TestAttachmentDocKind proves the office-document MIME allowlist maps to the new
// coarse kind "doc", including the filename-extension fallback, and that a truly
// unsupported type is still rejected (415-shaped).
func TestAttachmentDocKind(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := newAttachService(t, repo, fakeScanner{clean: true}, nil)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	cases := []struct{ filename, contentType string }{
		{"a.doc", "application/msword"},
		{"a.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"a.ppt", "application/vnd.ms-powerpoint"},
		{"a.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"a.xls", "application/vnd.ms-excel"},
		{"a.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		// Filename-extension fallback when the client sent a generic content type.
		{"report.docx", "application/octet-stream"},
	}
	for _, tc := range cases {
		att, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
			Filename: tc.filename, ContentType: tc.contentType, Size: 3, Reader: strings.NewReader("doc"),
		})
		if err != nil {
			t.Fatalf("upload %s (%s): %v", tc.filename, tc.contentType, err)
		}
		if att.Kind != "doc" {
			t.Errorf("upload %s (%s) kind = %q, want doc", tc.filename, tc.contentType, att.Kind)
		}
	}

	// A stray type with a non-office extension is still unsupported.
	if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "a.tar", ContentType: "application/x-tar", Size: 1, Reader: strings.NewReader("x"),
	}); !errors.Is(err, ErrUnsupportedAttachment) {
		t.Errorf("tar upload err = %v, want ErrUnsupportedAttachment", err)
	}
}

// TestAttachmentDocKindScannedFailClosed proves the doc-kind path is malware
// scanned fail-closed BEFORE the attachment row is created (so it can never become
// linkable). This is the untagged unit-level cover for the EICAR-in-docx case
// (the real-clamd variant is in eicar_integration_test.go). An infection or a scan
// error both reject with ErrAttachmentRejected and persist no row.
func TestAttachmentDocKindScannedFailClosed(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	ctx := context.Background()
	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	for _, tc := range []struct {
		name    string
		scanner fakeScanner
	}{
		{"infected", fakeScanner{clean: false}},
		{"scan-error", fakeScanner{clean: true, err: errors.New("clamd down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo(ada, bob)
			svc := newAttachService(t, repo, tc.scanner, nil)
			conv := startConv(t, svc, ada, bob)
			if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
				Filename: "macro.docx", ContentType: docx, Size: 4, Reader: strings.NewReader("EVIL"),
			}); !errors.Is(err, ErrAttachmentRejected) {
				t.Fatalf("docx %s err = %v, want ErrAttachmentRejected", tc.name, err)
			}
			// Scan runs before CreateMessageAttachment → no row persisted, so the
			// rejected upload can never be linked to a message.
			if len(repo.attachments) != 0 {
				t.Fatalf("rejected docx left %d attachment rows, want 0 (scan must precede linkability)", len(repo.attachments))
			}
		})
	}
}

// repeatReader yields exactly n bytes without allocating a large buffer, so the
// size-cap boundary can be exercised on an injected small limit (the real 100 MiB
// value is asserted by TestAttachmentLimitsAreDecidedValues; test-race stays fast).
type repeatReader struct{ remaining int64 }

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.remaining -= int64(n)
	return n, nil
}

// TestAttachmentSizeCapBoundary proves the per-file cap enforcement: a file at the
// cap is accepted (201) and one byte over is rejected (413/ErrAttachmentTooLarge),
// even when the client under-declares Size. Uses an injected 16-byte limit so the
// same code path is exercised without moving 100 MiB.
func TestAttachmentSizeCapBoundary(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	const cap16 = int64(16)
	svc := NewService(repo, WithAttachments(blobs, fakeScanner{clean: true}, cap16))
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	// Exactly at the cap → accepted (a lying Size of 0 must not bypass the check).
	if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "at.pdf", ContentType: "application/pdf", Size: 0, Reader: &repeatReader{remaining: cap16},
	}); err != nil {
		t.Fatalf("at-cap upload err = %v, want nil", err)
	}
	// One byte over → 413.
	if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename: "over.pdf", ContentType: "application/pdf", Size: 0, Reader: &repeatReader{remaining: cap16 + 1},
	}); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Errorf("over-cap upload err = %v, want ErrAttachmentTooLarge", err)
	}
}

// TestSendRejectsTooManyAttachments proves the per-message count cap: 31 referenced
// attachment ids is rejected up front (ErrInvalidAttachments) before any message
// row is created.
func TestSendRejectsTooManyAttachments(t *testing.T) {
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	svc := NewService(repo)
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	ids := make([]uuid.UUID, MaxAttachmentsPerMessage+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := svc.SendMessage(ctx, ada, conv, "too many", ids); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("31-attachment send err = %v, want ErrInvalidAttachments", err)
	}
	if len(repo.messages) != 0 {
		t.Errorf("a rejected over-count send created %d messages, want 0", len(repo.messages))
	}
}
