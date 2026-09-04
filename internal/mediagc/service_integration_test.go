//go:build integration

// Media GC integration test against a live S3-compatible store (MinIO via the
// compose "storage" profile). Self-skips when S3_TEST_ENDPOINT is unset. Run:
//
//	docker compose --profile storage up -d minio
//	S3_TEST_ENDPOINT=localhost:9000 go test -tags=integration ./internal/mediagc/...
//
// It runs in a bucket of its OWN (see newS3) because it is the one integration
// test that deletes across a whole bucket.
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

	"github.com/vidra/vidra-core/internal/media"
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
		Endpoint: endpoint,
		// A bucket of this package's own, NOT the shared vidra-test the other S3
		// integration tests use. The sweep below deletes every attributable
		// orphan in the WHOLE bucket, and `go test ./...` runs packages in
		// parallel against one MinIO — so on the shared bucket this test would
		// race to delete the objects internal/httpapi's direct-delivery test had
		// just written (they are the same minted shapes: web-videos/<id>.mp4,
		// thumbnails/<id>.jpg), failing that test intermittently and only in a
		// full-suite run. verify_blobs and bucket_ownership already take a bucket
		// each for the same reason.
		Bucket:         envOr("S3_TEST_BUCKET", "vidra-test-mediagc"),
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

// TestSweepAgainstMinIO proves the sweep lists and deletes orphans on a real
// S3-compatible backend, keeping referenced objects. It scopes its keys under a
// unique root and the swept prefixes so it cannot disturb other data — though it
// still exercises the exact same prefix logic as production.
func TestSweepAgainstMinIO(t *testing.T) {
	blobs := newS3(t)
	ctx := context.Background()

	// Referenced original + an orphan, both under the swept web-videos/ prefix
	// and both in the shape this install mints (media.OriginalVideoKey). A fresh
	// video id per object is what keeps them unique in a bucket shared with the
	// rest of the integration suite — and the shape is load-bearing, not
	// cosmetic: a key whose id position is not one of our ids is kept as
	// unattributable and would never be reported as an orphan at all.
	liveVid := uuid.New()
	refKey := media.OriginalVideoKey(liveVid, 0, ".mp4")
	orphanKey := media.OriginalVideoKey(uuid.New(), 0, ".mp4")
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
	// The bucket is shared with whatever else the integration suite left behind,
	// so this test asserts the sweep and not the ownership resolution: it states
	// the ownership the api would have resolved after adopting it, and gives the
	// ratio breaker no reason to fire (a handful of orphans is under the floor).
	svc := NewService(repo, blobs, WithBucketOwnership(OwnershipOwned), WithMaxOrphanPercent(100))

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

// TestBreakerAndOwnershipAgainstMinIO proves the two rails hold against a real
// S3-compatible store rather than only against the local filesystem — the
// deletes they gate are the ones that are irreversible in production, and the
// unit tests exercise a backend where a mistake is a `rm` on a temp dir.
//
// It also proves the ownership marker survives a round trip through the object
// store, which the local backend cannot establish: a key with a leading dot is
// exactly the shape an S3-compatible could decide to treat as special.
func TestBreakerAndOwnershipAgainstMinIO(t *testing.T) {
	blobs := newS3(t)
	ctx := context.Background()

	// More orphans than the absolute floor, under a swept prefix, all this
	// test's own and all in the shape this install mints
	// (media.VideoThumbnailKey) so the sweep counts them as orphans at all.
	// Nothing here is referenced, so the orphan share is 100%.
	orphans := make([]string, 0, breakerFloor+5)
	for i := 0; i <= breakerFloor+4; i++ {
		key := media.VideoThumbnailKey(uuid.New())
		if _, err := blobs.Put(ctx, key, strings.NewReader("x")); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
		t.Cleanup(func() { _ = blobs.Delete(context.Background(), key) })
		orphans = append(orphans, key)
	}
	repo := &fakeRepo{}

	t.Run("the breaker refuses the delete and leaves every object in place", func(t *testing.T) {
		svc := NewService(repo, blobs, WithBucketOwnership(OwnershipOwned), WithMaxOrphanPercent(25))
		res, err := svc.Sweep(ctx, false)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if !res.BreakerTripped || res.Deleted != 0 {
			t.Fatalf("sweep = %+v, want a tripped breaker that deleted nothing", res)
		}
		for _, k := range orphans {
			if ok, _ := blobs.Exists(ctx, k); !ok {
				t.Fatalf("a tripped breaker still deleted %q", k)
			}
		}
	})

	t.Run("an unowned bucket refuses the delete", func(t *testing.T) {
		svc := NewService(repo, blobs, WithBucketOwnership(OwnershipUnowned), WithMaxOrphanPercent(100))
		res, err := svc.Sweep(ctx, false)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if !res.ForcedDryRun || res.Deleted != 0 {
			t.Fatalf("sweep = %+v, want a forced dry run that deleted nothing", res)
		}
		for _, k := range orphans {
			if ok, _ := blobs.Exists(ctx, k); !ok {
				t.Fatalf("a sweep of an unowned bucket deleted %q", k)
			}
		}
	})

	t.Run("the ownership marker round-trips through the object store", func(t *testing.T) {
		// The marker is a single well-known key, so this test writes the real one
		// and restores whatever was there — the bucket is shared with the rest of
		// the integration suite.
		before, hadBefore, err := storage.ReadOwnerMarker(ctx, blobs)
		if err != nil {
			t.Fatalf("ReadOwnerMarker: %v", err)
		}
		t.Cleanup(func() {
			restoreCtx := context.Background()
			if hadBefore {
				_ = storage.WriteOwnerMarker(restoreCtx, blobs, before)
				return
			}
			_ = blobs.Delete(restoreCtx, storage.OwnerMarkerKey)
		})

		identity := "66666666-6666-4666-8666-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1_000_000_000_000)
		svc := NewService(repo, blobs, WithBucketOwnership(OwnershipUnowned), WithInstanceIdentity(identity))
		if err := svc.AdoptBucket(ctx, false); err != nil {
			t.Fatalf("AdoptBucket: %v", err)
		}
		got, found, err := storage.ReadOwnerMarker(ctx, blobs)
		if err != nil || !found || got != identity {
			t.Fatalf("marker after adoption = (%q, %v, %v), want %q", got, found, err, identity)
		}
		if svc.Ownership() != OwnershipOwned {
			t.Errorf("ownership after adoption = %q, want %q", svc.Ownership(), OwnershipOwned)
		}
		// And the marker is not something the sweep can collect.
		res, err := svc.Sweep(ctx, true)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		for _, o := range res.Orphans {
			if o == storage.OwnerMarkerKey {
				t.Fatal("the sweep enumerated the ownership marker it depends on")
			}
		}
	})
}
