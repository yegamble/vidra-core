//go:build integration

// End-to-end direct-delivery test against a REAL S3-compatible store (MinIO via
// the compose "storage" profile), excluded from `make ci`. It proves the whole
// chain the unit tests can only approximate: a public media GET answers 307,
// the Location is a genuinely signed URL, and fetching that URL from the object
// store returns the EXACT object bytes with the response headers the API byte
// path would have sent.
//
// Run with:
//
//	docker compose --profile storage up -d minio
//	S3_TEST_ENDPOINT=localhost:9000 go test -count=1 -race -tags=integration \
//	  -run TestDirectDelivery ./internal/httpapi/
package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/storage"
)

// newDeliveryS3Backend builds an S3 backend against the MinIO named by
// S3_TEST_ENDPOINT, skipping when it is unset.
func newDeliveryS3Backend(t *testing.T) *storage.S3 {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping direct-delivery integration test")
	}
	useSSL := false
	if v := os.Getenv("S3_TEST_USE_SSL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			t.Fatalf("parse S3_TEST_USE_SSL: %v", err)
		}
		useSSL = b
	}
	envOr := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	b, err := storage.NewS3(storage.S3Config{
		Endpoint:       endpoint,
		Bucket:         envOr("S3_TEST_BUCKET", "vidra-test"),
		AccessKey:      envOr("S3_TEST_ACCESS_KEY", "vidra"),
		SecretKey:      envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"),
		UseSSL:         useSSL,
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := b.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return b
}

// TestDirectDeliveryRedirectsToRealSignedURL is the wave's end-to-end proof.
func TestDirectDeliveryRedirectsToRealSignedURL(t *testing.T) {
	s3 := newDeliveryS3Backend(t)
	ctx := context.Background()

	// The HTTP layer serves from S3 and signs from the same (raw) backend —
	// exactly cmd/api's wiring when no migration target is configured. The video
	// service keeps the harness's local backend, so uploads land locally and the
	// test mirrors the objects it means to deliver into the bucket by hand.
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{
		WithMediaStorage(s3),
		WithDeliveryPresigner(s3),
	})
	setPresignedDelivery(t, srv, true)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	originalKey := "web-videos/" + id + ".mp4"
	segmentKey := "streaming-playlists/" + id + "/240p/seg_00000.ts"
	for _, key := range []string{originalKey, "thumbnails/" + id + ".jpg", segmentKey} {
		mirrorIntoS3(t, ctx, blobs, s3, key)
	}

	tests := []struct {
		name            string
		path            string
		wantBody        string
		wantContentType string
		wantDisposition string
	}{
		{
			name: "original", path: "/api/v1/videos/" + id + "/original",
			wantBody: "video-bytes", wantContentType: "video/mp4",
		},
		{
			name: "thumbnail", path: "/api/v1/videos/" + id + "/thumbnail",
			wantBody: "poster-bytes", wantContentType: "image/png",
		},
		{
			name: "hls segment", path: "/api/v1/videos/" + id + "/hls/240p/seg_00000.ts",
			wantBody: "fake-ts-bytes", wantContentType: "video/mp2t",
		},
		{
			name:     "download keeps the creator's filename",
			path:     "/api/v1/videos/" + id + "/download/original",
			wantBody: "video-bytes", wantContentType: "video/mp4",
			wantDisposition: "attachment; filename=clip.mp4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := getWith(srv, tt.path, "", "")
			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("status = %d, want 307; body=%s", rec.Code, rec.Body.String())
			}
			if cc := rec.Header().Get("Cache-Control"); cc != delivery.CachePresignedRedirect {
				t.Errorf("redirect Cache-Control = %q, want %q", cc, delivery.CachePresignedRedirect)
			}
			signed := rec.Header().Get("Location")
			if !strings.Contains(signed, "X-Amz-Signature=") {
				t.Fatalf("Location is not a signed URL: %q", signed)
			}

			resp, err := http.Get(signed) //nolint:gosec // the URL under test
			if err != nil {
				t.Fatalf("GET signed URL: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read signed body: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("signed GET = %d; body=%s", resp.StatusCode, body)
			}
			if string(body) != tt.wantBody {
				t.Fatalf("signed body = %q, want the exact object bytes %q", body, tt.wantBody)
			}
			// Header equivalence with the byte path is the whole reason the
			// resolver requires storage.ResponsePresigner.
			if got := resp.Header.Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
			if got := resp.Header.Get("Content-Disposition"); got != tt.wantDisposition {
				t.Errorf("Content-Disposition = %q, want %q", got, tt.wantDisposition)
			}
			if got := resp.Header.Get("Cache-Control"); got != delivery.CachePresignedRedirect {
				t.Errorf("object Cache-Control = %q, want %q", got, delivery.CachePresignedRedirect)
			}
		})
	}

	// Range requests survive the redirect: the browser re-sends Range to the
	// object store, which is what keeps seeking working for a presigned
	// original.
	t.Run("range survives the redirect", func(t *testing.T) {
		rec := getWith(srv, "/api/v1/videos/"+id+"/original", "", "")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rec.Header().Get("Location"), nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Range", "bytes=0-4")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("ranged GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusPartialContent || string(body) != "video" {
			t.Fatalf("ranged signed GET = %d body=%q, want 206/\"video\"", resp.StatusCode, body)
		}
	})

	// With the toggle off, the same routes serve bytes straight out of S3 — the
	// authoritative path is not a fallback that only works in theory.
	t.Run("toggle off serves the same bytes from the API", func(t *testing.T) {
		setPresignedDelivery(t, srv, false)
		t.Cleanup(func() { setPresignedDelivery(t, srv, true) })
		assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
		assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""), "fake-ts-bytes")
	})

	// A private video is served by the API even with the toggle on, and its key
	// never reaches a signed URL.
	t.Run("private media is never redirected", func(t *testing.T) {
		privID := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
		if rec := uploadVideoFile(srv, privID, "clip.mp4", "video/mp4", "private-bytes", tok); rec.Code != http.StatusCreated {
			t.Fatalf("private upload = %d; body=%s", rec.Code, rec.Body.String())
		}
		mirrorIntoS3(t, ctx, blobs, s3, "web-videos/"+privID+".mp4")
		assertBytes(t, getWith(srv, "/api/v1/videos/"+privID+"/original", tok, ""), "private-bytes")
	})
}

// mirrorIntoS3 copies one object from the harness's local backend into the
// bucket, and registers its removal.
func mirrorIntoS3(t *testing.T, ctx context.Context, from storage.Backend, to *storage.S3, key string) {
	t.Helper()
	rc, err := from.Open(ctx, key)
	if err != nil {
		t.Fatalf("open %q locally: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	if _, err := to.Put(ctx, key, bytes.NewReader(data)); err != nil {
		t.Fatalf("put %q into the bucket: %v", key, err)
	}
	t.Cleanup(func() { _ = to.Delete(context.Background(), key) })
}
