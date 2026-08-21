//go:build integration

// Bucket-ownership resolution against a live S3-compatible store (MinIO via the
// compose "storage" profile). Self-skips when S3_TEST_ENDPOINT is unset. Run:
//
//	docker compose --profile storage up -d minio
//	S3_TEST_ENDPOINT=localhost:9000 go test -tags=integration ./cmd/api/...
//
// This is the decision that stands between a shared or pre-populated bucket and
// a daily sweep that empties it, and every one of its four outcomes is decided
// by what a real object store answers — a fake would be asserting the fake.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/storage"
)

// freshBucket builds a backend against a bucket name no other test uses, and
// removes it afterwards, so "is this bucket empty" is a question with a real
// answer rather than one the rest of the suite has already spoiled.
func freshBucket(t *testing.T) (*storage.S3, string) {
	t.Helper()
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_TEST_ENDPOINT not set; skipping bucket-ownership integration test")
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
	name := fmt.Sprintf("vidra-owner-%d", time.Now().UnixNano())
	b, err := storage.NewS3(storage.S3Config{
		Endpoint:       endpoint,
		Bucket:         name,
		AccessKey:      envOr("S3_TEST_ACCESS_KEY", "vidra"),
		SecretKey:      envOr("S3_TEST_SECRET_KEY", "vidra-dev-secret"),
		UseSSL:         useSSL,
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return b, name
}

func TestResolveBucketOwnershipAgainstMinIO(t *testing.T) {
	const ours = "88888888-8888-4888-8888-888888888888"
	const theirs = "99999999-9999-4999-8999-999999999999"

	t.Run("a bucket this boot created is claimed automatically", func(t *testing.T) {
		ctx := context.Background()
		b, _ := freshBucket(t)
		created, err := b.EnsureBucket(ctx)
		if err != nil {
			t.Fatalf("EnsureBucket: %v", err)
		}
		if !created {
			t.Fatal("the test bucket already existed")
		}
		t.Cleanup(func() { cleanBucket(t, b) })

		if got := resolveBucketOwnership(ctx, quietLogger(), b, created, ours); got != mediagc.OwnershipOwned {
			t.Fatalf("ownership of a just-created bucket = %q, want %q", got, mediagc.OwnershipOwned)
		}
		marker, found, err := storage.ReadOwnerMarker(ctx, b)
		if err != nil || !found || marker != ours {
			t.Fatalf("marker = (%q, %v, %v), want %q", marker, found, err, ours)
		}
	})

	t.Run("an existing but empty bucket is claimed too", func(t *testing.T) {
		ctx := context.Background()
		b, _ := freshBucket(t)
		if _, err := b.EnsureBucket(ctx); err != nil {
			t.Fatalf("EnsureBucket: %v", err)
		}
		t.Cleanup(func() { cleanBucket(t, b) })

		// createdBucket=false: this is the SECOND boot against a store nobody has
		// written to, which must still be claimable — otherwise a restart between
		// provisioning and the first upload would strand the install forever.
		if got := resolveBucketOwnership(ctx, quietLogger(), b, false, ours); got != mediagc.OwnershipOwned {
			t.Fatalf("ownership of an empty bucket = %q, want %q", got, mediagc.OwnershipOwned)
		}
	})

	t.Run("a bucket with objects and no marker is NOT claimed", func(t *testing.T) {
		ctx := context.Background()
		b, _ := freshBucket(t)
		if _, err := b.EnsureBucket(ctx); err != nil {
			t.Fatalf("EnsureBucket: %v", err)
		}
		t.Cleanup(func() { cleanBucket(t, b) })
		// Somebody else's media, which is exactly what the sweep would call an
		// orphan set.
		if _, err := b.Put(ctx, "web-videos/theirs.mp4", strings.NewReader("not ours")); err != nil {
			t.Fatalf("Put: %v", err)
		}

		if got := resolveBucketOwnership(ctx, quietLogger(), b, false, ours); got != mediagc.OwnershipUnowned {
			t.Fatalf("ownership of a populated unmarked bucket = %q, want %q", got, mediagc.OwnershipUnowned)
		}
		if _, found, _ := storage.ReadOwnerMarker(ctx, b); found {
			t.Error("a marker was written into a bucket holding somebody else's objects")
		}
	})

	t.Run("another install's marker is a conflict", func(t *testing.T) {
		ctx := context.Background()
		b, _ := freshBucket(t)
		if _, err := b.EnsureBucket(ctx); err != nil {
			t.Fatalf("EnsureBucket: %v", err)
		}
		t.Cleanup(func() { cleanBucket(t, b) })
		if err := storage.WriteOwnerMarker(ctx, b, theirs); err != nil {
			t.Fatalf("WriteOwnerMarker: %v", err)
		}

		// createdBucket is deliberately true here: even "I just made this" must
		// not overwrite a marker that is already in it.
		if got := resolveBucketOwnership(ctx, quietLogger(), b, true, ours); got != mediagc.OwnershipConflict {
			t.Fatalf("ownership of a foreign-marked bucket = %q, want %q", got, mediagc.OwnershipConflict)
		}
		marker, _, _ := storage.ReadOwnerMarker(ctx, b)
		if marker != theirs {
			t.Errorf("the other install's marker was overwritten: %q", marker)
		}
	})

	t.Run("our own marker is recognised on the next boot", func(t *testing.T) {
		ctx := context.Background()
		b, _ := freshBucket(t)
		if _, err := b.EnsureBucket(ctx); err != nil {
			t.Fatalf("EnsureBucket: %v", err)
		}
		t.Cleanup(func() { cleanBucket(t, b) })
		if err := storage.WriteOwnerMarker(ctx, b, ours); err != nil {
			t.Fatalf("WriteOwnerMarker: %v", err)
		}
		if _, err := b.Put(ctx, "web-videos/ours.mp4", strings.NewReader("ours")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if got := resolveBucketOwnership(ctx, quietLogger(), b, false, ours); got != mediagc.OwnershipOwned {
			t.Fatalf("ownership of our own populated bucket = %q, want %q", got, mediagc.OwnershipOwned)
		}
	})
}

// cleanBucket empties a per-test bucket. The empty bucket itself is left behind:
// storage.Backend deliberately exposes no bucket-removal primitive (nothing in
// production should be able to delete a bucket), and an empty bucket in a
// disposable test MinIO costs nothing.
func cleanBucket(t *testing.T, b *storage.S3) {
	t.Helper()
	ctx := context.Background()
	for _, prefix := range []string{"web-videos", ".vidra"} {
		keys, err := b.ListKeys(ctx, prefix)
		if err != nil {
			continue
		}
		for _, k := range keys {
			_ = b.Delete(ctx, k)
		}
	}
}
