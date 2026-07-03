package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/account"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// accountFakeBlobs is a tiny in-memory blob store for the export archive.
type accountFakeBlobs struct{ objects map[string][]byte }

func (f *accountFakeBlobs) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	b, _ := io.ReadAll(r)
	f.objects[key] = b
	return int64(len(b)), nil
}

func (f *accountFakeBlobs) Open(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *accountFakeBlobs) Delete(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

func (f *accountFakeBlobs) Exists(_ context.Context, key string) (bool, error) {
	_, ok := f.objects[key]
	return ok, nil
}

// accountFakeRepo is a minimal account.Repository for handler tests. It shares
// the auth fake's user map so a hard delete is observable through the auth
// endpoints (login refusal), and keeps an in-memory export queue. Content
// reads/purges are no-ops — the full delete/export behavior is covered by the
// internal/account unit tests and the real-PG integration test.
type accountFakeRepo struct {
	authRepo *authFakeRepo
	exports  []sqlcgen.AccountExport

	createdPlaylists []sqlcgen.CreatePlaylistParams
	upsertedPrefs    []sqlcgen.UpsertNotificationPrefParams
	profileUpdates   []sqlcgen.UpdateUserProfileParams
}

func (f *accountFakeRepo) GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error) {
	return f.authRepo.GetUserByID(ctx, id)
}

func (f *accountFakeRepo) AnonymizeDeletedUser(_ context.Context, arg sqlcgen.AnonymizeDeletedUserParams) (int64, error) {
	for k, u := range f.authRepo.users {
		if u.ID == arg.ID {
			if u.DeletedAt.Valid {
				return 0, nil
			}
			u.Username = arg.Username
			u.Email = arg.Email
			u.PasswordHash = ""
			u.IsActive = false
			u.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
			delete(f.authRepo.users, k)
			f.authRepo.users[strings.ToLower(arg.Email)] = u
			return 1, nil
		}
	}
	return 0, nil
}

func (f *accountFakeRepo) RevokeAllUserSessions(_ context.Context, userID uuid.UUID) error {
	for id, s := range f.authRepo.sessions {
		if s.UserID == userID {
			delete(f.authRepo.sessions, id)
		}
	}
	return nil
}

func (f *accountFakeRepo) ListChannelsByOwner(context.Context, uuid.UUID) ([]sqlcgen.Channel, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListVideosByChannel(context.Context, uuid.UUID) ([]sqlcgen.ListVideosByChannelRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) DeleteChannel(context.Context, uuid.UUID) error { return nil }
func (f *accountFakeRepo) ListVideoFiles(context.Context, uuid.UUID) ([]sqlcgen.VideoFile, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListCaptionsByVideo(context.Context, uuid.UUID) ([]sqlcgen.Caption, error) {
	return nil, nil
}
func (f *accountFakeRepo) GetUserImage(context.Context, sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error) {
	return sqlcgen.UserImage{}, errors.New("not found")
}
func (f *accountFakeRepo) GetChannelImage(context.Context, sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error) {
	return sqlcgen.ChannelImage{}, errors.New("not found")
}
func (f *accountFakeRepo) TombstoneUserComments(context.Context, pgtype.UUID) error { return nil }
func (f *accountFakeRepo) DeleteVideoRatingsByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteSavedVideosByUser(context.Context, uuid.UUID) error { return nil }
func (f *accountFakeRepo) ClearWatchHistory(context.Context, uuid.UUID) error       { return nil }
func (f *accountFakeRepo) DeletePlaylistsByOwner(context.Context, uuid.UUID) error  { return nil }
func (f *accountFakeRepo) DeleteChannelFollowsByFollower(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteRemoteChannelFollowsByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteMutedAccountsByUser(context.Context, uuid.UUID) error  { return nil }
func (f *accountFakeRepo) DeleteMutedInstancesByUser(context.Context, uuid.UUID) error { return nil }
func (f *accountFakeRepo) DeleteUserBlocksByUser(context.Context, uuid.UUID) error     { return nil }
func (f *accountFakeRepo) DeleteOAuthIdentitiesByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteUserMFA(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (f *accountFakeRepo) DeleteRecoveryCodes(context.Context, uuid.UUID) error    { return nil }
func (f *accountFakeRepo) DeleteNotificationsByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteNotificationPrefsByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeletePasswordResetTokensByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteEmailVerificationTokensByUser(context.Context, uuid.UUID) error {
	return nil
}
func (f *accountFakeRepo) DeleteAccountActorKey(context.Context, uuid.UUID) error { return nil }
func (f *accountFakeRepo) DeleteUserImage(context.Context, sqlcgen.DeleteUserImageParams) (int64, error) {
	return 0, nil
}

func (f *accountFakeRepo) CreateAccountExport(_ context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	for _, e := range f.exports {
		if e.UserID == userID && (e.State == account.ExportPending || e.State == account.ExportRunning) {
			return sqlcgen.AccountExport{}, pgx.ErrNoRows
		}
	}
	row := sqlcgen.AccountExport{
		ID: uuid.New(), UserID: userID, State: account.ExportPending,
		NextAttemptAt: time.Now().Add(-time.Second), CreatedAt: time.Now(),
	}
	f.exports = append(f.exports, row)
	return row, nil
}

func (f *accountFakeRepo) GetLatestAccountExportByUser(_ context.Context, userID uuid.UUID) (sqlcgen.AccountExport, error) {
	for i := len(f.exports) - 1; i >= 0; i-- {
		if f.exports[i].UserID == userID {
			return f.exports[i], nil
		}
	}
	return sqlcgen.AccountExport{}, pgx.ErrNoRows
}

func (f *accountFakeRepo) DeleteInactiveAccountExportsByUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	kept := f.exports[:0]
	var keys []string
	for _, e := range f.exports {
		if e.UserID == userID && (e.State == account.ExportDone || e.State == account.ExportFailed) {
			keys = append(keys, e.StorageKey)
			continue
		}
		kept = append(kept, e)
	}
	f.exports = kept
	return keys, nil
}

func (f *accountFakeRepo) DeleteAccountExportsByUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	kept := f.exports[:0]
	var keys []string
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

func (f *accountFakeRepo) ClaimDueAccountExports(_ context.Context, limit int32) ([]sqlcgen.ClaimDueAccountExportsRow, error) {
	var out []sqlcgen.ClaimDueAccountExportsRow
	for i := range f.exports {
		if int32(len(out)) >= limit {
			break
		}
		if f.exports[i].State == account.ExportPending {
			f.exports[i].State = account.ExportRunning
			out = append(out, sqlcgen.ClaimDueAccountExportsRow{ID: f.exports[i].ID, UserID: f.exports[i].UserID})
		}
	}
	return out, nil
}

func (f *accountFakeRepo) CompleteAccountExport(_ context.Context, arg sqlcgen.CompleteAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = account.ExportDone
			f.exports[i].StorageKey = arg.StorageKey
			f.exports[i].ExpiresAt = arg.ExpiresAt
		}
	}
	return nil
}

func (f *accountFakeRepo) RescheduleAccountExport(_ context.Context, arg sqlcgen.RescheduleAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = account.ExportPending
			f.exports[i].NextAttemptAt = arg.NextAttemptAt
		}
	}
	return nil
}

func (f *accountFakeRepo) FailAccountExport(_ context.Context, arg sqlcgen.FailAccountExportParams) error {
	for i := range f.exports {
		if f.exports[i].ID == arg.ID {
			f.exports[i].State = account.ExportFailed
		}
	}
	return nil
}

func (f *accountFakeRepo) ListExpiredAccountExports(context.Context, int32) ([]sqlcgen.ListExpiredAccountExportsRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) DeleteAccountExport(context.Context, uuid.UUID) error { return nil }

func (f *accountFakeRepo) ListVideosByOwnerForExport(context.Context, uuid.UUID) ([]sqlcgen.ListVideosByOwnerForExportRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListVideoTags(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListCommentsByUserForExport(context.Context, pgtype.UUID) ([]sqlcgen.ListCommentsByUserForExportRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListFollowedChannelsByUser(context.Context, uuid.UUID) ([]sqlcgen.ListFollowedChannelsByUserRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListSavedVideosByUserForExport(context.Context, uuid.UUID) ([]sqlcgen.ListSavedVideosByUserForExportRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListWatchHistoryByUserForExport(context.Context, uuid.UUID) ([]sqlcgen.ListWatchHistoryByUserForExportRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListPlaylistsByOwner(context.Context, uuid.UUID) ([]sqlcgen.ListPlaylistsByOwnerRow, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListPlaylistItemVideoIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *accountFakeRepo) ListNotificationPrefs(context.Context, uuid.UUID) ([]sqlcgen.ListNotificationPrefsRow, error) {
	return []sqlcgen.ListNotificationPrefsRow{{Type: "follow", Enabled: false}}, nil
}

func (f *accountFakeRepo) UpdateUserProfile(_ context.Context, arg sqlcgen.UpdateUserProfileParams) (sqlcgen.User, error) {
	f.profileUpdates = append(f.profileUpdates, arg)
	return sqlcgen.User{}, nil
}
func (f *accountFakeRepo) CreatePlaylist(_ context.Context, arg sqlcgen.CreatePlaylistParams) (sqlcgen.Playlist, error) {
	f.createdPlaylists = append(f.createdPlaylists, arg)
	return sqlcgen.Playlist{ID: uuid.New()}, nil
}
func (f *accountFakeRepo) AddPlaylistItem(context.Context, sqlcgen.AddPlaylistItemParams) error {
	return nil
}
func (f *accountFakeRepo) GetVideoByID(context.Context, uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	return sqlcgen.GetVideoByIDRow{}, errors.New("not found")
}
func (f *accountFakeRepo) GetChannelByHandle(context.Context, string) (sqlcgen.Channel, error) {
	return sqlcgen.Channel{}, errors.New("not found")
}
func (f *accountFakeRepo) FollowChannel(context.Context, sqlcgen.FollowChannelParams) (int64, error) {
	return 1, nil
}
func (f *accountFakeRepo) UpsertNotificationPref(_ context.Context, arg sqlcgen.UpsertNotificationPrefParams) error {
	f.upsertedPrefs = append(f.upsertedPrefs, arg)
	return nil
}

// accountEnv builds a server with the auth + account services wired to shared
// in-memory fakes.
type accountEnv struct {
	srv        *Server
	accountsvc *account.Service
	repo       *accountFakeRepo
	blobs      *accountFakeBlobs
	logs       *bytes.Buffer
}

func newAccountEnv(t *testing.T) *accountEnv {
	t.Helper()
	authRepo := newAuthFakeRepo()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)

	repo := &accountFakeRepo{authRepo: authRepo}
	blobs := &accountFakeBlobs{objects: map[string][]byte{}}
	accountsvc := account.NewService(repo, blobs, nil, account.WithBaseURL("http://localhost:8080"))

	logs := &bytes.Buffer{}
	srv := New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithAccountService(accountsvc),
		WithLogger(slog.New(slog.NewJSONHandler(logs, nil))),
	)
	return &accountEnv{srv: srv, accountsvc: accountsvc, repo: repo, blobs: blobs, logs: logs}
}

func doJSON(srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDeleteAccountFlow(t *testing.T) {
	env := newAccountEnv(t)
	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Wrong password → 403, account intact, failure audited.
	rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", token, `{"password":"wrong"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(env.logs.String(), observability.ActionAccountDelete) ||
		!strings.Contains(env.logs.String(), "invalid_password") {
		t.Error("failed delete did not audit auth.account.delete failure")
	}
	if strings.Contains(env.logs.String(), "wrong") {
		t.Error("the presented password was logged")
	}

	// Correct password → 204.
	rec = doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", token, `{"password":"supersecret"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(env.logs.String(), `"result":"success"`) &&
		!strings.Contains(env.logs.String(), "result=success") {
		t.Error("successful delete did not audit success")
	}

	// The deleted user's login is refused (credentials no longer resolve).
	rec = postTo(env.srv, "/api/v1/auth/login", `{"email":"ada@example.test","password":"supersecret"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login after delete = %d, want 401", rec.Code)
	}

	// The bearer token no longer resolves an account either.
	rec = getWithAuth(env.srv, "/api/v1/auth/me", token)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
		t.Fatalf("me after delete = %d, want 401/404", rec.Code)
	}
}

func TestDeleteAccountValidationAndAuth(t *testing.T) {
	env := newAccountEnv(t)
	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", token, `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing password = %d, want 422", rec.Code)
	}
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", "", `{"password":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon delete = %d, want 401", rec.Code)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	env := newAccountEnv(t)
	// First registered account is admin.
	adminToken := registerAndToken(t, env.srv, `{"username":"root","email":"root@example.test","password":"supersecret"}`)
	userToken := registerAndToken(t, env.srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	var bobID string
	{
		rec := getWithAuth(env.srv, "/api/v1/auth/me", userToken)
		var u struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		bobID = u.ID
	}
	var adminID string
	{
		rec := getWithAuth(env.srv, "/api/v1/auth/me", adminToken)
		var u struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		adminID = u.ID
	}

	// Non-admin cannot use the admin variant.
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+adminID, userToken, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin = %d, want 403", rec.Code)
	}
	// Self-guard: an admin cannot hard-delete their own account (422).
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+adminID, adminToken, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self delete = %d, want 422", rec.Code)
	}
	// Unknown id → 404.
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+uuid.NewString(), adminToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d, want 404", rec.Code)
	}
	// Happy path → 204, audited, and bob can no longer log in.
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+bobID, adminToken, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", rec.Code)
	}
	if !strings.Contains(env.logs.String(), observability.ActionAdminUserDelete) {
		t.Error("admin delete did not audit admin.user.delete")
	}
	if rec := postTo(env.srv, "/api/v1/auth/login", `{"email":"bob@example.test","password":"supersecret"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bob login after admin delete = %d, want 401", rec.Code)
	}
	// Deleting an already-deleted account → 404.
	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/admin/users/"+bobID, adminToken, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete = %d, want 404", rec.Code)
	}
}

func TestAccountExportFlowAndImportRoundTrip(t *testing.T) {
	env := newAccountEnv(t)
	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// No export yet: status + download 404.
	if rec := getWithAuth(env.srv, "/api/v1/me/export", token); rec.Code != http.StatusNotFound {
		t.Fatalf("status before request = %d, want 404", rec.Code)
	}
	if rec := getWithAuth(env.srv, "/api/v1/me/export/download", token); rec.Code != http.StatusNotFound {
		t.Fatalf("download before request = %d, want 404", rec.Code)
	}

	// Request → 202 pending.
	rec := doJSON(env.srv, http.MethodPost, "/api/v1/me/export", token, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request export = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var status struct {
		State         string `json:"state"`
		DownloadReady bool   `json:"download_ready"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status.State != "pending" || status.DownloadReady {
		t.Fatalf("queued status = %+v", status)
	}

	// A second request while active → 409.
	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/me/export", token, ""); rec.Code != http.StatusConflict {
		t.Fatalf("double request = %d, want 409", rec.Code)
	}
	// Download before the worker ran → 409.
	if rec := getWithAuth(env.srv, "/api/v1/me/export/download", token); rec.Code != http.StatusConflict {
		t.Fatalf("download while pending = %d, want 409", rec.Code)
	}

	// Run the worker.
	if n, err := env.accountsvc.DrainExports(context.Background(), 5); err != nil || n != 1 {
		t.Fatalf("DrainExports = %d, %v; want 1", n, err)
	}

	// Status now done + download_ready with a 7-day expiry.
	rec = getWithAuth(env.srv, "/api/v1/me/export", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status after drain = %d; body=%s", rec.Code, rec.Body.String())
	}
	var done struct {
		State         string     `json:"state"`
		DownloadReady bool       `json:"download_ready"`
		ExpiresAt     *time.Time `json:"expires_at"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &done)
	if done.State != "done" || !done.DownloadReady || done.ExpiresAt == nil {
		t.Fatalf("done status = %+v", done)
	}
	if until := time.Until(*done.ExpiresAt); until < 6*24*time.Hour || until > 8*24*time.Hour {
		t.Fatalf("expiry %v not ~7 days out", done.ExpiresAt)
	}

	// Download: an attachment carrying a valid, secret-free archive.
	rec = getWithAuth(env.srv, "/api/v1/me/export/download", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d; body=%s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get(echo.HeaderContentDisposition); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	raw := rec.Body.Bytes()
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("archive not valid JSON: %v", err)
	}
	var walk func(v any)
	walk = func(v any) {
		switch tv := v.(type) {
		case map[string]any:
			for k, vv := range tv {
				if observability.IsSensitiveKey(k) || k == "password_hash" {
					t.Errorf("archive contains denylisted key %q", k)
				}
				walk(vv)
			}
		case []any:
			for _, vv := range tv {
				walk(vv)
			}
		}
	}
	walk(doc)
	if strings.Contains(string(raw), "supersecret") {
		t.Error("archive contains the raw password")
	}

	// Import the downloaded archive right back: safe subsets re-applied.
	rec = doJSON(env.srv, http.MethodPost, "/api/v1/me/import", token, string(raw))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d; body=%s", rec.Code, rec.Body.String())
	}
	var summary struct {
		NotificationPrefsApplied int            `json:"notification_prefs_applied"`
		SkippedSections          map[string]int `json:"skipped_sections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &summary)
	if summary.NotificationPrefsApplied != 1 {
		t.Fatalf("import summary = %s", rec.Body.String())
	}
	if len(env.repo.upsertedPrefs) != 1 || env.repo.upsertedPrefs[0].Type != "follow" {
		t.Fatalf("prefs not re-applied: %+v", env.repo.upsertedPrefs)
	}
}

func TestImportRejectsInvalidArchive(t *testing.T) {
	env := newAccountEnv(t)
	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/me/import", token, `{"nope":true}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid archive = %d, want 422", rec.Code)
	}
	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/me/import", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon import = %d, want 401", rec.Code)
	}
	// Export endpoints are auth-gated too.
	if rec := doJSON(env.srv, http.MethodPost, "/api/v1/me/export", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon export = %d, want 401", rec.Code)
	}
	if rec := getWithAuth(env.srv, "/api/v1/me/export", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon export status = %d, want 401", rec.Code)
	}
	if rec := getWithAuth(env.srv, "/api/v1/me/export/download", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon download = %d, want 401", rec.Code)
	}
	_ = token
}

// TestTombstonedCommentRendersDeleted proves the §1 comment-tombstone view
// contract: a comment whose author was hard-deleted renders "[deleted]" (never
// its empty stored body), is flagged deleted, and is not marked edited by the
// tombstoning update.
func TestTombstonedCommentRendersDeleted(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	c := sqlcgen.Comment{
		ID:        uuid.New(),
		VideoID:   uuid.New(),
		Body:      "", // tombstoned: body erased in storage
		UserID:    pgtype.UUID{Bytes: uuid.New(), Valid: true},
		CreatedAt: created,
		UpdatedAt: time.Now(), // bumped by the tombstoning UPDATE
		DeletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	v := newCommentView(c, "deleted-1a2b3c4d", "")
	if !v.Deleted || v.Body != "[deleted]" {
		t.Fatalf("tombstone view = deleted=%v body=%q, want deleted=true body=[deleted]", v.Deleted, v.Body)
	}
	if v.Edited {
		t.Error("tombstoning must not mark the comment edited")
	}

	// A live comment is unaffected.
	live := newCommentView(sqlcgen.Comment{ID: uuid.New(), VideoID: uuid.New(), Body: "hi",
		UserID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, CreatedAt: created, UpdatedAt: created}, "ada", "Ada")
	if live.Deleted || live.Body != "hi" {
		t.Fatalf("live view = %+v", live)
	}
}
