//go:build integration

package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestAccountHardDeletePersists runs the full §1 anonymisation checklist
// against real PostgreSQL: a user with the complete data graph (channel,
// video + files + caption + tags, comment on a surviving video with a reply
// under it, rating, save, history, playlist, follow, mutes, block, oauth
// identity, MFA + recovery codes, notifications + prefs, reset/verify tokens,
// actor key, avatar, sessions, a finished export) is hard-deleted, and every
// §1 retention rule is asserted on the live schema.
func TestAccountHardDeletePersists(t *testing.T) {
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
	mkUser := func(name string) sqlcgen.User {
		u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
			Username:     fmt.Sprintf("%s-%d", name, suffix),
			Email:        fmt.Sprintf("%s-%d@example.test", name, suffix),
			PasswordHash: "$2a$12$fakefakefakefakefakefakefakefakefakefakefakefakefakefe",
			Role:         "user",
		})
		if err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		t.Cleanup(func() {
			_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
		})
		return u
	}
	victim := mkUser("del-it")
	other := mkUser("del-other")

	// Owned channel + video with files, caption, tags, HLS blobs.
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: victim.ID, Handle: fmt.Sprintf("del-it-ch-%d", suffix), DisplayName: "Victim"})
	if err != nil {
		t.Fatal(err)
	}
	vid, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "mine", Privacy: "public", CommentsPolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	originalKey := "web-videos/" + vid.ID.String() + ".mp4"
	thumbKey := "thumbnails/" + vid.ID.String() + ".jpg"
	captionKey := "captions/" + vid.ID.String() + "/en.vtt"
	hlsKey := "streaming-playlists/" + vid.ID.String() + "/720p/seg_00001.ts"
	for _, key := range []string{originalKey, thumbKey, captionKey, hlsKey} {
		if _, err := blobs.Put(ctx, key, strings.NewReader("blob")); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []sqlcgen.CreateVideoFileParams{
		{VideoID: vid.ID, Kind: "original", StorageKey: originalKey, SizeBytes: 4},
		{VideoID: vid.ID, Kind: "thumbnail", StorageKey: thumbKey, SizeBytes: 4},
	} {
		if _, err := q.CreateVideoFile(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.UpsertCaption(ctx, sqlcgen.UpsertCaptionParams{
		VideoID: vid.ID, Language: "en", Label: "English", StorageKey: captionKey}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: vid.ID, Tags: []string{"go"}}); err != nil {
		t.Fatal(err)
	}

	// Other user's surviving channel + video, with the victim's comment on it
	// and the other user's REPLY under that comment (thread preservation).
	otherCh, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: other.ID, Handle: fmt.Sprintf("del-other-ch-%d", suffix), DisplayName: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	otherVid, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: otherCh.ID, Title: "theirs", Privacy: "public", CommentsPolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	victimComment, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: otherVid.ID, UserID: pgtype.UUID{Bytes: victim.ID, Valid: true}, Body: "hello from victim"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID: otherVid.ID, UserID: pgtype.UUID{Bytes: other.ID, Valid: true},
		Body: "reply", ParentID: pgtype.UUID{Bytes: victimComment.ID, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}

	// The victim's per-user rows across the schema.
	if err := q.UpsertVideoRating(ctx, sqlcgen.UpsertVideoRatingParams{UserID: victim.ID, VideoID: otherVid.ID, Rating: "like"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SaveVideo(ctx, sqlcgen.SaveVideoParams{UserID: victim.ID, VideoID: otherVid.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertWatchProgress(ctx, sqlcgen.UpsertWatchProgressParams{UserID: victim.ID, VideoID: otherVid.ID, PositionSeconds: 12}); err != nil {
		t.Fatal(err)
	}
	pl, err := q.CreatePlaylist(ctx, sqlcgen.CreatePlaylistParams{OwnerID: victim.ID, Title: "mine", Visibility: "private"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddPlaylistItem(ctx, sqlcgen.AddPlaylistItemParams{PlaylistID: pl.ID, VideoID: otherVid.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.FollowChannel(ctx, sqlcgen.FollowChannelParams{FollowerID: victim.ID, ChannelID: otherCh.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MuteAccount(ctx, sqlcgen.MuteAccountParams{MuterID: victim.ID, MutedID: other.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MuteAccount(ctx, sqlcgen.MuteAccountParams{MuterID: other.ID, MutedID: victim.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MuteInstance(ctx, sqlcgen.MuteInstanceParams{MuterID: victim.ID, Domain: "far.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.BlockUser(ctx, sqlcgen.BlockUserParams{BlockerID: victim.ID, BlockedID: other.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateOAuthIdentity(ctx, sqlcgen.CreateOAuthIdentityParams{
		Provider: "google", Subject: fmt.Sprintf("sub-%d", suffix), UserID: victim.ID, Email: victim.Email}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertUserMFA(ctx, sqlcgen.UpsertUserMFAParams{UserID: victim.ID, TotpSecretSealed: "enc:x"}); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateRecoveryCode(ctx, sqlcgen.CreateRecoveryCodeParams{UserID: victim.ID, CodeHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{UserID: victim.ID, Type: "follow"}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertNotificationPref(ctx, sqlcgen.UpsertNotificationPrefParams{UserID: victim.ID, Type: "follow", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreatePasswordResetToken(ctx, sqlcgen.CreatePasswordResetTokenParams{
		UserID: victim.ID, TokenHash: fmt.Sprintf("rh-%d", suffix), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateEmailVerificationToken(ctx, sqlcgen.CreateEmailVerificationTokenParams{
		UserID: victim.ID, TokenHash: fmt.Sprintf("vh-%d", suffix), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertAccountActorKeyIfAbsent(ctx, sqlcgen.InsertAccountActorKeyIfAbsentParams{
		UserID: victim.ID, PublicKeyPem: "pub", PrivateKeyPem: "priv"}); err != nil {
		t.Fatal(err)
	}
	avatarKey := "avatars/users/" + victim.ID.String() + ".png"
	if _, err := blobs.Put(ctx, avatarKey, strings.NewReader("img")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertUserImage(ctx, sqlcgen.UpsertUserImageParams{
		UserID: victim.ID, Kind: "avatar", StorageKey: avatarKey, ContentType: "image/png", SizeBytes: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
		UserID: victim.ID, RefreshHash: fmt.Sprintf("sess-%d", suffix), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateAccountExport(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}

	// Real video service so the delete path (and its hooks) is the production
	// one; the hook capture proves the federation-Delete seam fired.
	var hookVideo uuid.UUID
	var hookWasPublic bool
	videosvc := video.NewService(q, blobs, video.WithDeleteHook(func(_ context.Context, id, _ uuid.UUID, wasPublic bool) {
		hookVideo, hookWasPublic = id, wasPublic
	}))

	svc := NewService(q, blobs, videosvc)
	if err := svc.Delete(ctx, victim.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// --- users row anonymised, not removed.
	row := st.Pool.QueryRow(ctx, `
		SELECT username, email, password_hash, display_name, bio, is_active, deleted_at IS NOT NULL
		FROM users WHERE id = $1`, victim.ID)
	var username, email, hash, displayName, bio string
	var isActive, hasDeletedAt bool
	if err := row.Scan(&username, &email, &hash, &displayName, &bio, &isActive, &hasDeletedAt); err != nil {
		t.Fatalf("users row gone (must be anonymised, not removed): %v", err)
	}
	if !strings.HasPrefix(username, "deleted-") || len(username) != len("deleted-")+8 {
		t.Errorf("username = %q, want deleted-<8char>", username)
	}
	if !strings.HasPrefix(email, "deleted-") || !strings.HasSuffix(email, "@deleted.invalid") {
		t.Errorf("email = %q, want deleted sentinel", email)
	}
	if hash != "" || displayName != "" || bio != "" || isActive || !hasDeletedAt {
		t.Errorf("row not fully anonymised: hash=%q dn=%q bio=%q active=%v deleted=%v",
			hash, displayName, bio, isActive, hasDeletedAt)
	}

	// --- owned channel + video hard-deleted; hook fired for the public video.
	assertCount := func(query string, want int, args ...any) {
		t.Helper()
		var n int
		if err := st.Pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		if n != want {
			t.Errorf("count %q = %d, want %d", query, n, want)
		}
	}
	assertCount(`SELECT count(*) FROM channels WHERE owner_id = $1`, 0, victim.ID)
	assertCount(`SELECT count(*) FROM videos WHERE id = $1`, 0, vid.ID)
	assertCount(`SELECT count(*) FROM video_files WHERE video_id = $1`, 0, vid.ID)
	assertCount(`SELECT count(*) FROM captions WHERE video_id = $1`, 0, vid.ID)
	if hookVideo != vid.ID || !hookWasPublic {
		t.Errorf("delete hook = (%s, public=%v), want (%s, true)", hookVideo, hookWasPublic, vid.ID)
	}
	for _, key := range []string{originalKey, thumbKey, captionKey, hlsKey, avatarKey} {
		if ok, _ := blobs.Exists(ctx, key); ok {
			t.Errorf("blob %q not deleted", key)
		}
	}

	// --- comment tombstoned, thread preserved.
	var body string
	var deletedAt pgtype.Timestamptz
	if err := st.Pool.QueryRow(ctx,
		`SELECT body, deleted_at FROM comments WHERE id = $1`, victimComment.ID).Scan(&body, &deletedAt); err != nil {
		t.Fatalf("victim comment gone (must be tombstoned): %v", err)
	}
	if body != "" || !deletedAt.Valid {
		t.Errorf("comment not tombstoned: body=%q deleted=%v", body, deletedAt.Valid)
	}
	assertCount(`SELECT count(*) FROM comments WHERE id = $1 AND parent_id = $2`, 1, reply.ID, victimComment.ID)

	// --- per-user rows hard-deleted.
	for query, label := range map[string]string{
		`SELECT count(*) FROM video_ratings WHERE user_id = $1`:                     "ratings",
		`SELECT count(*) FROM saved_videos WHERE user_id = $1`:                      "saved",
		`SELECT count(*) FROM watch_history WHERE user_id = $1`:                     "history",
		`SELECT count(*) FROM playlists WHERE owner_id = $1`:                        "playlists",
		`SELECT count(*) FROM channel_follows WHERE follower_id = $1`:               "follows",
		`SELECT count(*) FROM muted_accounts WHERE muter_id = $1 OR muted_id = $1`:  "mutes",
		`SELECT count(*) FROM muted_instances WHERE muter_id = $1`:                  "instance mutes",
		`SELECT count(*) FROM user_blocks WHERE blocker_id = $1 OR blocked_id = $1`: "blocks",
		`SELECT count(*) FROM oauth_identities WHERE user_id = $1`:                  "oauth identities",
		`SELECT count(*) FROM user_mfa WHERE user_id = $1`:                          "mfa",
		`SELECT count(*) FROM mfa_recovery_codes WHERE user_id = $1`:                "recovery codes",
		`SELECT count(*) FROM notifications WHERE user_id = $1`:                     "notifications",
		`SELECT count(*) FROM notification_prefs WHERE user_id = $1`:                "notification prefs",
		`SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`:             "reset tokens",
		`SELECT count(*) FROM email_verification_tokens WHERE user_id = $1`:         "verify tokens",
		`SELECT count(*) FROM account_actor_keys WHERE user_id = $1`:                "actor keys",
		`SELECT count(*) FROM user_images WHERE user_id = $1`:                       "user images",
		`SELECT count(*) FROM account_exports WHERE user_id = $1`:                   "account exports",
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL`:   "active sessions",
	} {
		var n int
		if err := st.Pool.QueryRow(ctx, query, victim.ID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", label, err)
		}
		if n != 0 {
			t.Errorf("%s not purged: %d rows remain", label, n)
		}
	}

	// --- a second delete refuses (already deleted).
	if err := svc.Delete(ctx, victim.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

// TestAccountExportQueuePersists proves the account_exports queue (migration
// 0057) end-to-end on real PostgreSQL: request (single-active refused), claim
// → archive written → done with a 7-day expiry, download round-trip, and the
// expiry sweeper collecting blob + row.
func TestAccountExportQueuePersists(t *testing.T) {
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
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     fmt.Sprintf("exp-it-%d", suffix),
		Email:        fmt.Sprintf("exp-it-%d@example.test", suffix),
		PasswordHash: "x", Role: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})
	ch, err := q.CreateChannel(ctx, sqlcgen.CreateChannelParams{
		OwnerID: user.ID, Handle: fmt.Sprintf("exp-it-ch-%d", suffix), DisplayName: "Exporter"})
	if err != nil {
		t.Fatal(err)
	}
	vid, err := q.CreateVideo(ctx, sqlcgen.CreateVideoParams{
		ChannelID: ch.ID, Title: "clip", Privacy: "public", CommentsPolicy: "enabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertVideoTags(ctx, sqlcgen.InsertVideoTagsParams{VideoID: vid.ID, Tags: []string{"tag1"}}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(q, blobs, nil, WithBaseURL("https://it.example"))

	if _, err := svc.RequestExport(ctx, user.ID); err != nil {
		t.Fatalf("RequestExport: %v", err)
	}
	// Single active export per user (the partial unique index).
	if _, err := svc.RequestExport(ctx, user.ID); !errors.Is(err, ErrExportActive) {
		t.Fatalf("second request = %v, want ErrExportActive", err)
	}

	if n, err := svc.DrainExports(ctx, 5); err != nil || n != 1 {
		t.Fatalf("DrainExports = %d, %v; want 1", n, err)
	}
	rc, row, err := svc.OpenExport(ctx, user.ID)
	if err != nil {
		t.Fatalf("OpenExport: %v", err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	var archive Archive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatalf("archive JSON: %v", err)
	}
	if len(archive.Videos) != 1 || archive.Videos[0].OriginalDownloadURL !=
		"https://it.example/api/v1/videos/"+vid.ID.String()+"/original" {
		t.Fatalf("archive videos = %+v", archive.Videos)
	}
	if len(archive.Videos[0].Tags) != 1 || archive.Videos[0].Tags[0] != "tag1" {
		t.Fatalf("archive tags = %+v", archive.Videos[0].Tags)
	}
	if strings.Contains(string(raw), "password") {
		t.Error("archive mentions password")
	}

	// Force-expire, then sweep: blob and row are collected.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE account_exports SET expires_at = now() - interval '1 hour' WHERE id = $1`, row.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.SweepExpiredExports(ctx, 10); err != nil || n < 1 {
		t.Fatalf("SweepExpiredExports = %d, %v; want >=1", n, err)
	}
	if ok, _ := blobs.Exists(ctx, row.StorageKey); ok {
		t.Error("expired archive blob still present")
	}
	if _, err := svc.LatestExport(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestExport after sweep = %v, want ErrNotFound", err)
	}
}
