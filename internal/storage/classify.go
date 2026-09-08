package storage

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"syscall"

	"github.com/minio/minio-go/v7"
)

// ErrorClass is what an operator can DO about a storage failure, which is a
// much smaller set than what a provider can say about one.
//
// It exists because the provider's own sentence is the only thing this codebase
// had, and that sentence is unusable in two directions at once. Toward the
// client it is a leak: `s3: put "web-videos/<uuid>.mp4": Access Denied.` names a
// bucket layout, a video id and a vendor. Toward the operator it is invisible:
// 503 and 500 are 5xx, and the central error handler scrubs every 5xx message it
// has no stable code for down to "an unexpected error occurred" (see
// internal/httpapi/errors.go), so A24 measured a write-denied credential and a
// full bucket arriving at the uploader as the SAME bare 500.
//
// Three classes, because three are the distinct fixes: a permission to grant, a
// bill or a broom, and a network/endpoint to repair.
type ErrorClass string

const (
	// ClassWriteDenied: the store answered, and refused to accept the write.
	// The credential can usually still READ — which is exactly why this failure
	// hides: EnsureBucket (HeadBucket), the ownership-marker read and the
	// emptiness list all pass on a read-only key. See WriteProbePrefix.
	ClassWriteDenied ErrorClass = "write_denied"
	// ClassQuotaExceeded: the store accepted the request and had nowhere to put
	// it — a bucket quota, a hard filesystem limit (ENOSPC) or a user quota
	// (EDQUOT). Nothing is wrong with the credentials or the network.
	ClassQuotaExceeded ErrorClass = "quota_exceeded"
	// ClassUnreachable: the store could not be asked at all, or answered that
	// the destination itself is not there (NoSuchBucket, a 5xx, a dial failure,
	// a DNS failure, a deadline).
	ClassUnreachable ErrorClass = "unreachable"
)

// Error is a storage failure that has been classified at the backend boundary,
// which is the only place that can see the provider's answer.
//
// It WRAPS rather than replaces: Error() renders the underlying sentence with
// the class appended, so an operator log line keeps everything it had and gains
// the one word the API response is built from — and the worker's bounded-retry
// log line (internal/jobtrace) names the class for free, through the redaction
// it already applies. The client-facing text is NOT built from this string; the
// HTTP layer branches on Class and renders a fixed sentence per class, so no
// bucket name, object key or vendor phrase can reach a response body.
type Error struct {
	// Class is the operator-facing category. Never empty on a value this
	// package constructs.
	Class ErrorClass
	// Op is the backend operation that failed ("put", "delete", "open",
	// "stat", "list", "probe"). It is safe to render: it names no object.
	Op string
	// Err is the wrapped cause, provider text and all. It stays wrapped so
	// errors.Is on the sentinels below still works through a classified error.
	Err error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return "storage: " + e.Op + ": " + string(e.Class)
	}
	return e.Err.Error() + " [" + string(e.Class) + "]"
}

// Unwrap keeps errors.Is/As transparent through the classification, so
// ErrNotFound and ErrInvalidKey still match after a wrap.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ClassOf reports the class of the first classified storage failure in err's
// chain. It is the seam every consumer branches on — the HTTP error handler, the
// boot write probe and the worker's claim gate — so none of them has to parse a
// provider's prose.
func ClassOf(err error) (ErrorClass, bool) {
	var se *Error
	if errors.As(err, &se) && se != nil && se.Class != "" {
		return se.Class, true
	}
	return "", false
}

// classify wraps err with class when the cause is one this package recognises,
// and returns err untouched when it is not. Returning the original error for an
// unrecognised cause is deliberate: a class nobody can act on would send an
// operator to the wrong place, and an unclassified 500 is the honest answer for
// a failure nothing here understands.
func classify(op string, err error, class func(error) (ErrorClass, bool)) error {
	if err == nil {
		return nil
	}
	// Never classify the two sentinels: a missing object and a rejected key are
	// ordinary, expected answers, not a store that cannot serve.
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidKey) {
		return err
	}
	c, ok := class(err)
	if !ok {
		return err
	}
	return &Error{Class: c, Op: op, Err: err}
}

// classifyS3 classifies an S3-compatible store's answer.
func classifyS3(op string, err error) error { return classify(op, err, s3Class) }

// classifyLocal classifies the local filesystem's answer.
func classifyLocal(op string, err error) error { return classify(op, err, localClass) }

// s3Class maps an S3 answer to a class.
//
// Two passes on purpose. The CODE pass is the reliable one where a provider
// sends a real S3 error code. The MESSAGE pass exists because bucket quotas are
// the least standardised answer in the S3 API — MinIO reports
// XMinioAdminBucketQuotaExceeded with the sentence "Bucket quota exceeded"
// (A24), other stores use QuotaExceeded, and some only say it in prose — and
// matching both means a full bucket is never mistaken for a permissions
// problem, which is the mix-up that costs an operator the most time. Only then
// does the STATUS pass generalise, so a quota answer carrying a 403 cannot be
// swallowed by the forbidden-means-denied rule above it.
func s3Class(err error) (ErrorClass, bool) {
	// Cancellation and deadlines first: the SDK wraps them, and a store that
	// never answered is unreachable regardless of what else is in the chain.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ClassUnreachable, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ClassUnreachable, true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ClassUnreachable, true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return ClassUnreachable, true
	}

	// errors.As, NOT minio.ToErrorResponse: that helper is a bare type SWITCH
	// with no unwrapping, so it answers an empty ErrorResponse for every error
	// this package has already wrapped with its own op/key context — which is
	// every error a caller ever sees. minio.ErrorResponse has a value-receiver
	// Error(), so it is reachable through any %w chain.
	var resp minio.ErrorResponse
	if !errors.As(err, &resp) {
		return "", false
	}
	switch resp.Code {
	case "QuotaExceeded", "XMinioAdminBucketQuotaExceeded", "MaxBucketQuotaExceeded":
		return ClassQuotaExceeded, true
	case "AccessDenied", "AllAccessDisabled", "UnauthorizedAccess", "InvalidAccessKeyId",
		"SignatureDoesNotMatch", "AccountProblem", "InvalidObjectState", "NotEntitled":
		return ClassWriteDenied, true
	case "NoSuchBucket", "InvalidBucketName", "PermanentRedirect", "AuthorizationHeaderMalformed":
		return ClassUnreachable, true
	case "SlowDown", "ServiceUnavailable", "InternalError", "RequestTimeout":
		return ClassUnreachable, true
	}

	if strings.Contains(strings.ToLower(resp.Message+" "+err.Error()), "quota exceeded") {
		return ClassQuotaExceeded, true
	}

	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized, http.StatusMethodNotAllowed:
		return ClassWriteDenied, true
	case http.StatusInsufficientStorage:
		return ClassQuotaExceeded, true
	case http.StatusNotFound, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// A 404 HERE is never a missing object — classify() has already let
		// ErrNotFound past — so it is the BUCKET that is not there.
		return ClassUnreachable, true
	}
	return "", false
}

// localClass maps a filesystem answer to the same three classes, so a
// single-node install on STORAGE_LOCAL_ROOT gets the same refusals as an S3 one.
// A read-only mount and a volume owned by another uid are the local shape of a
// read-only credential, and they are just as invisible without this.
func localClass(err error) (ErrorClass, bool) {
	switch {
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		return ClassWriteDenied, true
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT), errors.Is(err, syscall.EFBIG):
		return ClassQuotaExceeded, true
	}
	return "", false
}
