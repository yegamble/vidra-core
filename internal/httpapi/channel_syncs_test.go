package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/channelsync"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
	"github.com/vidra/vidra-core/internal/ytdlp"
)

// csFakeRepo is an in-memory channelsync.Repository for handler tests. It reads
// channels from the SHARED channelFakeRepo (so ownership checks resolve against
// channels created through the real channel service) and keeps its own sync +
// dedupe stores.
type csFakeRepo struct {
	channels *channelFakeRepo
	syncs    map[uuid.UUID]sqlcgen.ChannelSync
	seen     map[string]bool
}

func newCSFakeRepo(ch *channelFakeRepo) *csFakeRepo {
	return &csFakeRepo{channels: ch, syncs: map[uuid.UUID]sqlcgen.ChannelSync{}, seen: map[string]bool{}}
}

func (f *csFakeRepo) GetChannelByID(_ context.Context, id uuid.UUID) (sqlcgen.Channel, error) {
	for _, c := range f.channels.byHandle {
		if c.ID == id {
			return c, nil
		}
	}
	return sqlcgen.Channel{}, errors.New("not found")
}

func (f *csFakeRepo) CreateChannelSync(_ context.Context, arg sqlcgen.CreateChannelSyncParams) (sqlcgen.ChannelSync, error) {
	for _, s := range f.syncs {
		if s.ChannelID == arg.ChannelID && s.ExternalChannelUrl == arg.ExternalChannelUrl {
			return sqlcgen.ChannelSync{}, &pgconn.PgError{Code: "23505"}
		}
	}
	s := sqlcgen.ChannelSync{
		ID: uuid.New(), ChannelID: arg.ChannelID, UserID: arg.UserID,
		ExternalChannelUrl: arg.ExternalChannelUrl, State: "waiting_first_run",
		NextRunAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.syncs[s.ID] = s
	return s, nil
}

func (f *csFakeRepo) ListChannelSyncsByUser(_ context.Context, userID uuid.UUID) ([]sqlcgen.ChannelSync, error) {
	var out []sqlcgen.ChannelSync
	for _, s := range f.syncs {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *csFakeRepo) GetChannelSyncByID(_ context.Context, id uuid.UUID) (sqlcgen.ChannelSync, error) {
	s, ok := f.syncs[id]
	if !ok {
		return sqlcgen.ChannelSync{}, errors.New("not found")
	}
	return s, nil
}

func (f *csFakeRepo) CountChannelSyncsByUser(_ context.Context, userID uuid.UUID) (int64, error) {
	var n int64
	for _, s := range f.syncs {
		if s.UserID == userID {
			n++
		}
	}
	return n, nil
}

func (f *csFakeRepo) DeleteChannelSync(_ context.Context, id uuid.UUID) error {
	delete(f.syncs, id)
	return nil
}

func (f *csFakeRepo) TriggerChannelSyncNow(_ context.Context, id uuid.UUID) error {
	if s, ok := f.syncs[id]; ok {
		s.NextRunAt = time.Now()
		f.syncs[id] = s
	}
	return nil
}

func (f *csFakeRepo) ClaimDueChannelSyncs(_ context.Context, _ int32) ([]sqlcgen.ClaimDueChannelSyncsRow, error) {
	return nil, nil
}
func (f *csFakeRepo) FinishChannelSync(_ context.Context, _ sqlcgen.FinishChannelSyncParams) error {
	return nil
}
func (f *csFakeRepo) FailChannelSync(_ context.Context, _ sqlcgen.FailChannelSyncParams) error {
	return nil
}
func (f *csFakeRepo) InsertChannelSyncSeen(_ context.Context, _ sqlcgen.InsertChannelSyncSeenParams) (int64, error) {
	return 1, nil
}

// noopDrafter / noopEnqueuer / noopLister satisfy the worker seams; the handler
// tests never drive DrainDue, so they are never called.
type noopDrafter struct{}

func (noopDrafter) CreateDraft(context.Context, uuid.UUID, video.CreateInput) (sqlcgen.Video, error) {
	return sqlcgen.Video{}, nil
}

type noopEnqueuer struct{}

func (noopEnqueuer) Enqueue(context.Context, uuid.UUID, string, string) (sqlcgen.ImportJob, error) {
	return sqlcgen.ImportJob{}, nil
}

type noopLister struct{}

func (noopLister) Playlist(context.Context, string, int) ([]ytdlp.PlaylistEntry, error) {
	return nil, nil
}

// channelSyncServer wires real auth + channel + channelsync services over shared
// in-memory fakes, with the feature toggled by `enabled`.
func channelSyncServer(t *testing.T, enabled bool) *Server {
	t.Helper()
	srv, _ := channelSyncServerWithRepo(t, enabled)
	return srv
}

// channelSyncServerWithRepo is channelSyncServer but also returns the underlying
// sync repo (so a test can stamp last_sync_at to exercise the cooldown) and lets
// callers append extra channelsync options.
func channelSyncServerWithRepo(t *testing.T, enabled bool, opts ...channelsync.Option) (*Server, *csFakeRepo) {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chRepo := newChannelFakeRepo()
	chansvc := channel.NewService(chRepo)
	csRepo := newCSFakeRepo(chRepo)
	base := []channelsync.Option{
		channelsync.WithEnabled(enabled),
		channelsync.WithLister(noopLister{}),
		channelsync.WithMaxPerUser(5),
		channelsync.WithBatch(15),
		channelsync.WithInterval(time.Hour),
	}
	cssvc := channelsync.NewService(csRepo, noopDrafter{}, noopEnqueuer{}, append(base, opts...)...)
	// The handler's runtime gate (channel_sync_enabled, W8) falls back to
	// cfg.ChannelSyncEnabled when no settings service is wired — keep it in
	// step with the service flag so `enabled` still means what it says.
	cfg := testConfig()
	cfg.ChannelSyncEnabled = enabled
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(chansvc),
		WithChannelSyncService(cssvc),
	)
	return srv, csRepo
}

// channelIDFor creates a channel and returns its uuid (the create response
// carries the id).
func channelIDFor(t *testing.T, srv *Server, tok, handle string) string {
	t.Helper()
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"`+handle+`","display_name":"`+handle+`"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v channelView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return v.ID
}

func TestChannelSyncRequiresAuth(t *testing.T) {
	srv := channelSyncServer(t, true)
	if rec := postTo(srv, "/api/v1/channel-syncs", `{"channel_id":"x","external_channel_url":"https://y"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon create = %d, want 401", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/channel-syncs", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon list = %d, want 401", rec.Code)
	}
}

// TestChannelSyncDisabled403: with the runtime admin setting off (W8 gate
// idiom), create is refused 403 with the stable feature_disabled code — the
// same shape as every other feature toggle.
func TestChannelSyncDisabled403(t *testing.T) {
	srv := channelSyncServer(t, false)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, tok, "ada")

	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, tok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled create = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "feature_disabled" {
		t.Errorf("403 code = %q, want feature_disabled", body.Error.Code)
	}
}

// TestChannelSyncBootDisabled503: the runtime setting is ON but the deployment
// lacks the boot capability (service constructed disabled — no yt-dlp
// resolver): the service's 503 is preserved for that distinct state.
func TestChannelSyncBootDisabled503(t *testing.T) {
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chRepo := newChannelFakeRepo()
	csRepo := newCSFakeRepo(chRepo)
	cssvc := channelsync.NewService(csRepo, noopDrafter{}, noopEnqueuer{},
		channelsync.WithEnabled(false)) // boot capability off
	cfg := testConfig()
	cfg.ChannelSyncEnabled = true // runtime setting on
	srv := New(cfg, nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(channel.NewService(chRepo)),
		WithChannelSyncService(cssvc),
	)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, tok, "ada")

	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("boot-disabled create = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "service_unavailable" {
		t.Errorf("503 code = %q, want service_unavailable", body.Error.Code)
	}
}

func TestChannelSyncCreateValidation(t *testing.T) {
	srv := channelSyncServer(t, true)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, tok, "ada")

	// Missing fields → 422.
	if rec := postJSONAuth(srv, "/api/v1/channel-syncs", `{}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty body = %d, want 422", rec.Code)
	}
	// Non-public URL → 422.
	if rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"http://127.0.0.1/@x"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("private url = %d, want 422", rec.Code)
	}
	// Unknown channel → 404.
	if rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+uuid.NewString()+`","external_channel_url":"https://youtube.com/@x"}`, tok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", rec.Code)
	}
}

func TestChannelSyncNonOwner404(t *testing.T) {
	srv := channelSyncServer(t, true)
	adaTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, adaTok, "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, bobTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner create = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestChannelSyncLifecycle(t *testing.T) {
	srv := channelSyncServer(t, true)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, tok, "ada")

	// Create → 201 with a waiting_first_run sync and no last_sync_at yet.
	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created channelSyncResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ChannelSync.State != "waiting_first_run" {
		t.Errorf("state = %q, want waiting_first_run", created.ChannelSync.State)
	}
	if created.ChannelSync.LastSyncAt != nil {
		t.Errorf("last_sync_at = %v, want omitted before first run", created.ChannelSync.LastSyncAt)
	}
	syncID := created.ChannelSync.ID

	// Duplicate → 409.
	if dup := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, tok); dup.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", dup.Code)
	}

	// List → 200 with the one sync.
	list := sendJSONAuth(srv, http.MethodGet, "/api/v1/channel-syncs", "", tok)
	if list.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", list.Code)
	}
	var listed channelSyncListResponse
	_ = json.Unmarshal(list.Body.Bytes(), &listed)
	if len(listed.ChannelSyncs) != 1 || listed.ChannelSyncs[0].ID != syncID {
		t.Fatalf("list = %+v, want the one created sync", listed.ChannelSyncs)
	}

	// sync-now → 202.
	if now := sendJSONAuth(srv, http.MethodPost, "/api/v1/channel-syncs/"+syncID+"/sync-now", "", tok); now.Code != http.StatusAccepted {
		t.Errorf("sync-now = %d, want 202", now.Code)
	}

	// Delete → 204, then it is gone.
	if del := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channel-syncs/"+syncID, "", tok); del.Code != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", del.Code)
	}
	after := sendJSONAuth(srv, http.MethodGet, "/api/v1/channel-syncs", "", tok)
	var afterList channelSyncListResponse
	_ = json.Unmarshal(after.Body.Bytes(), &afterList)
	if len(afterList.ChannelSyncs) != 0 {
		t.Errorf("after delete list = %+v, want empty", afterList.ChannelSyncs)
	}
}

// TestChannelSyncNowCooldown429 proves the server-side cooldown on
// POST .../sync-now: a sync whose last run completed inside the cooldown window
// is rejected 429 (stable code rate_limited) with a Retry-After header, while a
// sync past the window is accepted 202.
func TestChannelSyncNowCooldown429(t *testing.T) {
	srv, repo := channelSyncServerWithRepo(t, true, channelsync.WithCooldown(time.Hour))
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, tok, "ada")

	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created channelSyncResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	syncID := uuid.MustParse(created.ChannelSync.ID)

	// Stamp a very recent completed run so the cooldown is active.
	s := repo.syncs[syncID]
	s.LastSyncAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	repo.syncs[syncID] = s

	now := sendJSONAuth(srv, http.MethodPost, "/api/v1/channel-syncs/"+created.ChannelSync.ID+"/sync-now", "", tok)
	if now.Code != http.StatusTooManyRequests {
		t.Fatalf("sync-now inside cooldown = %d, want 429; body=%s", now.Code, now.Body.String())
	}
	if ra := now.Header().Get("Retry-After"); ra == "" {
		t.Errorf("429 missing Retry-After header")
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(now.Body.Bytes(), &body)
	if body.Error.Code != "rate_limited" {
		t.Errorf("429 code = %q, want rate_limited", body.Error.Code)
	}

	// Move the last run outside the window → the next trigger is accepted.
	s = repo.syncs[syncID]
	s.LastSyncAt = pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true}
	repo.syncs[syncID] = s
	if after := sendJSONAuth(srv, http.MethodPost, "/api/v1/channel-syncs/"+created.ChannelSync.ID+"/sync-now", "", tok); after.Code != http.StatusAccepted {
		t.Fatalf("sync-now past cooldown = %d, want 202", after.Code)
	}
}

func TestChannelSyncDeleteNonOwner404(t *testing.T) {
	srv := channelSyncServer(t, true)
	adaTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	chID := channelIDFor(t, srv, adaTok, "ada")
	rec := postJSONAuth(srv, "/api/v1/channel-syncs",
		`{"channel_id":"`+chID+`","external_channel_url":"https://youtube.com/@ada"}`, adaTok)
	var created channelSyncResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	if del := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channel-syncs/"+created.ChannelSync.ID, "", bobTok); del.Code != http.StatusNotFound {
		t.Errorf("non-owner delete = %d, want 404", del.Code)
	}
	if now := sendJSONAuth(srv, http.MethodPost, "/api/v1/channel-syncs/"+created.ChannelSync.ID+"/sync-now", "", bobTok); now.Code != http.StatusNotFound {
		t.Errorf("non-owner sync-now = %d, want 404", now.Code)
	}
}
