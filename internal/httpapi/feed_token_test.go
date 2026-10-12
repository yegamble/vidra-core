package httpapi

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/feedtoken"
	"github.com/vidra/vidra-core/internal/ratelimit"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// fakeFeedTokenRepo mirrors the SQL: one row per user, rotate overwrites.
type fakeFeedTokenRepo struct {
	hashByUser map[uuid.UUID]string
}

func (f *fakeFeedTokenRepo) UpsertUserFeedToken(_ context.Context, a sqlcgen.UpsertUserFeedTokenParams) error {
	f.hashByUser[a.UserID] = a.TokenHash
	return nil
}
func (f *fakeFeedTokenRepo) GetActiveUserByFeedTokenHash(_ context.Context, h string) (uuid.UUID, error) {
	for u, x := range f.hashByUser {
		if x == h {
			return u, nil
		}
	}
	return uuid.Nil, pgx.ErrNoRows
}
func (f *fakeFeedTokenRepo) GetUserFeedTokenCreatedAt(_ context.Context, u uuid.UUID) (time.Time, error) {
	if _, ok := f.hashByUser[u]; !ok {
		return time.Time{}, pgx.ErrNoRows
	}
	return time.Now(), nil
}
func (f *fakeFeedTokenRepo) DeleteUserFeedToken(_ context.Context, u uuid.UUID) error {
	delete(f.hashByUser, u)
	return nil
}

func TestPrivateSubscriptionsFeed(t *testing.T) {
	cfg := distributionCfg()
	frepo := &fakeFeedTokenRepo{hashByUser: map[uuid.UUID]string{}}
	srv, _, _, _, repo := videoServerFullWith(t, cfg, []Option{WithFeedTokenService(feedtoken.NewService(frepo))})
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	_ = bobTok
	bob, _ := repo.channels.GetChannelByHandle(context.Background(), "bob")
	seedVideo(repo, bob, "Bob Public", "d", "public", "published", 5)
	seedVideo(repo, bob, "Bob Private", "d", "private", "published", 6)
	repo.remoteSubs = append(repo.remoteSubs, sqlcgen.ListSubscriptionVideosRow{
		ID: uuid.New(), Remote: true, Title: "Remote premiere", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Domain: "tube.remote.example", WatchUrl: "https://tube.remote.example/w/abc",
	})
	if f := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/bob/follow", "", adaTok); f.Code != http.StatusNoContent {
		t.Fatalf("follow = %d", f.Code)
	}

	// Requires auth.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/feed-token", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous rotate = %d, want 401", rec.Code)
	}
	var st feedTokenStatus
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/feed-token", "", adaTok)
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if rec.Code != 200 || st.Configured {
		t.Fatalf("initial status = %d %+v, want unconfigured", rec.Code, st)
	}

	issue := func() feedTokenIssued {
		rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/feed-token", "", adaTok)
		if rec.Code != http.StatusCreated {
			t.Fatalf("rotate = %d %s", rec.Code, rec.Body.String())
		}
		var out feedTokenIssued
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := issue()
	if !strings.HasPrefix(first.URL, "https://videos.example/feeds/subscriptions.xml?token=") || first.Token == "" {
		t.Fatalf("issued = %+v", first)
	}
	for _, h := range frepo.hashByUser {
		if h == first.Token {
			t.Fatal("raw token stored unhashed")
		}
	}

	rec = get(t, srv, "/feeds/subscriptions.xml?token="+first.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed = %d %s", rec.Code, rec.Body.String())
	}
	var doc rssDocument
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if len(doc.Channel.Items) != 1 || doc.Channel.Items[0].Title != "Bob Public" {
		t.Errorf("items = %+v, want only Bob Public (no private, no remote)", doc.Channel.Items)
	}
	if rp := rec.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", rp)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if strings.Contains(rec.Body.String(), first.Token) {
		t.Error("feed body echoes the secret")
	}

	// Rotation kills the old token; a bad/missing token is the same 404.
	second := issue()
	for _, p := range []string{"?token=" + first.Token, "?token=nope", "", "?token="} {
		if rec := get(t, srv, "/feeds/subscriptions.xml"+p); rec.Code != http.StatusNotFound {
			t.Errorf("GET feed%s = %d, want 404", p, rec.Code)
		}
	}
	if rec := get(t, srv, "/feeds/subscriptions.xml?token="+second.Token); rec.Code != http.StatusOK {
		t.Errorf("rotated token = %d, want 200", rec.Code)
	}
	// The token authorises nothing else.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/subscriptions?token="+second.Token, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("token as API credential = %d, want 401", rec.Code)
	}

	// Revoke.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/feed-token", "", adaTok); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rec := get(t, srv, "/feeds/subscriptions.xml?token="+second.Token); rec.Code != http.StatusNotFound {
		t.Errorf("revoked token = %d, want 404", rec.Code)
	}
}

// The feed applies the TOKEN OWNER's effective sensitive policy (override, else
// instance), and a reader cannot blur/warn, so only "display" keeps the video.
func TestSubscriptionsFeedHonoursOwnersSensitivePolicy(t *testing.T) {
	frepo := &fakeFeedTokenRepo{hashByUser: map[uuid.UUID]string{}}
	srv, _, _, _, repo := videoServerFullWith(t, distributionCfg(), []Option{WithFeedTokenService(feedtoken.NewService(frepo))})
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	bob, _ := repo.channels.GetChannelByHandle(context.Background(), "bob")
	id := seedVideo(repo, bob, "Spicy", "d", "public", "published", 5)
	row := repo.videos[id]
	row.IsSensitive = true
	repo.videos[id] = row
	if f := sendJSONAuth(srv, http.MethodPost, "/api/v1/channels/bob/follow", "", adaTok); f.Code != http.StatusNoContent {
		t.Fatalf("follow = %d", f.Code)
	}
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/me/feed-token", "", adaTok)
	var issued feedTokenIssued
	_ = json.Unmarshal(rec.Body.Bytes(), &issued)
	shows := func() bool {
		r := get(t, srv, "/feeds/subscriptions.xml?token="+issued.Token)
		return strings.Contains(r.Body.String(), "Spicy")
	}
	setUser := func(p string) {
		if r := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", `{"sensitive_content_policy":"`+p+`"}`, adaTok); r.Code != http.StatusOK {
			t.Fatalf("set policy = %d", r.Code)
		}
	}
	for _, instance := range []string{"hide", "warn", "blur"} {
		applySensitivePolicy(t, srv, instance)
		if shows() {
			t.Errorf("instance %q: flagged video must not reach a feed reader", instance)
		}
	}
	applySensitivePolicy(t, srv, "display")
	if !shows() {
		t.Error("instance display + no override: flagged video should show")
	}
	setUser("hide")
	if shows() {
		t.Error("owner override hide must beat instance display")
	}
	applySensitivePolicy(t, srv, "hide")
	setUser("display")
	if !shows() {
		t.Error("owner override display must beat instance hide")
	}
}

// Remote cards are excluded in SQL, so they cannot consume the LIMIT window.
func TestSubscriptionsFeedLocalOnlyInQuery(t *testing.T) {
	frepo := &fakeFeedTokenRepo{hashByUser: map[uuid.UUID]string{}}
	srv, _, _, _, repo := videoServerFullWith(t, distributionCfg(), []Option{WithFeedTokenService(feedtoken.NewService(frepo))})
	adaTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	repo.remoteSubs = append(repo.remoteSubs, sqlcgen.ListSubscriptionVideosRow{ID: uuid.New(), Remote: true, Title: "R", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	items, err := srv.videosvc.ListLocalSubscriptions(context.Background(), uuid.New(), 50)
	if err != nil || len(items) != 0 {
		t.Fatalf("local-only query returned %d items, err=%v", len(items), err)
	}
	_ = adaTok
}

func TestSubscriptionsFeedIsRateLimited(t *testing.T) {
	srv := New(distributionCfg(), nil, nil,
		WithRateLimiter(ratelimit.NewLimiter(&fakeCounter{}, 1, time.Minute)),
		WithVideoService(video.NewService(nil, nil)),
		WithChannelService(channel.NewService(nil)),
		WithFeedTokenService(feedtoken.NewService(&fakeFeedTokenRepo{hashByUser: map[uuid.UUID]string{}})),
	)
	var codes []int
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/subscriptions.xml?token=guess", nil))
		codes = append(codes, rec.Code)
	}
	if codes[0] != http.StatusNotFound || codes[1] != http.StatusTooManyRequests {
		t.Errorf("codes = %v, want [404 429]", codes)
	}
}

// If the owner's row cannot be read, their stricter override is unknown: hide.
func TestSensitivePolicyForUserFailsClosed(t *testing.T) {
	srv, _, _, _, _ := videoServerFullWith(t, distributionCfg(), nil)
	applySensitivePolicy(t, srv, "display")
	if got := srv.sensitivePolicyForUser(context.Background(), uuid.New()); got != "hide" {
		t.Errorf("policy for unreadable user = %q, want hide", got)
	}
}
