package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/minio/minio-go/v7"
)

// s3Err builds the error shape minio-go hands back for a server answer, which is
// what the classifier actually sees in production.
func s3Err(code, message string, status int) error {
	return minio.ErrorResponse{Code: code, Message: message, StatusCode: status}
}

func TestS3ClassCoversTheThreeOperatorAnswers(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"access denied", s3Err("AccessDenied", "Access Denied.", http.StatusForbidden), ClassWriteDenied},
		{"invalid key id", s3Err("InvalidAccessKeyId", "The access key ID does not exist", http.StatusForbidden), ClassWriteDenied},
		{"bare 403", s3Err("", "", http.StatusForbidden), ClassWriteDenied},
		// A24 measured this exact sentence from MinIO when a bucket quota is hit.
		{"minio bucket quota", s3Err("XMinioAdminBucketQuotaExceeded", "Bucket quota exceeded", http.StatusBadRequest), ClassQuotaExceeded},
		{"quota by prose only", s3Err("", "Bucket quota exceeded", http.StatusBadRequest), ClassQuotaExceeded},
		{"quota wearing a 403", s3Err("QuotaExceeded", "quota exceeded", http.StatusForbidden), ClassQuotaExceeded},
		{"insufficient storage", s3Err("", "", http.StatusInsufficientStorage), ClassQuotaExceeded},
		{"no such bucket", s3Err("NoSuchBucket", "The specified bucket does not exist", http.StatusNotFound), ClassUnreachable},
		{"upstream 503", s3Err("", "", http.StatusServiceUnavailable), ClassUnreachable},
		{"dial refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, ClassUnreachable},
		{"dns failure", &net.DNSError{Err: "no such host", Name: "storage.example.invalid"}, ClassUnreachable},
		{"deadline", context.DeadlineExceeded, ClassUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := classifyS3("put", fmt.Errorf(`storage: s3: put %q: %w`, "web-videos/x.mp4", tc.err))
			got, ok := ClassOf(wrapped)
			if !ok {
				t.Fatalf("ClassOf(%v) reported no class; a refusal an operator cannot act on is the whole defect", tc.err)
			}
			if got != tc.want {
				t.Errorf("class = %q, want %q", got, tc.want)
			}
		})
	}
}

// An answer this package does not understand must pass through unclassified: a
// made-up class sends an operator to fix the wrong thing.
func TestS3ClassLeavesAnUnknownAnswerAlone(t *testing.T) {
	err := classifyS3("put", fmt.Errorf("storage: s3: put %q: %w", "web-videos/x.mp4", s3Err("TeapotError", "i am a teapot", http.StatusTeapot)))
	if _, ok := ClassOf(err); ok {
		t.Fatalf("an unrecognised provider answer was given a class: %v", err)
	}
}

// The two sentinels are ordinary answers, not a store that cannot serve. A
// classified ErrNotFound would turn every missing thumbnail into a 503.
func TestClassifyNeverClassifiesTheSentinels(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrInvalidKey} {
		got := classifyS3("open", fmt.Errorf("storage: s3: open %q: %w", "thumbnails/x.jpg", err))
		if _, ok := ClassOf(got); ok {
			t.Errorf("%v was classified", err)
		}
		if !errors.Is(got, err) {
			t.Errorf("errors.Is broke for %v", err)
		}
	}
}

// The wrapped error keeps the provider's sentence (the operator's half) and
// gains the class (the API's half); errors.Is still reaches through it.
func TestClassifiedErrorKeepsTheCauseAndNamesTheClass(t *testing.T) {
	cause := s3Err("AccessDenied", "Access Denied.", http.StatusForbidden)
	err := classifyS3("put", fmt.Errorf("storage: s3: put %q: %w", "web-videos/x.mp4", cause))
	if !strings.Contains(err.Error(), "Access Denied") {
		t.Errorf("the provider sentence was lost: %q", err)
	}
	if !strings.Contains(err.Error(), string(ClassWriteDenied)) {
		t.Errorf("the class is not in the message, so the worker's failure log cannot name it: %q", err)
	}
	if !errors.As(err, new(*Error)) {
		t.Error("errors.As could not reach the classified error")
	}
}

// The local backend's half of the same three classes: a media root on a
// read-only mount or owned by another uid is the local shape of a read-only S3
// credential, and a full disk is the local shape of a bucket quota.
func TestLocalClassMapsPosixRefusals(t *testing.T) {
	cases := []struct {
		name       string
		errno      error
		want       ErrorClass
		classified bool
	}{
		{"permission denied", syscall.EACCES, ClassWriteDenied, true},
		{"read-only filesystem", syscall.EROFS, ClassWriteDenied, true},
		{"no space left", syscall.ENOSPC, ClassQuotaExceeded, true},
		{"disk quota", syscall.EDQUOT, ClassQuotaExceeded, true},
		{"file too large", syscall.EFBIG, ClassQuotaExceeded, true},
		// Not a storage refusal: a caller asking for something impossible.
		{"not a directory", syscall.ENOTDIR, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyLocal("put", &os.PathError{Op: "open", Path: "/media/web-videos/x.mp4", Err: tc.errno})
			got, ok := ClassOf(err)
			if ok != tc.classified || got != tc.want {
				t.Fatalf("class = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.classified)
			}
		})
	}
}

// The wiring: Local.Put must route its own failures through the classifier, not
// merely have a classifier available.
//
// It deliberately does NOT skip when the mode bits do not bite (a root CI
// container, a filesystem that ignores them). A39's zero-silent-skips rule is
// the point: a test that quietly removes itself on the machine that matters is
// worse than one that asserts a little less there. When the write unexpectedly
// succeeds, the mapping is still asserted — from the errno the kernel would
// have produced — so this test always checks something.
func TestLocalPutClassifiesARefusedWrite(t *testing.T) {
	root := t.TempDir()
	l, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "web-videos")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, perr := l.Put(context.Background(), "web-videos/denied.mp4", strings.NewReader("x"))
	if perr == nil {
		t.Logf("this process can write through mode 0500 (uid %d); asserting the mapping instead of the syscall", os.Geteuid())
		if class, ok := ClassOf(classifyLocal("put", &os.PathError{Op: "open", Err: syscall.EACCES})); !ok || class != ClassWriteDenied {
			t.Fatalf("EACCES class = (%q, %v), want write_denied", class, ok)
		}
		return
	}
	class, ok := ClassOf(perr)
	if !ok || class != ClassWriteDenied {
		t.Fatalf("class = (%q, %v), want write_denied — an unwritable media root must not read as an unexplained 500", class, ok)
	}
}

// PutSized is the path every upload takes (storage.PutSized feature-detects it),
// so the classification has to survive it rather than only decorating Put.
func TestPutSizedClassifiesThroughTheHelper(t *testing.T) {
	b := newFailingBackend(t)
	b.putErr = &os.PathError{Op: "open", Path: "/media/x", Err: syscall.EACCES}
	body := strings.NewReader("x")
	_, err := PutSized(context.Background(), classifyingPutter{b}, "web-videos/x.mp4", body, int64(body.Len()))
	if _, ok := ClassOf(err); !ok {
		t.Fatalf("PutSized lost the class: %v", err)
	}
}

// classifyingPutter is the shape a backend has after this change: the class is
// attached at the boundary, not by the caller.
type classifyingPutter struct{ Backend }

func (c classifyingPutter) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	n, err := c.Backend.Put(ctx, key, r)
	return n, classifyLocal("put", err)
}
