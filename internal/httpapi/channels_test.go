package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// channelFakeRepo is an in-memory channel.Repository for handler tests.
type channelFakeRepo struct {
	byHandle   map[string]sqlcgen.Channel
	follows    map[string]bool      // "followerID|channelID"
	followedAt map[string]time.Time // when each follow was created
	followSeq  []string             // follow keys in follow order (for stable "newest first")
}

func newChannelFakeRepo() *channelFakeRepo {
	return &channelFakeRepo{
		byHandle:   map[string]sqlcgen.Channel{},
		follows:    map[string]bool{},
		followedAt: map[string]time.Time{},
	}
}

func (f *channelFakeRepo) FollowChannel(_ context.Context, a sqlcgen.FollowChannelParams) (int64, error) {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	if f.follows[key] {
		return 0, nil // already following
	}
	f.follows[key] = true
	f.followedAt[key] = time.Now()
	f.followSeq = append(f.followSeq, key)
	return 1, nil
}

func (f *channelFakeRepo) UnfollowChannel(_ context.Context, a sqlcgen.UnfollowChannelParams) error {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	delete(f.follows, key)
	delete(f.followedAt, key)
	for i, k := range f.followSeq {
		if k == key {
			f.followSeq = append(f.followSeq[:i], f.followSeq[i+1:]...)
			break
		}
	}
	return nil
}

func (f *channelFakeRepo) ListFollowedChannels(ctx context.Context, a sqlcgen.ListFollowedChannelsParams) ([]sqlcgen.ListFollowedChannelsRow, error) {
	prefix := a.FollowerID.String() + "|"
	// Walk followSeq in reverse so the most recently followed channel is first
	// (mirrors the SQL ORDER BY cf.created_at DESC).
	var out []sqlcgen.ListFollowedChannelsRow
	skipped, taken := int32(0), int32(0)
	for i := len(f.followSeq) - 1; i >= 0; i-- {
		key := f.followSeq[i]
		if !f.follows[key] || !strings.HasPrefix(key, prefix) {
			continue
		}
		chID := strings.TrimPrefix(key, prefix)
		var ch sqlcgen.Channel
		found := false
		for _, c := range f.byHandle {
			if c.ID.String() == chID {
				ch, found = c, true
				break
			}
		}
		if !found {
			continue
		}
		if skipped < a.Offset {
			skipped++
			continue
		}
		if a.Limit > 0 && taken >= a.Limit {
			break
		}
		count, _ := f.CountChannelFollowers(ctx, ch.ID)
		out = append(out, sqlcgen.ListFollowedChannelsRow{
			ID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle,
			DisplayName: ch.DisplayName, Description: ch.Description,
			CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt,
			FollowerCount: count, FollowedAt: f.followedAt[key],
		})
		taken++
	}
	return out, nil
}

func (f *channelFakeRepo) CountChannelFollowers(_ context.Context, channelID uuid.UUID) (int64, error) {
	var n int64
	suffix := "|" + channelID.String()
	for k := range f.follows {
		if strings.HasSuffix(k, suffix) {
			n++
		}
	}
	return n, nil
}

func (f *channelFakeRepo) CountFollowersByOwner(ctx context.Context, ownerID uuid.UUID) ([]sqlcgen.CountFollowersByOwnerRow, error) {
	var rows []sqlcgen.CountFollowersByOwnerRow
	for _, ch := range f.byHandle {
		if ch.OwnerID != ownerID {
			continue
		}
		n, _ := f.CountChannelFollowers(ctx, ch.ID)
		rows = append(rows, sqlcgen.CountFollowersByOwnerRow{ChannelID: ch.ID, Followers: n})
	}
	return rows, nil
}

func (f *channelFakeRepo) CreateChannel(_ context.Context, a sqlcgen.CreateChannelParams) (sqlcgen.Channel, error) {
	key := strings.ToLower(a.Handle)
	if _, ok := f.byHandle[key]; ok {
		return sqlcgen.Channel{}, &pgconn.PgError{Code: "23505"}
	}
	ch := sqlcgen.Channel{
		ID: uuid.New(), OwnerID: a.OwnerID, Handle: a.Handle,
		DisplayName: a.DisplayName, Description: a.Description,
		// Mirror the DB column defaults (migration 0096): a new channel
		// federates over both protocols until the owner opts out.
		ActivitypubEnabled: true, AtprotoEnabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.byHandle[key] = ch
	return ch, nil
}

func (f *channelFakeRepo) GetChannelByHandle(_ context.Context, lowerHandle string) (sqlcgen.Channel, error) {
	ch, ok := f.byHandle[strings.ToLower(lowerHandle)]
	if !ok {
		return sqlcgen.Channel{}, errors.New("not found")
	}
	return ch, nil
}

func (f *channelFakeRepo) ListChannelsByOwner(_ context.Context, ownerID uuid.UUID) ([]sqlcgen.Channel, error) {
	var out []sqlcgen.Channel
	for _, ch := range f.byHandle {
		if ch.OwnerID == ownerID {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (f *channelFakeRepo) UpdateChannel(_ context.Context, a sqlcgen.UpdateChannelParams) (sqlcgen.Channel, error) {
	for k, ch := range f.byHandle {
		if ch.ID == a.ID {
			if a.DisplayName != nil {
				ch.DisplayName = *a.DisplayName
			}
			if a.Description != nil {
				ch.Description = *a.Description
			}
			if a.ActivitypubEnabled != nil {
				ch.ActivitypubEnabled = *a.ActivitypubEnabled
			}
			if a.AtprotoEnabled != nil {
				ch.AtprotoEnabled = *a.AtprotoEnabled
			}
			ch.UpdatedAt = time.Now()
			f.byHandle[k] = ch
			return ch, nil
		}
	}
	return sqlcgen.Channel{}, errors.New("not found")
}

func (f *channelFakeRepo) DeleteChannel(_ context.Context, id uuid.UUID) error {
	for k, ch := range f.byHandle {
		if ch.ID == id {
			delete(f.byHandle, k)
			return nil
		}
	}
	return nil
}

// channelServer wires real auth + channel services over in-memory fakes.
func channelServer(t *testing.T) *Server {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chansvc := channel.NewService(newChannelFakeRepo())
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(chansvc),
	)
}

func TestCreateChannelRequiresAuth(t *testing.T) {
	srv := channelServer(t)
	rec := postTo(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateChannelValidation(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"a","display_name":""}`, token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if len(er.Error.Fields) == 0 {
		t.Errorf("expected field errors, got %+v", er.Error)
	}
}

func TestCreateChannelAndListAndGet(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	// Create
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes","description":"things"}`, token)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created channelView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Handle != "ada_makes" || created.OwnerID == "" {
		t.Errorf("unexpected channel: %+v", created)
	}

	// List own
	list := getWithAuth(srv, "/api/v1/me/channels", token)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.Code)
	}
	var body channelListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Channels) != 1 || body.Channels[0].Handle != "ada_makes" {
		t.Errorf("unexpected list: %+v", body.Channels)
	}

	// Public get by handle (case-insensitive, no auth)
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ADA_MAKES", nil)
	srv.Handler().ServeHTTP(get, req)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body=%s", get.Code, get.Body.String())
	}
	var fetched channelView
	if err := json.Unmarshal(get.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("get returned %q, want %q", fetched.ID, created.ID)
	}
}

func TestCreateChannelDuplicateConflict(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	const body = `{"handle":"ada_makes","display_name":"Ada Makes"}`
	_ = postJSONAuth(srv, "/api/v1/channels", body, token)
	rec := postJSONAuth(srv, "/api/v1/channels", body, token)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestGetChannelNotFound(t *testing.T) {
	srv := channelServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/ghost", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var er ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &er)
	if er.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", er.Error.Code)
	}
}

func sendJSONAuth(srv *Server, method, path, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUpdateChannelOwnerAndNonOwner(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes","description":"old"}`, ownerTok)

	// Owner partial update succeeds.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"new"}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner update = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var ch channelView
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Description != "new" || ch.DisplayName != "Ada Makes" {
		t.Errorf("unexpected channel after update: %+v", ch)
	}

	// Non-owner is forbidden.
	bad := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"hax"}`, otherTok)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("non-owner update = %d, want 403", bad.Code)
	}

	// Unauthenticated is 401.
	if anon := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"description":"x"}`, ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon update = %d, want 401", anon.Code)
	}
}

func TestUpdateChannelValidation(t *testing.T) {
	srv := channelServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, tok)
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch = %d, want 422", rec.Code)
	}
}

// TestUpdateChannelProtocolFlags proves the per-channel protocol flags
// (migration 0096): they default enabled on create, PATCH toggles either
// independently (owner only), and the flags round-trip on the public GET.
func TestUpdateChannelProtocolFlags(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	create := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)
	var created channelView
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	if !created.ActivitypubEnabled || !created.AtprotoEnabled {
		t.Fatalf("new channel should default both protocols enabled: %+v", created)
	}

	// A patch that toggles ONLY activitypub_enabled=false leaves atproto on.
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"activitypub_enabled":false}`, ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("flag patch = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var patched channelView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.ActivitypubEnabled {
		t.Errorf("activitypub_enabled should be false after patch: %+v", patched)
	}
	if !patched.AtprotoEnabled {
		t.Errorf("atproto_enabled should be unchanged (true): %+v", patched)
	}

	// The flag round-trips on the public GET.
	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada_makes", nil))
	var got channelView
	_ = json.Unmarshal(get.Body.Bytes(), &got)
	if got.ActivitypubEnabled || !got.AtprotoEnabled {
		t.Errorf("GET flags = ap:%v at:%v, want ap:false at:true", got.ActivitypubEnabled, got.AtprotoEnabled)
	}

	// A bare {activitypub_enabled:...} body is accepted (not the empty-patch 422).
	if only := sendJSONAuth(srv, http.MethodPatch, "/api/v1/channels/ada_makes", `{"atproto_enabled":false}`, ownerTok); only.Code != http.StatusOK {
		t.Fatalf("atproto-only patch = %d, want 200", only.Code)
	}
}

func TestDeleteChannelOwnerAndNonOwner(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)

	// Non-owner cannot delete.
	if bad := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes", "", otherTok); bad.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete = %d, want 403", bad.Code)
	}

	// Owner deletes; then it is gone (public get 404).
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes", "", ownerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d, want 204", rec.Code)
	}
	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada_makes", nil))
	if get.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", get.Code)
	}
}

func TestFollowFlowAndFollowerCount(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	followerTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)

	getCount := func() int64 {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada_makes", nil))
		var v channelView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return v.FollowerCount
	}

	if c := getCount(); c != 0 {
		t.Fatalf("initial follower_count = %d, want 0", c)
	}

	// Follow requires auth.
	if anon := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada_makes/follow", "", ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon follow = %d, want 401", anon.Code)
	}

	// Follow (idempotent: twice → still 1).
	for i := 0; i < 2; i++ {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada_makes/follow", "", followerTok)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("follow #%d = %d, want 204", i, rec.Code)
		}
	}
	if c := getCount(); c != 1 {
		t.Fatalf("follower_count after follow = %d, want 1", c)
	}

	// Unfollow (idempotent).
	for i := 0; i < 2; i++ {
		rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes/follow", "", followerTok)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unfollow #%d = %d, want 204", i, rec.Code)
		}
	}
	if c := getCount(); c != 0 {
		t.Fatalf("follower_count after unfollow = %d, want 0", c)
	}
}

func TestFollowUnknownChannel404(t *testing.T) {
	srv := channelServer(t)
	tok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ghost/follow", "", tok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestListFollowedChannels exercises GET /api/v1/me/subscriptions: the caller's
// "FOLLOWING" list — most recently followed first, follower_count + followed_at
// populated, paginated, and reflecting unfollows.
func TestListFollowedChannels(t *testing.T) {
	srv := channelServer(t)
	adaTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	northTok := registerAndToken(t, srv, `{"username":"north","email":"north@example.test","password":"supersecret"}`)
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, adaTok)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"north_loop","display_name":"North Loop"}`, northTok)

	list := func(query, token string) followedChannelsResponse {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/subscriptions"+query, "", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /me/subscriptions%s = %d, want 200; body=%s", query, rec.Code, rec.Body.String())
		}
		var out followedChannelsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		return out
	}

	// Auth required.
	if anon := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/subscriptions", "", ""); anon.Code != http.StatusUnauthorized {
		t.Fatalf("anon = %d, want 401", anon.Code)
	}

	// No follows yet → empty (never null).
	if got := list("", bobTok); got.Channels == nil || len(got.Channels) != 0 {
		t.Fatalf("empty follow list = %+v, want zero-length non-nil", got.Channels)
	}

	// Follow ada_makes, then north_loop.
	for _, h := range []string{"ada_makes", "north_loop"} {
		if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/"+h+"/follow", "", bobTok); rec.Code != http.StatusNoContent {
			t.Fatalf("follow %s = %d, want 204", h, rec.Code)
		}
	}

	// Most recently followed first: north_loop before ada_makes.
	got := list("", bobTok)
	if len(got.Channels) != 2 {
		t.Fatalf("got %d channels, want 2: %+v", len(got.Channels), got.Channels)
	}
	if got.Channels[0].Handle != "north_loop" || got.Channels[1].Handle != "ada_makes" {
		t.Fatalf("order = [%s, %s], want [north_loop, ada_makes]", got.Channels[0].Handle, got.Channels[1].Handle)
	}
	for _, ch := range got.Channels {
		if ch.FollowerCount != 1 {
			t.Errorf("%s follower_count = %d, want 1", ch.Handle, ch.FollowerCount)
		}
		if ch.DisplayName == "" || ch.FollowedAt.IsZero() {
			t.Errorf("%s missing display_name/followed_at: %+v", ch.Handle, ch)
		}
	}

	// Pagination.
	if p := list("?limit=1", bobTok); len(p.Channels) != 1 || p.Channels[0].Handle != "north_loop" || p.Limit != 1 {
		t.Fatalf("limit=1 = %+v, want [north_loop]", p.Channels)
	}
	if p := list("?limit=1&offset=1", bobTok); len(p.Channels) != 1 || p.Channels[0].Handle != "ada_makes" || p.Offset != 1 {
		t.Fatalf("limit=1&offset=1 = %+v, want [ada_makes]", p.Channels)
	}

	// Unfollow drops it from the list.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/north_loop/follow", "", bobTok); rec.Code != http.StatusNoContent {
		t.Fatalf("unfollow = %d, want 204", rec.Code)
	}
	after := list("", bobTok)
	if len(after.Channels) != 1 || after.Channels[0].Handle != "ada_makes" {
		t.Fatalf("after unfollow = %+v, want [ada_makes]", after.Channels)
	}
}

func TestUserProfilePrivacyAndOwnedChannels(t *testing.T) {
	srv := channelServer(t)
	token := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, token); rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d; body=%s", rec.Code, rec.Body.String())
	}

	path := "/api/v1/users/ada/profile"
	if rec := getWithAuth(srv, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous private profile = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if rec := getWithAuth(srv, path, token); rec.Code != http.StatusOK {
		t.Fatalf("owner preview = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"profile_public":true}`, token); rec.Code != http.StatusOK {
		t.Fatalf("publish profile = %d; body=%s", rec.Code, rec.Body.String())
	}
	public := getWithAuth(srv, path, "")
	if public.Code != http.StatusOK {
		t.Fatalf("public profile = %d, want 200; body=%s", public.Code, public.Body.String())
	}
	var profile publicUserProfileView
	if err := json.Unmarshal(public.Body.Bytes(), &profile); err != nil {
		t.Fatalf("unmarshal profile: %v", err)
	}
	if profile.Username != "ada" || len(profile.Channels) != 1 || profile.Channels[0].Handle != "ada_makes" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}

// postJSONAuth posts a JSON body with a bearer token.
func postJSONAuth(srv *Server, path, body, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
