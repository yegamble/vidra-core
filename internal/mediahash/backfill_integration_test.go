//go:build integration

// Integration tests require a live PostgreSQL reachable via DATABASE_URL with
// migrations already applied. Run with:
//
//	docker compose --profile core up -d postgres migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -count=1 -tags=integration -race ./internal/mediahash/...
package mediahash

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestBackfillPassAgainstPostgres runs one real pass over real rows: a file
// whose object is present gets the object's digest, a file whose object is gone
// gets the missing sentinel, and a file that already carries a digest is left
// exactly as it was. The last one is the guard that matters operationally — the
// pass must never be able to overwrite a hash the write path produced.
func TestBackfillPassAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	suffix := time.Now().UnixNano()
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     fmt.Sprintf("hashy-%d", suffix),
		Email:        fmt.Sprintf("hashy-%d@example.test", suffix),
		PasswordHash: "$2a$12$fakefakefakefakefakefakefakefakefakefakefakefakefakefe",
		Role:         "user",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: u.ID, Handle: fmt.Sprintf("hashy-ch-%d", suffix), DisplayName: "Hashy"})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	v, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "hashme", Privacy: "private", CommentsPolicy: "enabled"})
	if err != nil {
		t.Fatalf("create video: %v", err)
	}

	presentKey := "web-videos/" + v.ID.String() + ".mp4"
	if _, err := blobs.Put(ctx, presentKey, strings.NewReader(helloWorld)); err != nil {
		t.Fatal(err)
	}
	present, err := q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID: v.ID, Kind: "original", StorageKey: presentKey, SizeBytes: int64(len(helloWorld)),
	})
	if err != nil {
		t.Fatalf("create present file: %v", err)
	}
	gone, err := q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID: v.ID, Kind: "thumbnail", StorageKey: "thumbnails/" + v.ID.String() + ".jpg", SizeBytes: 4,
	})
	if err != nil {
		t.Fatalf("create missing-object file: %v", err)
	}
	// Already hashed by its write path: the pass must not see it at all.
	alreadyKey := "web-videos/" + v.ID.String() + "-v2.mp4"
	if _, err := blobs.Put(ctx, alreadyKey, strings.NewReader("different bytes entirely")); err != nil {
		t.Fatal(err)
	}
	const preExisting = "0000000000000000000000000000000000000000000000000000000000000000"
	already, err := q.CreateVideoFile(ctx, sqlcgen.CreateVideoFileParams{
		VideoID: v.ID, Kind: "rendition", StorageKey: alreadyKey, SizeBytes: 24, Sha256: preExisting,
	})
	if err != nil {
		t.Fatalf("create pre-hashed file: %v", err)
	}

	// One pass. The batch is large enough to drain this video's rows but the
	// scan is instance-wide, so assertions are per-row, never on the counts.
	if _, err := NewService(q, blobs, nil).BackfillOnce(ctx, 200); err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}

	read := func(id interface{ String() string }) string {
		t.Helper()
		var got string
		if err := st.Pool.QueryRow(ctx, `SELECT sha256 FROM video_files WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("read sha256: %v", err)
		}
		return got
	}
	if got := read(present.ID); got != helloWorldSHA256 {
		t.Errorf("present object sha256 = %q, want %q", got, helloWorldSHA256)
	}
	if got := read(gone.ID); got != SentinelMissing {
		t.Errorf("absent object sha256 = %q, want the %q sentinel", got, SentinelMissing)
	}
	if got := read(already.ID); got != preExisting {
		t.Errorf("already-hashed row was rewritten: sha256 = %q, want %q", got, preExisting)
	}

	// And the pass is idempotent for these rows: a second one finds nothing
	// left to do for them.
	if _, err := NewService(q, blobs, nil).BackfillOnce(ctx, 200); err != nil {
		t.Fatalf("second BackfillOnce: %v", err)
	}
	if got := read(present.ID); got != helloWorldSHA256 {
		t.Errorf("second pass changed a hashed row: %q", got)
	}
}
