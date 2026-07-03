//go:build integration

// Integration coverage for the profile-image sqlc queries against a live
// PostgreSQL (real user_images/channel_images tables, migration 0040 applied).
// Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/profileimage/
package profileimage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
)

// TestProfileImagePersistence proves the full lifecycle over the real
// database: upsert (insert + conflict-update on re-upload), get, and delete
// for both user and channel images, with the FK cascade owning cleanup.
func TestProfileImagePersistence(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)

	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	svc := NewService(st.Queries(), blobs)

	// Seed a user + channel; deleting the user cascades to the channel and, via
	// the 0040 FKs, to both image tables.
	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"img-"+suffix, "img-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Img') RETURNING id`,
		userID, "img_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// User avatar: insert, then conflict-update on re-upload with a new
	// extension (one row per (user, kind); updated_at advances; old blob gone).
	first, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "a.png", Reader: strings.NewReader("v1")})
	if err != nil {
		t.Fatalf("SetUserImage: %v", err)
	}
	second, err := svc.SetUserImage(ctx, userID, KindAvatar, UploadInput{Filename: "b.jpg", Reader: strings.NewReader("v2-bytes")})
	if err != nil {
		t.Fatalf("SetUserImage replace: %v", err)
	}
	if second.ContentType != "image/jpeg" || second.SizeBytes != 8 {
		t.Fatalf("replaced image = %+v, want image/jpeg size=8", second)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("updated_at %v should advance past %v on re-upload", second.UpdatedAt, first.UpdatedAt)
	}
	var count int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM user_images WHERE user_id = $1 AND kind = 'avatar'`, userID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("user avatar rows = (%d, %v), want exactly 1 (upsert)", count, err)
	}
	if ok, _ := blobs.Exists(ctx, first.StorageKey); ok {
		t.Errorf("old blob %s should be evicted on extension change", first.StorageKey)
	}

	got, err := svc.UserImage(ctx, userID, KindAvatar)
	if err != nil || got.StorageKey != second.StorageKey || got.ContentType != "image/jpeg" {
		t.Fatalf("UserImage = (%+v, %v), want the jpg row", got, err)
	}
	if err := svc.DeleteUserImage(ctx, userID, KindAvatar); err != nil {
		t.Fatalf("DeleteUserImage: %v", err)
	}
	if _, err := svc.UserImage(ctx, userID, KindAvatar); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UserImage after delete = %v, want ErrNotFound", err)
	}
	if ok, _ := blobs.Exists(ctx, second.StorageKey); ok {
		t.Errorf("blob %s should be removed on delete", second.StorageKey)
	}

	// Channel banner round-trip on the channel_images table.
	img, err := svc.SetChannelImage(ctx, channelID, KindBanner, UploadInput{Filename: "wide.webp", Reader: strings.NewReader("banner")})
	if err != nil {
		t.Fatalf("SetChannelImage: %v", err)
	}
	if want := "banners/channels/" + channelID.String() + ".webp"; img.StorageKey != want {
		t.Fatalf("channel banner key = %q, want %q", img.StorageKey, want)
	}
	if !svc.HasChannelImage(ctx, channelID, KindBanner) || svc.HasChannelImage(ctx, channelID, KindAvatar) {
		t.Error("HasChannelImage: want banner=true avatar=false")
	}
	if err := svc.DeleteChannelImage(ctx, channelID, KindBanner); err != nil {
		t.Fatalf("DeleteChannelImage: %v", err)
	}
	if err := svc.DeleteChannelImage(ctx, channelID, KindBanner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second channel delete = %v, want ErrNotFound", err)
	}
}
