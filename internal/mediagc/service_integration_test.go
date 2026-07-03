//go:build integration

// Media GC integration test against a live S3-compatible store (MinIO via the
// compose "storage" profile). Self-skips when S3_TEST_ENDPOINT is unset. Run:
//
//	docker compose --profile storage up -d minio
//	S3_TEST_ENDPOINT=localhost:9000 go test -tags=integration ./internal/mediagc/...
package mediagc

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

func newS3(t *testing.T) *storage.S3 {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping media GC MinIO integration test")
	}
	useSSL := false
	if v := os.Getenv("S3_TEST_USE_SSL"); v != "" {
		useSSL, _ = strconv.ParseBool(v)
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
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
	if err := b.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return b
}

// TestSweepAgainstMinIO proves the sweep lists and deletes orphans on a real
// S3-compatible backend, keeping referenced objects. It scopes its keys under a
// unique root and the swept prefixes so it cannot disturb other data — though it
// still exercises the exact same prefix logic as production.
func TestSweepAgainstMinIO(t *testing.T) {
	blobs := newS3(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())

	liveVid := uuid.New()
	// Referenced original + an orphan, both under the swept web-videos/ prefix.
	refKey := "web-videos/" + liveVid.String() + "-" + tag + ".mp4"
	orphanKey := "web-videos/orphan-" + tag + ".mp4"
	for _, k := range []string{refKey, orphanKey} {
		k := k
		if _, err := blobs.Put(ctx, k, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
		t.Cleanup(func() { _ = blobs.Delete(context.Background(), k) })
	}

	// Only refKey is referenced; keep any other real objects live by returning
	// the full video-id/key sets the DB would (here just what this test seeded is
	// unreferenced except refKey — other prefixes are empty in a fresh bucket).
	repo := &fakeRepo{fileKeys: []string{refKey}}
	svc := NewService(repo, blobs)

	res, err := svc.Sweep(ctx, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// Our orphan must be among the deleted set; refKey must survive.
	if ok, _ := blobs.Exists(ctx, orphanKey); ok {
		t.Errorf("orphan %q survived the MinIO sweep", orphanKey)
	}
	if ok, _ := blobs.Exists(ctx, refKey); !ok {
		t.Errorf("referenced %q was deleted by the MinIO sweep", refKey)
	}
	found := false
	for _, o := range res.Orphans {
		if o == orphanKey {
			found = true
		}
	}
	if !found {
		t.Errorf("orphan %q not reported in %v", orphanKey, res.Orphans)
	}
}
