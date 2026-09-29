package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type copyProtocol struct {
	t                                       *testing.T
	source, destination                     *S3
	copier                                  ServerCopier
	size, destinationSize                   int64
	headCode, copyCode, initCode, partCode  string
	completeCode, abortCode                 string
	destinationType, destinationETag        string
	sourceETag                              string
	sourceVersion                           string
	headRequests, copies, starts, completes int
	aborts, parts                           int
	cancel                                  context.CancelFunc
	backblaze                               bool
}

func newCopyProtocol(t *testing.T, size int64) *copyProtocol {
	t.Helper()
	f := &copyProtocol{t: t, size: size, destinationSize: size, destinationType: "video/mp4", destinationETag: "copied-etag", sourceETag: "source-etag", sourceVersion: "source-version"}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	for _, target := range []struct {
		bucket string
		out    **S3
	}{{"source-bucket", &f.source}, {"destination-bucket", &f.destination}} {
		b, err := NewS3(S3Config{Endpoint: strings.TrimPrefix(srv.URL, "http://"), Bucket: target.bucket,
			AccessKey: target.bucket, SecretKey: "test-secret", Region: "us-east-1", ForcePathStyle: true})
		if err != nil {
			t.Fatal(err)
		}
		*target.out = b
	}
	var err error
	f.copier, err = NewS3Copier(f.destination, "copy-access", "test-copy-secret")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *copyProtocol) serve(w http.ResponseWriter, r *http.Request) {
	errReply := func(code string) bool {
		if code == "" {
			return false
		}
		status := http.StatusBadRequest
		switch code {
		case "AccessDenied":
			status = http.StatusForbidden
		case "PreconditionFailed":
			status = http.StatusPreconditionFailed
		case "NoSuchKey":
			status = http.StatusNotFound
		case "NotImplemented":
			status = http.StatusNotImplemented
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, code)
		return true
	}
	if r.Method == http.MethodHead {
		f.headRequests++
		isSource := strings.HasPrefix(r.URL.Path, "/source-bucket/")
		credential, size, etag, mime := "destination-bucket", f.destinationSize, f.destinationETag, f.destinationType
		if isSource {
			credential, size, etag, mime = "source-bucket", f.size, f.sourceETag, "application/octet-stream"
			if errReply(f.headCode) {
				return
			}
		} else if r.URL.Query().Get("versionId") != "destination-version" {
			f.t.Errorf("destination verification did not pin returned version: %s", r.URL)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "Credential="+credential+"/") {
			f.t.Error("HEAD used the wrong credential")
		}
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("ETag", strconv.Quote(etag))
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("x-amz-version-id", f.sourceVersion)
		return
	}
	if r.Method == http.MethodGet {
		f.t.Error("server copy downloaded object bytes")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !strings.Contains(r.Header.Get("Authorization"), "Credential=copy-access/") {
		f.t.Error("copy request did not use dedicated credentials")
	}
	if r.Method == http.MethodDelete {
		f.aborts++
		if !errReply(f.abortCode) {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	if r.Method == http.MethodPost {
		if r.URL.Query().Has("uploads") {
			f.starts++
			if errReply(f.initCode) {
				return
			}
			if r.Header.Get("Content-Type") != "video/mp4" {
				f.t.Error("multipart initiation lost destination content type")
			}
			_, _ = fmt.Fprint(w, "<InitiateMultipartUploadResult><UploadId>upload-id</UploadId></InitiateMultipartUploadResult>")
		} else {
			f.completes++
			if errReply(f.completeCode) {
				return
			}
			w.Header().Set("x-amz-version-id", "destination-version")
			_, _ = fmt.Fprint(w, "<CompleteMultipartUploadResult><Bucket>destination-bucket</Bucket><Key>media.mp4</Key><ETag>\"copied-etag\"</ETag></CompleteMultipartUploadResult>")
		}
		return
	}
	if r.Method != http.MethodPut {
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	copySource, parseErr := url.Parse(r.Header.Get("x-amz-copy-source"))
	if parseErr != nil || copySource.Path != "source-bucket/media.mp4" || copySource.Query().Get("versionId") != f.sourceVersion || r.Header.Get("x-amz-copy-source-if-match") != `"source-etag"` {
		f.t.Error("copy did not pin the source version and ETag")
	}
	if r.URL.Query().Get("uploadId") != "" {
		f.parts++
		start := int64(f.parts-1) * (128 << 20)
		end := min(start+(128<<20), f.size) - 1
		if r.Header.Get("x-amz-copy-source-range") != fmt.Sprintf("bytes=%d-%d", start, end) {
			f.t.Errorf("incorrect part range: %s", r.Header.Get("x-amz-copy-source-range"))
		}
		if errReply(f.partCode) {
			return
		}
		if f.cancel != nil {
			f.cancel()
		}
		_, _ = fmt.Fprint(w, "<CopyPartResult><ETag>\"part-etag\"</ETag></CopyPartResult>")
		return
	}
	f.copies++
	if errReply(f.copyCode) {
		return
	}
	_, taggingPresent := r.Header["X-Amz-Tagging"]
	if f.backblaze && taggingPresent {
		f.t.Error("B2 rejects x-amz-tagging even when its value is empty")
	} else if !f.backblaze && r.Header.Get("x-amz-tagging-directive") != "REPLACE" {
		f.t.Error("generic S3 copy must discard source tags")
	}
	if r.Header.Get("x-amz-metadata-directive") != "REPLACE" || r.Header.Get("Content-Type") != "video/mp4" {
		f.t.Error("copy preserved untrusted source metadata")
	}
	w.Header().Set("x-amz-version-id", "destination-version")
	_, _ = fmt.Fprint(w, "<CopyObjectResult><ETag>\"copied-etag\"</ETag></CopyObjectResult>")
}

type copyTestTransport func(*http.Request) (*http.Response, error)

func (transport copyTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestServerCopyBackblazeOmitsUnsupportedTaggingHeader(t *testing.T) {
	for _, host := range []string{"s3.us-west-004.backblazeb2.com", "storage.example.com", "s3.backblazeb2.com.example.com"} {
		t.Run(host, func(t *testing.T) {
			f := &copyProtocol{t: t, size: 10, destinationSize: 10, destinationType: "video/mp4", destinationETag: "copied-etag", sourceETag: "source-etag", sourceVersion: "source-version",
				backblaze: host == "s3.us-west-004.backblazeb2.com"}
			client := func(accessKey string) *minio.Client {
				c, err := minio.New(host, &minio.Options{Secure: true, Region: "us-east-1", BucketLookup: minio.BucketLookupPath,
					Creds: credentials.NewStaticV4(accessKey, "fixture-secret", ""),
					Transport: copyTestTransport(func(request *http.Request) (*http.Response, error) {
						response := httptest.NewRecorder()
						f.serve(response, request)
						return response.Result(), nil
					})})
				if err != nil {
					t.Fatal(err)
				}
				return c
			}
			f.source = &S3{client: client("source-bucket"), bucket: "source-bucket", region: "us-east-1"}
			f.destination = &S3{client: client("destination-bucket"), bucket: "destination-bucket", region: "us-east-1"}
			copier := &s3Copier{destination: f.destination, client: client("copy-access")}
			if n, err := copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30); err != nil || n != 10 {
				t.Fatalf("Copy = %d, %v", n, err)
			}
		})
	}
}

func TestServerCopyUsesConditionalCopyWithoutReadingPayload(t *testing.T) {
	for _, size := range []int64{0, 4096, 4 << 30, (5 << 30) + 17, 16 << 30} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			f := newCopyProtocol(t, size)
			n, err := f.copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30)
			if err != nil || n != size {
				t.Fatalf("Copy = %d, %v; want %d", n, err, size)
			}
			if f.headRequests != 2 || f.aborts != 0 {
				t.Fatalf("heads=%d aborts=%d", f.headRequests, f.aborts)
			}
			if size <= 4<<30 {
				if f.copies != 1 || f.starts != 0 {
					t.Fatal("small copy did not use CopyObject")
				}
			} else if f.copies != 0 || f.starts != 1 || f.completes != 1 || f.parts != int((size+(128<<20)-1)/(128<<20)) {
				t.Fatalf("multipart counts: starts=%d parts=%d completes=%d", f.starts, f.parts, f.completes)
			}
		})
	}
}

func TestServerCopyPinsOpaqueSourceVersion(t *testing.T) {
	for _, size := range []int64{10, 5 << 30} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			f := newCopyProtocol(t, size)
			f.sourceVersion = "source-version+/="
			if n, err := f.copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30); err != nil || n != size {
				t.Fatalf("Copy = %d, %v", n, err)
			}
		})
	}
}

func TestServerCopyChecksLimitsBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		name string
		size int64
		etag string
	}{{"oversize", (16 << 30) + 1, "source-etag"}, {"missing etag", 10, ""}} {
		t.Run(test.name, func(t *testing.T) {
			f := newCopyProtocol(t, test.size)
			f.sourceETag = test.etag
			if _, err := f.copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30); err == nil || errors.Is(err, ErrCopyUnavailable) {
				t.Fatalf("invalid source error = %v", err)
			}
			if f.copies+f.starts != 0 {
				t.Fatal("invalid source wrote destination")
			}
		})
	}
}

func TestServerCopySourceAbsenceAndCanceledContext(t *testing.T) {
	f := newCopyProtocol(t, 10)
	f.headCode = "NoSuchKey"
	if _, err := f.copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.copier.Copy(ctx, f.source, "media.mp4", "media.mp4", 16<<30); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HEAD error = %v", err)
	}
	if f.copies+f.starts != 0 {
		t.Fatal("missing source or canceled context wrote destination")
	}
}

func TestServerCopyUnavailableDoesNotHideHardFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		size        int64
		configure   func(*copyProtocol)
		unavailable bool
		abort       int
	}{
		{"copy denied", 10, func(f *copyProtocol) { f.copyCode = "AccessDenied" }, true, 0},
		{"copy unsupported", 10, func(f *copyProtocol) { f.copyCode = "NotImplemented" }, true, 0},
		{"init denied", 5 << 30, func(f *copyProtocol) { f.initCode = "AccessDenied" }, true, 0},
		{"source denied", 10, func(f *copyProtocol) { f.headCode = "AccessDenied" }, false, 0},
		{"source changed", 10, func(f *copyProtocol) { f.copyCode = "PreconditionFailed" }, false, 0},
		{"quota", 10, func(f *copyProtocol) { f.copyCode = "QuotaExceeded" }, false, 0},
		{"part denied", 5 << 30, func(f *copyProtocol) { f.partCode = "AccessDenied" }, false, 1},
		{"part changed", 5 << 30, func(f *copyProtocol) { f.partCode = "PreconditionFailed" }, false, 1},
		{"completion failed", 5 << 30, func(f *copyProtocol) { f.completeCode = "InvalidPart" }, false, 1},
		{"wrong destination size", 10, func(f *copyProtocol) { f.destinationSize++ }, false, 0},
		{"wrong destination type", 10, func(f *copyProtocol) { f.destinationType = "text/html" }, false, 0},
		{"wrong destination etag", 10, func(f *copyProtocol) { f.destinationETag = "changed" }, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCopyProtocol(t, test.size)
			test.configure(f)
			_, err := f.copier.Copy(context.Background(), f.source, "media.mp4", "media.mp4", 16<<30)
			if err == nil || errors.Is(err, ErrCopyUnavailable) != test.unavailable || f.aborts != test.abort {
				t.Fatalf("error=%v unavailable=%v aborts=%d", err, errors.Is(err, ErrCopyUnavailable), f.aborts)
			}
		})
	}
}

func TestServerCopyCancellationAbortsAndSurfacesCleanupFailure(t *testing.T) {
	for _, abortCode := range []string{"", "AccessDenied"} {
		t.Run("abort_"+abortCode, func(t *testing.T) {
			f := newCopyProtocol(t, 5<<30)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.cancel, f.abortCode = cancel, abortCode
			_, err := f.copier.Copy(ctx, f.source, "media.mp4", "media.mp4", 16<<30)
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCopyUnavailable) || f.aborts != 1 || f.completes != 0 {
				t.Fatalf("cancel error=%v aborts=%d completes=%d", err, f.aborts, f.completes)
			}
			if abortCode != "" && !strings.Contains(err.Error(), "abort multipart copy") {
				t.Fatalf("cleanup failure lost: %v", err)
			}
		})
	}
}

func TestServerCopyEligibilityAndConfiguration(t *testing.T) {
	f := newCopyProtocol(t, 10)
	local, _ := NewLocal(t.TempDir())
	if c, err := NewS3Copier(local, "", ""); err != nil || c != nil {
		t.Fatalf("disabled copier = %v, %v", c, err)
	}
	for _, keys := range [][2]string{{"access", ""}, {"", "secret"}, {"access", "secret"}} {
		if _, err := NewS3Copier(local, keys[0], keys[1]); err == nil {
			t.Fatal("invalid copy configuration accepted")
		}
	}
	for _, source := range []Backend{local, &S3{client: f.source.client, bucket: f.source.bucket, region: "other"}} {
		if _, err := f.copier.Copy(context.Background(), source, "media.mp4", "media.mp4", 16<<30); !errors.Is(err, ErrCopyUnavailable) {
			t.Fatalf("ineligible copy error = %v", err)
		}
	}
	for _, secure := range []bool{false, true} {
		endpoint := f.source.client.EndpointURL().Host
		if !secure {
			endpoint = "unreachable.invalid"
		}
		source, err := NewS3(S3Config{Endpoint: endpoint, UseSSL: secure, Bucket: "source-bucket", Region: "us-east-1", AccessKey: "source", SecretKey: "secret"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.copier.Copy(context.Background(), source, "media.mp4", "media.mp4", 16<<30); !errors.Is(err, ErrCopyUnavailable) {
			t.Fatalf("endpoint/scheme mismatch error = %v", err)
		}
	}
	for _, key := range []string{"../media.mp4", "/media.mp4", ""} {
		if _, err := f.copier.Copy(context.Background(), f.source, key, "media.mp4", 16<<30); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("invalid source key error = %v", err)
		}
		if _, err := f.copier.Copy(context.Background(), f.source, "media.mp4", key, 16<<30); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("invalid destination key error = %v", err)
		}
	}
	if _, err := f.copier.Copy(context.Background(), f.destination, "media.mp4", "media.mp4", 16<<30); err == nil || errors.Is(err, ErrCopyUnavailable) {
		t.Fatalf("self-copy error = %v", err)
	}
	if f.headRequests+f.copies+f.starts != 0 {
		t.Fatal("ineligible copy performed network I/O")
	}
}
