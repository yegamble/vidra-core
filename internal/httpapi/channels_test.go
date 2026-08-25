package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// channelFakeRepo is an in-memory channel.Repository for handler tests.
type channelFakeRepo struct {
	byHandle   map[string]sqlcgen.Channel
	follows    map[string]bool                  // "followerID|channelID"
	bells      map[string]string                // follow key -> notification_setting (0101)
	followedAt map[string]time.Time             // when each follow was created
	followSeq  []string                         // follow keys in follow order (for stable "newest first")
	members    map[string]sqlcgen.ChannelMember // "channelID|userID" (migration 0097)
	// users, when wired, resolves usernames for member invites against the auth
	// fake (nil-safe: unset → GetUserByUsername always 404s).
	users *authFakeRepo
}

func newChannelFakeRepo() *channelFakeRepo {
	return &channelFakeRepo{
		byHandle:   map[string]sqlcgen.Channel{},
		follows:    map[string]bool{},
		bells:      map[string]string{},
		followedAt: map[string]time.Time{},
		members:    map[string]sqlcgen.ChannelMember{},
	}
}

func channelMemberKey(channelID, userID uuid.UUID) string {
	return channelID.String() + "|" + userID.String()
}

func (f *channelFakeRepo) GetUserByUsername(_ context.Context, username string) (sqlcgen.User, error) {
	if f.users != nil {
		for _, u := range f.users.users {
			if strings.EqualFold(u.Username, strings.TrimSpace(username)) && u.IsActive {
				return u, nil
			}
		}
	}
	return sqlcgen.User{}, pgx.ErrNoRows
}

func (f *channelFakeRepo) AddChannelMember(_ context.Context, a sqlcgen.AddChannelMemberParams) (sqlcgen.ChannelMember, error) {
	m := sqlcgen.ChannelMember{
		ChannelID: a.ChannelID, UserID: a.UserID, Role: a.Role,
		InvitedBy: a.InvitedBy, CreatedAt: time.Now(),
	}
	f.members[channelMemberKey(a.ChannelID, a.UserID)] = m
	return m, nil
}

func (f *channelFakeRepo) GetChannelMember(_ context.Context, a sqlcgen.GetChannelMemberParams) (sqlcgen.ChannelMember, error) {
	if m, ok := f.members[channelMemberKey(a.ChannelID, a.UserID)]; ok {
		return m, nil
	}
	return sqlcgen.ChannelMember{}, pgx.ErrNoRows
}

func (f *channelFakeRepo) DeleteChannelMember(_ context.Context, a sqlcgen.DeleteChannelMemberParams) (int64, error) {
	key := channelMemberKey(a.ChannelID, a.UserID)
	if _, ok := f.members[key]; ok {
		delete(f.members, key)
		return 1, nil
	}
	return 0, nil
}

func (f *channelFakeRepo) ListChannelMembers(_ context.Context, a sqlcgen.ListChannelMembersParams) ([]sqlcgen.ListChannelMembersRow, error) {
	channelID := a.ChannelID
	var out []sqlcgen.ListChannelMembersRow
	for _, m := range f.members {
		if m.ChannelID != channelID {
			continue
		}
		row := sqlcgen.ListChannelMembersRow{UserID: m.UserID, Role: m.Role, InvitedBy: m.InvitedBy, CreatedAt: m.CreatedAt}
		if f.users != nil {
			for _, u := range f.users.users {
				if u.ID == m.UserID {
					row.Username, row.DisplayName = u.Username, u.DisplayName
					break
				}
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *channelFakeRepo) IsChannelManager(_ context.Context, a sqlcgen.IsChannelManagerParams) (bool, error) {
	for _, ch := range f.byHandle {
		if ch.ID == a.ChannelID {
			if ch.OwnerID == a.UserID {
				return true, nil
			}
			_, ok := f.members[channelMemberKey(a.ChannelID, a.UserID)]
			return ok, nil
		}
	}
	return false, nil
}

func (f *channelFakeRepo) ListChannelsForMember(_ context.Context, userID uuid.UUID) ([]sqlcgen.ListChannelsForMemberRow, error) {
	var out []sqlcgen.ListChannelsForMemberRow
	for _, m := range f.members {
		if m.UserID != userID {
			continue
		}
		for _, ch := range f.byHandle {
			if ch.ID != m.ChannelID {
				continue
			}
			out = append(out, sqlcgen.ListChannelsForMemberRow{
				ID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle,
				DisplayName: ch.DisplayName, Description: ch.Description,
				ActivitypubEnabled: ch.ActivitypubEnabled, AtprotoEnabled: ch.AtprotoEnabled,
				CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt, Role: m.Role,
			})
		}
	}
	return out, nil
}

func (f *channelFakeRepo) FollowChannel(_ context.Context, a sqlcgen.FollowChannelParams) (int64, error) {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	if f.follows[key] {
		return 0, nil // already following
	}
	f.follows[key] = true
	f.bells[key] = channel.NotifyAll // the column default a new follow gets
	f.followedAt[key] = time.Now()
	f.followSeq = append(f.followSeq, key)
	return 1, nil
}

func (f *channelFakeRepo) GetFollowNotificationSetting(_ context.Context, a sqlcgen.GetFollowNotificationSettingParams) (string, error) {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	if !f.follows[key] {
		return "", pgx.ErrNoRows
	}
	return f.bells[key], nil
}

func (f *channelFakeRepo) SetFollowNotificationSetting(_ context.Context, a sqlcgen.SetFollowNotificationSettingParams) (int64, error) {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	if !f.follows[key] {
		return 0, nil
	}
	f.bells[key] = a.NotificationSetting
	return 1, nil
}

func (f *channelFakeRepo) UnfollowChannel(_ context.Context, a sqlcgen.UnfollowChannelParams) error {
	key := a.FollowerID.String() + "|" + a.ChannelID.String()
	delete(f.follows, key)
	delete(f.bells, key)
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
			NotificationSetting: f.bells[key],
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
	authRepo := newAuthFakeRepo()
	authsvc := auth.NewService(authRepo, issuer, 720*time.Hour)
	chRepo := newChannelFakeRepo()
	chRepo.users = authRepo // resolve member-invite usernames against the auth fake
	chansvc := channel.NewService(chRepo)
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

// TestFollowNotificationBellEndpoint exercises PUT
// /api/v1/channels/{handle}/follow/notifications and the read paths that render
// the bell: the single channel GET (behind optionalAuth) and the FOLLOWING list.
func TestFollowNotificationBellEndpoint(t *testing.T) {
	srv := channelServer(t)
	ownerTok := registerAndToken(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	followerTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	_ = postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada_makes","display_name":"Ada Makes"}`, ownerTok)

	channelAs := func(token string) channelView {
		t.Helper()
		rec := getWithAuth(srv, "/api/v1/channels/ada_makes", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("get channel = %d, want 200", rec.Code)
		}
		var v channelView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode channel: %v", err)
		}
		return v
	}

	// Anonymous callers get the public projection only — no relationship fields.
	anon := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/api/v1/channels/ada_makes", nil))
	var anonBody map[string]any
	if err := json.Unmarshal(anon.Body.Bytes(), &anonBody); err != nil {
		t.Fatalf("decode anonymous channel: %v", err)
	}
	if _, present := anonBody["is_following"]; present {
		t.Errorf("anonymous channel view carries is_following: %v", anonBody["is_following"])
	}
	if _, present := anonBody["notification_setting"]; present {
		t.Errorf("anonymous channel view carries notification_setting: %v", anonBody["notification_setting"])
	}

	// Signed in but not following: is_following=false, no bell.
	if v := channelAs(followerTok); v.IsFollowing == nil || *v.IsFollowing || v.NotificationSetting != "" {
		t.Fatalf("before follow: is_following=%v setting=%q, want false and no bell", v.IsFollowing, v.NotificationSetting)
	}
	// Setting a bell without following is 404 (as is an unknown handle).
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ada_makes/follow/notifications",
		`{"notification_setting":"none"}`, followerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("bell before following = %d, want 404", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ghost/follow/notifications",
		`{"notification_setting":"none"}`, followerTok); rec.Code != http.StatusNotFound {
		t.Fatalf("bell on unknown channel = %d, want 404", rec.Code)
	}
	// Auth is required.
	if rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ada_makes/follow/notifications",
		`{"notification_setting":"none"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon bell = %d, want 401", rec.Code)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/ada_makes/follow", "", followerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("follow = %d, want 204", rec.Code)
	}
	// A new follow arrives with the bell on.
	if v := channelAs(followerTok); v.IsFollowing == nil || !*v.IsFollowing || v.NotificationSetting != channel.NotifyAll {
		t.Fatalf("after follow: is_following=%v setting=%q, want true/%q", v.IsFollowing, v.NotificationSetting, channel.NotifyAll)
	}

	// Mute it.
	rec := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ada_makes/follow/notifications",
		`{"notification_setting":"none"}`, followerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("mute bell = %d, want 200", rec.Code)
	}
	var echoed followNotificationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
		t.Fatalf("decode bell response: %v", err)
	}
	if echoed.NotificationSetting != channel.NotifyNone {
		t.Errorf("bell response = %q, want %q", echoed.NotificationSetting, channel.NotifyNone)
	}
	if v := channelAs(followerTok); *v.IsFollowing != true || v.NotificationSetting != channel.NotifyNone {
		t.Fatalf("after mute: is_following=%v setting=%q, want true/%q", *v.IsFollowing, v.NotificationSetting, channel.NotifyNone)
	}
	// Muting the bell is NOT unfollowing.
	if v := channelAs(followerTok); v.FollowerCount != 1 {
		t.Errorf("follower_count after muting = %d, want 1", v.FollowerCount)
	}

	// The FOLLOWING list carries the bell per row.
	list := getWithAuth(srv, "/api/v1/me/subscriptions", followerTok)
	if list.Code != http.StatusOK {
		t.Fatalf("subscriptions = %d, want 200", list.Code)
	}
	var followed followedChannelsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &followed); err != nil {
		t.Fatalf("decode subscriptions: %v", err)
	}
	if len(followed.Channels) != 1 || followed.Channels[0].NotificationSetting != channel.NotifyNone {
		t.Fatalf("subscriptions bell = %+v, want one row with %q", followed.Channels, channel.NotifyNone)
	}

	// An unsupported mode is refused and leaves the stored bell alone. YouTube's
	// "personalized" is the realistic thing a client would send.
	if bad := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ada_makes/follow/notifications",
		`{"notification_setting":"personalized"}`, followerTok); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported mode = %d, want 422", bad.Code)
	}
	if v := channelAs(followerTok); v.NotificationSetting != channel.NotifyNone {
		t.Errorf("bell after a rejected mode = %q, want %q (unchanged)", v.NotificationSetting, channel.NotifyNone)
	}

	// Back on, then unfollow drops the relationship and the bell together.
	if on := sendJSONAuth(srv, http.MethodPut, "/api/v1/channels/ada_makes/follow/notifications",
		`{"notification_setting":"all"}`, followerTok); on.Code != http.StatusOK {
		t.Fatalf("unmute = %d, want 200", on.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada_makes/follow", "", followerTok); rec.Code != http.StatusNoContent {
		t.Fatalf("unfollow = %d, want 204", rec.Code)
	}
	if v := channelAs(followerTok); *v.IsFollowing || v.NotificationSetting != "" {
		t.Fatalf("after unfollow: is_following=%v setting=%q, want false and no bell", *v.IsFollowing, v.NotificationSetting)
	}
}

// Counts and the managed-channel UNION mirror their List so the fake pair can
// never disagree.
func (f *channelFakeRepo) CountChannelMembers(ctx context.Context, channelID uuid.UUID) (int64, error) {
	rows, err := f.ListChannelMembers(ctx, sqlcgen.ListChannelMembersParams{ChannelID: channelID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}

func (f *channelFakeRepo) CountFollowedChannels(ctx context.Context, followerID uuid.UUID) (int64, error) {
	rows, err := f.ListFollowedChannels(ctx, sqlcgen.ListFollowedChannelsParams{FollowerID: followerID, Limit: 1 << 30})
	return int64(len(rows)), err
}

func (f *channelFakeRepo) ListManagedChannels(ctx context.Context, a sqlcgen.ListManagedChannelsParams) ([]sqlcgen.ListManagedChannelsRow, error) {
	owned, err := f.ListChannelsByOwner(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]sqlcgen.ListManagedChannelsRow, 0, len(owned))
	add := func(ch sqlcgen.Channel, role string) {
		n, _ := f.CountChannelFollowers(ctx, ch.ID)
		out = append(out, sqlcgen.ListManagedChannelsRow{
			ID: ch.ID, OwnerID: ch.OwnerID, Handle: ch.Handle, DisplayName: ch.DisplayName,
			Description: ch.Description, CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt,
			ActivitypubEnabled: ch.ActivitypubEnabled, AtprotoEnabled: ch.AtprotoEnabled,
			Role: role, FollowerCount: n,
		})
	}
	for _, ch := range owned {
		add(ch, "owner")
	}
	shared, err := f.ListChannelsForMember(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	for _, r := range shared {
		add(sqlcgen.Channel{
			ID: r.ID, OwnerID: r.OwnerID, Handle: r.Handle, DisplayName: r.DisplayName,
			Description: r.Description, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			ActivitypubEnabled: r.ActivitypubEnabled, AtprotoEnabled: r.AtprotoEnabled,
		}, r.Role)
	}
	// The fake's owner list walks a map, so impose the query's deterministic
	// ORDER BY (owned first, then oldest first, id as tiebreak) before paging.
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Role == "owner") != (out[j].Role == "owner") {
			return out[i].Role == "owner"
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	lo := min(int(a.ResultOffset), len(out))
	out = out[lo:]
	if a.ResultLimit > 0 && int(a.ResultLimit) < len(out) {
		out = out[:a.ResultLimit]
	}
	return out, nil
}

func (f *channelFakeRepo) CountManagedChannels(ctx context.Context, userID uuid.UUID) (int64, error) {
	rows, err := f.ListManagedChannels(ctx, sqlcgen.ListManagedChannelsParams{UserID: userID, ResultLimit: 1 << 30})
	return int64(len(rows)), err
}
