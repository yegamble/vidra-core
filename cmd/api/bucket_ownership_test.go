package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/storage"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The two branches of resolveBucketOwnership that need no object store. Both
// matter more than they look: the first is every local-disk deployment there is,
// and the second is the state a fresh install sits in between `docker compose
// up` and the migration one-shot finishing.
func TestResolveBucketOwnershipWithoutAnObjectStore(t *testing.T) {
	ctx := context.Background()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	got := resolveBucketOwnership(ctx, quietLogger(), local, false, "77777777-7777-4777-8777-777777777777")
	if got != mediagc.OwnershipNotApplicable {
		t.Errorf("local backend = %q, want %q — a storage root has no ownership question to answer", got, mediagc.OwnershipNotApplicable)
	}
	// And no marker was written into somebody's media directory.
	if _, found, _ := storage.ReadOwnerMarker(ctx, local); found {
		t.Error("a marker was written into the local storage root; local is exempt by design, not by accident")
	}

	s3, err := storage.NewS3(storage.S3Config{
		Endpoint: "s3.example.invalid", Bucket: "b", AccessKey: "k", SecretKey: "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	// No identity: the question cannot be asked, so the answer has to be the one
	// that forbids deleting. Note this returns WITHOUT dialling the (invalid)
	// endpoint — an unmigrated database must not make boot wait on a network
	// round trip whose answer it could not use.
	if got := resolveBucketOwnership(ctx, quietLogger(), s3, false, ""); got != mediagc.OwnershipUnowned {
		t.Errorf("object store with no instance identity = %q, want %q", got, mediagc.OwnershipUnowned)
	}
}
