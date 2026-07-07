//go:build integration

// This test exercises a REAL clamd daemon and so is excluded from the default
// `make ci` gate (which must stay green on hosts without clamd). Bring up the
// compose `scan` profile and point CLAMAV_TEST_ADDR at it:
//
//	docker compose --profile scan up -d clamav
//	CLAMAV_TEST_ADDR=localhost:3310 go test -tags integration ./internal/messaging/
//
// The clamav/clamav image's first boot downloads signature databases (freshclam);
// give it up to ~2 minutes before the daemon accepts scans. The untagged unit
// test TestAttachmentDocKindScannedFailClosed covers the same fail-closed doc-kind
// path with a fake scanner and runs in `make ci`.
package messaging

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// eicarTestString is the standard, harmless anti-malware test file. Assembled from
// fragments so this source file itself never contains the contiguous signature.
var eicarTestString = strings.Join([]string{
	`X5O!P%@AP[4\PZX54(P^)7CC)7}`,
	`$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`,
}, "")

// eicarDocx builds a minimal .docx (a zip container) with the EICAR test string
// embedded as a member entry, so a real clamd's archive scanning detects it —
// proving an EICAR payload INSIDE a docx is rejected end-to-end.
func eicarDocx(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(eicarTestString)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestAttachmentEICARInDocxRejected proves the D6 doc-kind upload path is scanned
// fail-closed against a REAL clamd: an EICAR payload inside a .docx is rejected
// (ErrAttachmentRejected) and leaves no linkable attachment row.
func TestAttachmentEICARInDocxRejected(t *testing.T) {
	addr := os.Getenv("CLAMAV_TEST_ADDR")
	if addr == "" {
		t.Skip("CLAMAV_TEST_ADDR not set; skipping real-clamd EICAR-in-docx test")
	}
	ada, bob := uuid.New(), uuid.New()
	repo := newFakeRepo(ada, bob)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	// 2 minutes tolerates a freshly-booted clamd still loading its databases.
	scanner := media.NewClamAV(addr, blobs, 2*time.Minute)
	svc := NewService(repo, WithAttachments(blobs, scanner, 0))
	ctx := context.Background()
	conv := startConv(t, svc, ada, bob)

	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if _, err := svc.UploadAttachment(ctx, ada, conv, AttachmentInput{
		Filename:    "macro.docx",
		ContentType: docx,
		Size:        0,
		Reader:      bytes.NewReader(eicarDocx(t)),
	}); !errors.Is(err, ErrAttachmentRejected) {
		t.Fatalf("EICAR-in-docx upload err = %v, want ErrAttachmentRejected", err)
	}
	if len(repo.attachments) != 0 {
		t.Fatalf("infected docx left %d attachment rows, want 0", len(repo.attachments))
	}
}
