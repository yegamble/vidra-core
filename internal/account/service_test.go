package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ---------------------------------------------------------------------------
// fakes

// fakeBlobs is an in-memory storage.Backend + PrefixDeleter.
type fakeBlobs struct {
	objects map[string][]byte
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{objects: map[string][]byte{}} }

func (f *fakeBlobs) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	f.objects[key] = b
	return int64(len(b)), nil
}

func (f *fakeBlobs) Open(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeBlobs) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func (f *fakeBlobs) Exists(_ context.Context, key string) (bool, error) {
	_, ok := f.objects[key]
	return ok, nil
}

func (f *fakeBlobs) DeletePrefix(_ context.Context, prefix string) error {
	for k := range f.objects {
		if k == prefix || strings.HasPrefix(k, prefix+"/") {
			delete(f.objects, k)
		}
	}
	return nil
}

// fakeVideoDeleter records the videos deleted through the video service (the
// seam that fires the federation Delete hooks in production).
type fakeVideoDeleter struct {
	deleted []uuid.UUID
	err     error
}

func (f *fakeVideoDeleter) Delete(_ context.Context, _, id uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

// fakeRepo is an in-memory account.Repository.
type fakeRepo struct {
	user sqlcgen.User

	channels      []sqlcgen.Channel
	videos        map[uuid.UUID][]sqlcgen.ListVideosByChannelRow // by channel
	videoFiles    map[uuid.UUID][]sqlcgen.VideoFile
	captions      map[uuid.UUID][]sqlcgen.Caption
	userImages    map[string]sqlcgen.UserImage
	channelImages map[uuid.UUID]map[string]sqlcgen.ChannelImage

	purged          []string // names of the per-user purges invoked
	tombstoned      []uuid.UUID
	sessionsRevoked int
	deletedChannels []uuid.UUID
	anonymized      []sqlcgen.AnonymizeDeletedUserParams
	anonymizeErrs   int // fail the first N anonymize calls

	exports []sqlcgen.AccountExport

	// export data
	prefs         []sqlcgen.ListNotificationPrefsRow
	exportVideos  []sqlcgen.ListVideosByOwnerForExportRow
	tags          map[uuid.UUID][]string
	comments      []sqlcgen.ListCommentsByUserForExportRow
	follows       []sqlcgen.ListFollowedChannelsByUserRow
	saved         []sqlcgen.ListSavedVideosByUserForExportRow
	history       []sqlcgen.ListWatchHistoryByUserForExportRow
	playlists     []sqlcgen.ListPlaylistsByOwnerRow
	playlistItems map[uuid.UUID][]uuid.UUID

	// import records
	profileUpdates   []sqlcgen.UpdateUserProfileParams
	createdPlaylists []sqlcgen.CreatePlaylistParams
	addedItems       []sqlcgen.AddPlaylistItemParams
	followed         []sqlcgen.FollowChannelParams
	upsertedPrefs    []sqlcgen.UpsertNotificationPrefParams
	localVideos      map[uuid.UUID]bool
	localChannels    map[string]sqlcgen.Channel
}

func newFakeRepo(user sqlcgen.User) *fakeRepo {
	return &fakeRepo{
		user:          user,
		videos:        map[uuid.UUID][]sqlcgen.ListVideosByChannelRow{},
		videoFiles:    map[uuid.UUID][]sqlcgen.VideoFile{},
		captions:      map[uuid.UUID][]sqlcgen.Caption{},
		userImages:    map[string]sqlcgen.UserImage{},
		channelImages: map[uuid.UUID]map[string]sqlcgen.ChannelImage{},
		tags:          map[uuid.UUID][]string{},
		playlistItems: map[uuid.UUID][]uuid.UUID{},
		localVideos:   map[uuid.UUID]bool{},
		localChannels: map[string]sqlcgen.Channel{},
	}
}

func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (sqlcgen.User, error) {
	if id != f.user.ID {
		return sqlcgen.User{}, errors.New("not found")
	}
	return f.user, nil
}

func (f *fakeRepo) AnonymizeDeletedUser(_ context.Context, arg sqlcgen.AnonymizeDeletedUserParams) (int64, error) {
	if f.anonymizeErrs > 0 {
		f.anonymizeErrs--
		return 0, errors.New("unique violation")
	}
	f.anonymized = append(f.anonymized, arg)
	f.user.Username = arg.Username
	f.user.Email = arg.Email
	f.user.PasswordHash = ""
	f.user.IsActive = false
	f.user.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return 1, nil
}

func (f *fakeRepo) RevokeAllUserSessions(_ context.Context, _ uuid.UUID) error {
	f.sessionsRevoked++
	return nil
}

func (f *fakeRepo) ListChannelsByOwner(_ context.Context, _ uuid.UUID) ([]sqlcgen.Channel, error) {
	return f.channels, nil
}

func (f *fakeRepo) ListVideosByChannel(_ context.Context, ch uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error) {
	return f.videos[ch], nil
}

func (f *fakeRepo) DeleteChannel(_ context.Context, id uuid.UUID) error {
	f.deletedChannels = append(f.deletedChannels, id)
	return nil
}

func (f *fakeRepo) ListVideoFiles(_ context.Context, id uuid.UUID) ([]sqlcgen.VideoFile, error) {
	return f.videoFiles[id], nil
}

func (f *fakeRepo) ListCaptionsByVideo(_ context.Context, id uuid.UUID) ([]sqlcgen.Caption, error) {
	return f.captions[id], nil
}

func (f *fakeRepo) GetUserImage(_ context.Context, arg sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error) {
	img, ok := f.userImages[arg.Kind]
	if !ok {
		return sqlcgen.UserImage{}, errors.New("not found")
	}
	return img, nil
}

func (f *fakeRepo) GetChannelImage(_ context.Context, arg sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error) {
	img, ok := f.channelImages[arg.ChannelID][arg.Kind]
	if !ok {
		return sqlcgen.ChannelImage{}, errors.New("not found")
	}
	return img, nil
}

func (f *fakeRepo) TombstoneUserComments(_ context.Context, id pgtype.UUID) error {
	f.tombstoned = append(f.tombstoned, uuid.UUID(id.Bytes))
	return nil
}

func (f *fakeRepo) purge(name string) error {
	f.purged = append(f.purged, name)
	return nil
}

func (f *fakeRepo) DeleteVideoRatingsByUser(context.Context, uuid.UUID) error {
	return f.purge("ratings")
}
func (f *fakeRepo) DeleteSavedVideosByUser(context.Context, uuid.UUID) error { return f.purge("saved") }
func (f *fakeRepo) ClearWatchHistory(context.Context, uuid.UUID) error       { return f.purge("history") }
func (f *fakeRepo) DeletePlaylistsByOwner(context.Context, uuid.UUID) error {
	return f.purge("playlists")
}
func (f *fakeRepo) DeleteChannelFollowsByFollower(context.Context, uuid.UUID) error {
	return f.purge("follows")
}
func (f *fakeRepo) DeleteRemoteChannelFollowsByUser(context.Context, uuid.UUID) error {
	return f.purge("remote_follows")
}
func (f *fakeRepo) DeleteMutedAccountsByUser(context.Context, uuid.UUID) error {
	return f.purge("mutes")
}
func (f *fakeRepo) DeleteMutedInstancesByUser(context.Context, uuid.UUID) error {
	return f.purge("instance_mutes")
}
func (f *fakeRepo) DeleteUserBlocksByUser(context.Context, uuid.UUID) error { return f.purge("blocks") }
func (f *fakeRepo) DeleteOAuthIdentitiesByUser(context.Context, uuid.UUID) error {
	return f.purge("oauth")
}
func (f *fakeRepo) DeleteUserMFA(context.Context, uuid.UUID) (int64, error) {
	return 1, f.purge("mfa")
}
func (f *fakeRepo) DeleteRecoveryCodes(context.Context, uuid.UUID) error {
	return f.purge("recovery_codes")
}
func (f *fakeRepo) DeleteNotificationsByUser(context.Context, uuid.UUID) error {
	return f.purge("notifications")
}
func (f *fakeRepo) DeleteNotificationPrefsByUser(context.Context, uuid.UUID) error {
	return f.purge("notification_prefs")
}
func (f *fakeRepo) DeletePasswordResetTokensByUser(context.Context, uuid.UUID) error {
	return f.purge("reset_tokens")
}
func (f *fakeRepo) DeleteEmailVerificationTokensByUser(context.Context, uuid.UUID) error {
	return f.purge("verify_tokens")
}
func (f *fakeRepo) DeleteAccountActorKey(context.Context, uuid.UUID) error {
	return f.purge("actor_key")
}

func (f *fakeRepo) DeleteUserImage(_ context.Context, arg sqlcgen.DeleteUserImageParams) (int64, error) {
	delete(f.userImages, arg.Kind)
	return 1, nil
}

// --- export queue

func (f *fakeRepo) CreateAccountExport(_ context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	for _, e := range f.exports {
		if e.UserID == userID && (e.State == ExportPending || e.State == ExportRunning) {
			return sqlcgen.AccountExport{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
		}
	}
	row := sqlcgen.AccountExport{
		ID: uuid.New(), UserID: userID, State: ExportPending,
		NextAttemptAt: time.Now().Add(-time.Second), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.exports = append(f.exports, row)
	return row, nil
}

func (f *fakeRepo) GetLatestAccountExportByUser(_ context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	for i := len(f.exports) - 1; i >= 0; i-- {
		if f.exports[i].UserID == userID {
			return f.exports[i], nil
		}
	}
	return sqlcgen.AccountExport{}, pgx.ErrNoRows
}

func (f *fakeRepo) DeleteInactiveAccountExportsByUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	var keys []string
	kept := f.exports[:0]
	for _, e := range f.exports {
		if e.UserID == userID && (e.State == ExportDone || e.State == ExportFailed) {
			keys = append(keys, e.StorageKey)
			continue
		}
		kept = append(kept, e)
	}
	f.exports = kept
	return keys, nil
}

func (f *fakeRepo) DeleteAccountExportsByUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	var keys []string
	kept := f.exports[:0]
	for _, e := range f.exports {
		if e.UserID == userID {
			keys = append(keys, e.StorageKey)
			continue
		}
		kept = append(kept, e)
	}
	f.exports = kept
	return keys, nil
}

func (f *fakeRepo) ClaimDueAccountExports(_ context.Context, limit int32) ([]sqlcgen.ClaimDueAccountExportsRow, error) {
	var out []sqlcgen.ClaimDueAccountExportsRow
	now := time.Now()
	for i := range f.exports {
		if int32(len(out)) >= limit {
			break
		}
		if f.exports[i].State == ExportPending && !f.exports[i].NextAttemptAt.After(now) {
			f.exports[i].State = ExportRunning
			out = append(out, sqlcgen.ClaimDueAccountExportsRow{
				ID: f.exports[i].ID, UserID: f.exports[i].UserID, Attempts: f.exports[i].Attempts,
			})
		}
	}
	return out, nil
}

func (f *fakeRepo) CompleteAccountExport(_ context.Context, arg sqlcgen.CompleteAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = ExportDone
			f.exports[i].StorageKey = arg.StorageKey
			f.exports[i].ExpiresAt = arg.ExpiresAt
		}
	}
	return nil
}

func (f *fakeRepo) RescheduleAccountExport(_ context.Context, arg sqlcgen.RescheduleAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = ExportPending
			f.exports[i].Attempts++
			f.exports[i].NextAttemptAt = arg.NextAttemptAt
			f.exports[i].LastError = arg.LastError
		}
	}
	return nil
}

func (f *fakeRepo) FailAccountExport(_ context.Context, arg sqlcgen.FailAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = ExportFailed
			f.exports[i].Attempts++
			f.exports[i].LastError = arg.LastError
		}
	}
	return nil
}

func (f *fakeRepo) ListExpiredAccountExports(_ context.Context, limit int32) ([]sqlcgen.ListExpiredAccountExportsRow, error) {
	var out []sqlcgen.ListExpiredAccountExportsRow
	now := time.Now()
	for _, e := range f.exports {
		if int32(len(out)) >= limit {
			break
		}
		if e.ExpiresAt.Valid && !e.ExpiresAt.Time.After(now) {
			out = append(out, sqlcgen.ListExpiredAccountExportsRow{ID: e.ID, StorageKey: e.StorageKey})
		}
	}
	return out, nil
}

func (f *fakeRepo) DeleteAccountExport(_ context.Context, id uuid.UUID) error {
	kept := f.exports[:0]
	for _, e := range f.exports {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	f.exports = kept
	return nil
}

// --- export data reads

func (f *fakeRepo) ListVideosByOwnerForExport(context.Context, uuid.UUID) ([]sqlcgen.ListVideosByOwnerForExportRow, error) {
	return f.exportVideos, nil
}
func (f *fakeRepo) ListVideoTags(_ context.Context, id uuid.UUID) ([]string, error) {
	return f.tags[id], nil
}
func (f *fakeRepo) ListCommentsByUserForExport(context.Context, pgtype.UUID) ([]sqlcgen.ListCommentsByUserForExportRow, error) {
	return f.comments, nil
}
func (f *fakeRepo) ListFollowedChannelsByUser(context.Context, uuid.UUID) ([]sqlcgen.ListFollowedChannelsByUserRow, error) {
	return f.follows, nil
}
func (f *fakeRepo) ListSavedVideosByUserForExport(context.Context, uuid.UUID) ([]sqlcgen.ListSavedVideosByUserForExportRow, error) {
	return f.saved, nil
}
func (f *fakeRepo) ListWatchHistoryByUserForExport(context.Context, uuid.UUID) ([]sqlcgen.ListWatchHistoryByUserForExportRow, error) {
	return f.history, nil
}
func (f *fakeRepo) ListPlaylistsByOwner(context.Context, uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	return f.playlists, nil
}
func (f *fakeRepo) ListPlaylistItemVideoIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return f.playlistItems[id], nil
}
func (f *fakeRepo) ListNotificationPrefs(context.Context, uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error) {
	return f.prefs, nil
}

// --- import writes

func (f *fakeRepo) UpdateUserProfile(_ context.Context, arg sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error) {
	f.profileUpdates = append(f.profileUpdates, arg)
	return f.user, nil
}

func (f *fakeRepo) CreatePlaylist(_ context.Context, arg sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error) {
	f.createdPlaylists = append(f.createdPlaylists, arg)
	return sqlcgen.Playlist{ID: uuid.New(), OwnerID: arg.OwnerID, Title: arg.Title, Visibility: arg.Visibility}, nil
}

func (f *fakeRepo) AddPlaylistItem(_ context.Context, arg sqlcgen.AddPlaylistItemParams) error {
	f.addedItems = append(f.addedItems, arg)
	return nil
}

func (f *fakeRepo) GetVideoByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if f.localVideos[id] {
		return sqlcgen.GetVideoByIDRow{ID: id}, nil
	}
	return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
}

func (f *fakeRepo) GetChannelByHandle(_ context.Context, handle string) (sqlcgen.Channel, error) {
	ch, ok := f.localChannels[strings.ToLower(handle)]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func (f *fakeRepo) FollowChannel(_ context.Context, arg sqlcgen.FollowChannelParams) (int64, error) {
	f.followed = append(f.followed, arg)
	return 1, nil
}

func (f *fakeRepo) UpsertNotificationPref(_ context.Context, arg sqlcgen.UpsertNotificationPrefParams) error {
	f.upsertedPrefs = append(f.upsertedPrefs, arg)
	return nil
}

// ---------------------------------------------------------------------------
// helpers

func testUser() sqlcgen.User {
	return sqlcgen.User{
		ID:           uuid.New(),
		Username:     "alice",
		Email:        "alice@example.test",
		PasswordHash: "$2a$12$notarealhashnotarealhashnotarealhash",
		Role:         "user",
		IsActive:     true,
		DisplayName:  "Alice",
		Bio:          "hi",
		CreatedAt:    time.Now().Add(-24 * time.Hour),
	}
}

// ---------------------------------------------------------------------------
// hard delete

func TestDeleteRunsFullChecklist(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	blobs := newFakeBlobs()
	deleter := &fakeVideoDeleter{}

	chID, vID := uuid.New(), uuid.New()
	repo.channels = []sqlcgen.Channel{{ID: chID, OwnerID: user.ID, Handle: "alice-main"}}
	repo.videos[chID] = []sqlcgen.ListVideosByChannelRow{{ID: vID}}
	repo.videoFiles[vID] = []sqlcgen.VideoFile{
		{StorageKey: "web-videos/" + vID.String() + ".mp4"},
		{StorageKey: "thumbnails/" + vID.String() + ".jpg"},
	}
	repo.captions[vID] = []sqlcgen.Caption{{StorageKey: "captions/" + vID.String() + "/en.vtt"}}
	repo.userImages["avatar"] = sqlcgen.UserImage{StorageKey: "avatars/users/" + user.ID.String() + ".png"}
	repo.channelImages[chID] = map[string]sqlcgen.ChannelImage{
		"banner": {StorageKey: "banners/channels/" + chID.String() + ".jpg"},
	}
	repo.exports = []sqlcgen.AccountExport{{
		ID: uuid.New(), UserID: user.ID, State: ExportDone,
		StorageKey: "exports/accounts/" + user.ID.String() + "/old.json",
	}}
	for _, key := range []string{
		"web-videos/" + vID.String() + ".mp4",
		"thumbnails/" + vID.String() + ".jpg",
		"captions/" + vID.String() + "/en.vtt",
		"streaming-playlists/" + vID.String() + "/master.m3u8",
		"streaming-playlists/" + vID.String() + "/720p/seg_00001.ts",
		"avatars/users/" + user.ID.String() + ".png",
		"banners/channels/" + chID.String() + ".jpg",
		"exports/accounts/" + user.ID.String() + "/old.json",
	} {
		_, _ = blobs.Put(ctx, key, strings.NewReader("x"))
	}

	svc := NewService(repo, blobs, deleter)
	if err := svc.Delete(ctx, user.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Videos went through the video service (federation delete hooks fire there).
	if len(deleter.deleted) != 1 || deleter.deleted[0] != vID {
		t.Fatalf("video deletions = %v, want [%s]", deleter.deleted, vID)
	}
	// All blobs gone — originals, thumbnail, caption, HLS ladder (prefix),
	// profile/channel images, and the old export archive.
	if len(blobs.objects) != 0 {
		t.Fatalf("blobs left behind: %v", blobs.objects)
	}
	// Channel row removed.
	if len(repo.deletedChannels) != 1 || repo.deletedChannels[0] != chID {
		t.Fatalf("deleted channels = %v", repo.deletedChannels)
	}
	// Comments tombstoned for this user.
	if len(repo.tombstoned) != 1 || repo.tombstoned[0] != user.ID {
		t.Fatalf("tombstoned = %v", repo.tombstoned)
	}
	// Every per-user purge ran.
	want := []string{
		"ratings", "saved", "history", "playlists", "follows", "remote_follows",
		"mutes", "instance_mutes", "blocks", "oauth", "recovery_codes",
		"notifications", "notification_prefs", "reset_tokens", "verify_tokens",
		"actor_key", "mfa",
	}
	got := map[string]bool{}
	for _, p := range repo.purged {
		got[p] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("purge %q did not run (ran: %v)", w, repo.purged)
		}
	}
	// Sessions revoked, row anonymised.
	if repo.sessionsRevoked != 1 {
		t.Fatalf("sessionsRevoked = %d, want 1", repo.sessionsRevoked)
	}
	if len(repo.anonymized) != 1 {
		t.Fatalf("anonymized calls = %d, want 1", len(repo.anonymized))
	}
	arg := repo.anonymized[0]
	if !strings.HasPrefix(arg.Username, "deleted-") || len(arg.Username) != len("deleted-")+8 {
		t.Errorf("anonymised username = %q, want deleted-<8char>", arg.Username)
	}
	if !strings.HasPrefix(arg.Email, "deleted-") || !strings.HasSuffix(arg.Email, "@deleted.invalid") {
		t.Errorf("anonymised email = %q", arg.Email)
	}
	// Export rows for the user are gone.
	if len(repo.exports) != 0 {
		t.Fatalf("export rows left: %v", repo.exports)
	}

	// A second delete refuses: the account is already deleted.
	if err := svc.Delete(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestDeleteUnknownUser(t *testing.T) {
	repo := newFakeRepo(testUser())
	svc := NewService(repo, newFakeBlobs(), &fakeVideoDeleter{})
	if err := svc.Delete(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete unknown = %v, want ErrNotFound", err)
	}
}

func TestDeleteRetriesAnonymizeOnCollision(t *testing.T) {
	user := testUser()
	repo := newFakeRepo(user)
	repo.anonymizeErrs = 2 // first two sentinel attempts "collide"
	svc := NewService(repo, newFakeBlobs(), &fakeVideoDeleter{})
	if err := svc.Delete(context.Background(), user.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(repo.anonymized) != 1 {
		t.Fatalf("anonymized = %d calls recorded, want 1 success", len(repo.anonymized))
	}
}

func TestDeleteAbortsWhenVideoDeleteFails(t *testing.T) {
	user := testUser()
	repo := newFakeRepo(user)
	chID, vID := uuid.New(), uuid.New()
	repo.channels = []sqlcgen.Channel{{ID: chID, OwnerID: user.ID}}
	repo.videos[chID] = []sqlcgen.ListVideosByChannelRow{{ID: vID}}
	svc := NewService(repo, newFakeBlobs(), &fakeVideoDeleter{err: errors.New("boom")})
	if err := svc.Delete(context.Background(), user.ID); err == nil {
		t.Fatal("Delete succeeded despite video delete failure")
	}
	if len(repo.anonymized) != 0 {
		t.Fatal("user was anonymised despite an aborted delete")
	}
}

// ---------------------------------------------------------------------------
// export

func TestRequestExportSingleActive(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	svc := NewService(repo, newFakeBlobs(), nil)

	first, err := svc.RequestExport(ctx, user.ID)
	if err != nil {
		t.Fatalf("RequestExport: %v", err)
	}
	if first.State != ExportPending {
		t.Fatalf("state = %q, want pending", first.State)
	}
	// A second request while one is active is refused.
	if _, err := svc.RequestExport(ctx, user.ID); !errors.Is(err, ErrExportActive) {
		t.Fatalf("second RequestExport = %v, want ErrExportActive", err)
	}
}

func TestRequestExportReplacesFinishedArchive(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	blobs := newFakeBlobs()
	oldKey := "exports/accounts/" + user.ID.String() + "/old.json"
	_, _ = blobs.Put(ctx, oldKey, strings.NewReader("old"))
	repo.exports = []sqlcgen.AccountExport{{
		ID: uuid.New(), UserID: user.ID, State: ExportDone, StorageKey: oldKey,
	}}
	svc := NewService(repo, blobs, nil)

	if _, err := svc.RequestExport(ctx, user.ID); err != nil {
		t.Fatalf("RequestExport: %v", err)
	}
	if ok, _ := blobs.Exists(ctx, oldKey); ok {
		t.Fatal("previous archive blob was not deleted")
	}
}

// walkJSONKeys invokes fn for every object key in a decoded JSON value.
func walkJSONKeys(v any, fn func(key string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			fn(k)
			walkJSONKeys(vv, fn)
		}
	case []any:
		for _, vv := range t {
			walkJSONKeys(vv, fn)
		}
	}
}

func TestDrainExportsBuildsArchiveWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	blobs := newFakeBlobs()
	svc := NewService(repo, blobs, nil, WithBaseURL("https://vidra.example"))

	vID, chID, plID := uuid.New(), uuid.New(), uuid.New()
	cat := "22"
	repo.channels = []sqlcgen.Channel{{ID: chID, OwnerID: user.ID, Handle: "alice-main", DisplayName: "Main"}}
	repo.exportVideos = []sqlcgen.ListVideosByOwnerForExportRow{{
		ID: vID, ChannelHandle: "alice-main", Title: "First", Privacy: "public",
		State: "published", Category: &cat, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}
	repo.tags[vID] = []string{"go", "video"}
	repo.playlists = []sqlcgen.ListPlaylistsByOwnerRow{{ID: plID, OwnerID: user.ID, Title: "Faves", Visibility: "private"}}
	repo.playlistItems[plID] = []uuid.UUID{vID}
	repo.prefs = []sqlcgen.ListNotificationPrefsRow{{Type: "follow", Enabled: false}}
	repo.comments = []sqlcgen.ListCommentsByUserForExportRow{{ID: uuid.New(), VideoID: vID, Body: "nice"}}
	repo.follows = []sqlcgen.ListFollowedChannelsByUserRow{{ChannelID: chID, Handle: "other", DisplayName: "Other"}}
	repo.saved = []sqlcgen.ListSavedVideosByUserForExportRow{{VideoID: vID}}
	repo.history = []sqlcgen.ListWatchHistoryByUserForExportRow{{VideoID: vID, PositionSeconds: 42}}

	if _, err := svc.RequestExport(ctx, user.ID); err != nil {
		t.Fatalf("RequestExport: %v", err)
	}
	n, err := svc.DrainExports(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainExports = %d, %v; want 1 completed", n, err)
	}

	row, err := svc.LatestExport(ctx, user.ID)
	if err != nil || row.State != ExportDone {
		t.Fatalf("LatestExport = %+v, %v; want done", row, err)
	}
	if !row.ExpiresAt.Valid || time.Until(row.ExpiresAt.Time) < 6*24*time.Hour {
		t.Fatalf("expires_at = %v, want ~7 days out", row.ExpiresAt)
	}

	rc, _, err := svc.OpenExport(ctx, user.ID)
	if err != nil {
		t.Fatalf("OpenExport: %v", err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()

	var archive Archive
	if err := json.Unmarshal(raw, &archive); err != nil {
		t.Fatalf("archive is not valid JSON: %v", err)
	}
	if archive.Meta.Version != ArchiveVersion || archive.Profile.Username != "alice" {
		t.Fatalf("archive meta/profile wrong: %+v %+v", archive.Meta, archive.Profile)
	}
	if len(archive.Videos) != 1 || archive.Videos[0].OriginalDownloadURL !=
		"https://vidra.example/api/v1/videos/"+vID.String()+"/original" {
		t.Fatalf("video download url wrong: %+v", archive.Videos)
	}
	if len(archive.Videos[0].Tags) != 2 || archive.Videos[0].Category == nil || *archive.Videos[0].Category != "22" {
		t.Fatalf("video taxonomy/tags wrong: %+v", archive.Videos[0])
	}
	if len(archive.Playlists) != 1 || len(archive.Playlists[0].VideoIDs) != 1 {
		t.Fatalf("playlists wrong: %+v", archive.Playlists)
	}
	if len(archive.NotificationPrefs) != 1 || archive.NotificationPrefs[0].Enabled {
		t.Fatalf("prefs wrong: %+v", archive.NotificationPrefs)
	}
	if len(archive.Comments) != 1 || len(archive.Follows) != 1 || len(archive.SavedVideos) != 1 || len(archive.WatchHistory) != 1 {
		t.Fatalf("sections missing: %+v", archive)
	}

	// NO secrets: no denylisted key anywhere in the document, and the stored
	// password hash never appears in the payload.
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	walkJSONKeys(doc, func(key string) {
		if observability.IsSensitiveKey(key) {
			t.Errorf("archive contains denylisted key %q", key)
		}
		if key == "password_hash" {
			t.Errorf("archive contains password_hash")
		}
	})
	if strings.Contains(string(raw), user.PasswordHash) {
		t.Error("archive contains the password hash value")
	}
}

func TestDrainExportsRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	// nil blobs → runExport fails every attempt.
	svc := NewService(repo, nil, nil)

	if _, err := svc.RequestExport(ctx, user.ID); err != nil {
		t.Fatalf("RequestExport: %v", err)
	}
	for i := 0; i < exportMaxAttempts; i++ {
		if _, err := svc.DrainExports(ctx, 10); err != nil {
			t.Fatalf("DrainExports: %v", err)
		}
		// Make the rescheduled job immediately due again.
		for j := range repo.exports {
			repo.exports[j].NextAttemptAt = time.Now().Add(-time.Second)
		}
	}
	row, err := svc.LatestExport(ctx, user.ID)
	if err != nil {
		t.Fatalf("LatestExport: %v", err)
	}
	if row.State != ExportFailed {
		t.Fatalf("state after %d attempts = %q, want failed", exportMaxAttempts, row.State)
	}
	if row.LastError == "" {
		t.Fatal("dead-lettered job has no last_error")
	}
	// A failed export is not downloadable.
	if _, _, err := svc.OpenExport(ctx, user.ID); !errors.Is(err, ErrExportNotReady) {
		t.Fatalf("OpenExport after failure = %v, want ErrExportNotReady", err)
	}
}

func TestSweepExpiredExports(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	blobs := newFakeBlobs()
	key := "exports/accounts/" + user.ID.String() + "/x.json"
	_, _ = blobs.Put(ctx, key, strings.NewReader("{}"))
	repo.exports = []sqlcgen.AccountExport{{
		ID: uuid.New(), UserID: user.ID, State: ExportDone, StorageKey: key,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}}
	svc := NewService(repo, blobs, nil)

	// Expired archive: download refused even before the sweep runs.
	if _, _, err := svc.OpenExport(ctx, user.ID); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("OpenExport expired = %v, want ErrExportExpired", err)
	}

	n, err := svc.SweepExpiredExports(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("SweepExpiredExports = %d, %v; want 1", n, err)
	}
	if ok, _ := blobs.Exists(ctx, key); ok {
		t.Fatal("expired archive blob not deleted")
	}
	if len(repo.exports) != 0 {
		t.Fatal("expired export row not deleted")
	}
	// Nothing left → status is not found.
	if _, err := svc.LatestExport(ctx, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestExport after sweep = %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// import

func TestParseArchiveRejectsGarbage(t *testing.T) {
	if _, err := ParseArchive([]byte("not json")); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("garbage = %v, want ErrInvalidArchive", err)
	}
	if _, err := ParseArchive([]byte(`{"vidra_export":{"version":99}}`)); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("unknown version = %v, want ErrInvalidArchive", err)
	}
	if _, err := ParseArchive([]byte(`{}`)); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("missing marker = %v, want ErrInvalidArchive", err)
	}
}

func TestImportArchiveAppliesSafeSubsets(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	svc := NewService(repo, newFakeBlobs(), nil)

	localVideo := uuid.New()
	repo.localVideos[localVideo] = true
	repo.localChannels["other"] = sqlcgen.Channel{ID: uuid.New(), Handle: "other"}

	archive := Archive{
		Meta:    ArchiveMeta{Version: ArchiveVersion},
		Profile: ArchiveProfile{DisplayName: "Alice Imported", Bio: "from archive"},
		NotificationPrefs: []ArchivePref{
			{Type: "follow", Enabled: false},
			{Type: "not_a_real_type", Enabled: true},
		},
		Playlists: []ArchivePlaylist{{
			Title:      "Faves",
			Visibility: "sneaky", // unknown → private
			VideoIDs:   []string{localVideo.String(), uuid.New().String(), "not-a-uuid"},
		}},
		Follows: []ArchiveFollow{
			{ChannelHandle: "other"},
			{ChannelHandle: "missing"},
			{ChannelHandle: "remote@far.example"},
		},
		Channels:     []ArchiveChannel{{Handle: "alice-main"}},
		Videos:       []ArchiveVideo{{Title: "First"}},
		Comments:     []ArchiveComment{{Body: "nice"}},
		SavedVideos:  []ArchiveSaved{{VideoID: localVideo.String()}},
		WatchHistory: []ArchiveHistory{{VideoID: localVideo.String()}},
	}

	sum, err := svc.ImportArchive(ctx, user.ID, archive)
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}

	if !sum.ProfileApplied || len(repo.profileUpdates) != 1 {
		t.Fatalf("profile not applied: %+v", sum)
	}
	if got := repo.profileUpdates[0]; got.DisplayName == nil || *got.DisplayName != "Alice Imported" ||
		got.Bio == nil || *got.Bio != "from archive" {
		t.Fatalf("profile update = %+v", repo.profileUpdates[0])
	}
	if sum.NotificationPrefsApplied != 1 || sum.NotificationPrefsSkipped != 1 {
		t.Fatalf("prefs summary = %+v", sum)
	}
	if sum.PlaylistsCreated != 1 || len(repo.createdPlaylists) != 1 {
		t.Fatalf("playlists summary = %+v", sum)
	}
	if repo.createdPlaylists[0].Visibility != "private" {
		t.Fatalf("unknown visibility not defaulted: %q", repo.createdPlaylists[0].Visibility)
	}
	if sum.PlaylistItemsAdded != 1 || sum.PlaylistItemsSkipped != 2 {
		t.Fatalf("playlist items summary = %+v", sum)
	}
	if len(repo.addedItems) != 1 || repo.addedItems[0].VideoID != localVideo {
		t.Fatalf("added items = %+v", repo.addedItems)
	}
	if sum.FollowsCreated != 1 || sum.FollowsSkipped != 2 {
		t.Fatalf("follows summary = %+v", sum)
	}
	// Non-importable sections all reported.
	for _, section := range []string{"channels", "videos", "comments", "saved_videos", "watch_history"} {
		if sum.SkippedSections[section] != 1 {
			t.Errorf("skipped_sections[%s] = %d, want 1", section, sum.SkippedSections[section])
		}
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	user := testUser()
	repo := newFakeRepo(user)
	blobs := newFakeBlobs()
	svc := NewService(repo, blobs, nil, WithBaseURL("https://vidra.example"))

	vID, plID := uuid.New(), uuid.New()
	repo.localVideos[vID] = true
	repo.playlists = []sqlcgen.ListPlaylistsByOwnerRow{{ID: plID, OwnerID: user.ID, Title: "Mix", Visibility: "public"}}
	repo.playlistItems[plID] = []uuid.UUID{vID}
	repo.prefs = []sqlcgen.ListNotificationPrefsRow{{Type: "follow", Enabled: false}}

	if _, err := svc.RequestExport(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.DrainExports(ctx, 1); err != nil || n != 1 {
		t.Fatalf("drain = %d, %v", n, err)
	}
	rc, _, err := svc.OpenExport(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()

	archive, err := ParseArchive(raw)
	if err != nil {
		t.Fatalf("ParseArchive on our own export: %v", err)
	}
	sum, err := svc.ImportArchive(ctx, user.ID, archive)
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}
	if sum.PlaylistsCreated != 1 || sum.PlaylistItemsAdded != 1 || sum.NotificationPrefsApplied != 1 {
		t.Fatalf("round-trip summary = %+v", sum)
	}
}
