package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type stubVideoLookup struct {
	channelID uuid.UUID
	tier      string
	found     bool
	err       error
}

func (s stubVideoLookup) LookupVideoTier(_ context.Context, _ string) (uuid.UUID, string, bool, error) {
	return s.channelID, s.tier, s.found, s.err
}

type stubMembershipLookup struct {
	tier string
	err  error
}

func (s stubMembershipLookup) GetActiveTier(_ context.Context, _, _ uuid.UUID) (string, error) {
	return s.tier, s.err
}

func newRequest(path string, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID != "" {
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
	}
	return req
}

func runMiddleware(t *testing.T, cfg InnerCircleAccessConfig, req *http.Request) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	RequireInnerCircleAccess(cfg)(next).ServeHTTP(rr, req)
	return rr, called
}

func TestRequireInnerCircleAccess_PublicVideo_PassesThrough(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "", found: true},
		Memberships: stubMembershipLookup{},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", ""))
	if !called {
		t.Fatalf("public video should pass through to handler")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestRequireInnerCircleAccess_NonMember_403_OnMasterM3U8(t *testing.T) {
	channelID := uuid.New()
	userID := uuid.New().String()
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: channelID, tier: "vip", found: true},
		Memberships: stubMembershipLookup{tier: ""},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", userID))
	if called {
		t.Fatalf("non-member must be blocked, but handler ran")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	var body struct {
		Error map[string]string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error["code"] != "inner_circle_tier_required" {
		t.Fatalf("code = %q, want inner_circle_tier_required", body.Error["code"])
	}
	if body.Error["tier_required"] != "vip" {
		t.Fatalf("tier_required = %q, want vip", body.Error["tier_required"])
	}
	if body.Error["channel_id"] != channelID.String() {
		t.Fatalf("channel_id = %q, want %s", body.Error["channel_id"], channelID)
	}
}

func TestRequireInnerCircleAccess_NonMember_403_OnTSegment(t *testing.T) {
	channelID := uuid.New()
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: channelID, tier: "supporter", found: true},
		Memberships: stubMembershipLookup{tier: ""},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/720p/seg-12.ts", uuid.New().String()))
	if called {
		t.Fatalf("non-member must be blocked from segments")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRequireInnerCircleAccess_AnonymousNonMember_403(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "elite", found: true},
		Memberships: stubMembershipLookup{tier: ""},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", ""))
	if called {
		t.Fatalf("anonymous viewer must be blocked from gated videos")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRequireInnerCircleAccess_Member_200_OnMasterM3U8(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "vip", found: true},
		Memberships: stubMembershipLookup{tier: "vip"},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", uuid.New().String()))
	if !called {
		t.Fatalf("member must pass through, status=%d", rr.Code)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestRequireInnerCircleAccess_HigherTierMember_Passes(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "supporter", found: true},
		Memberships: stubMembershipLookup{tier: "elite"},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", uuid.New().String()))
	if !called {
		t.Fatalf("elite member must pass supporter-gated video")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestRequireInnerCircleAccess_LowerTierBlocked(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "elite", found: true},
		Memberships: stubMembershipLookup{tier: "vip"},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/abc-vid/master.m3u8", uuid.New().String()))
	if called {
		t.Fatalf("vip member must not access elite-gated video")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRequireInnerCircleAccess_VideoNotFound_PassesThrough(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{found: false},
		Memberships: stubMembershipLookup{},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/hls/missing/master.m3u8", ""))
	if !called {
		t.Fatalf("unknown video should pass through to handler (which produces 404)")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (handler ran, set 200)", rr.Code)
	}
}

func TestRequireInnerCircleAccess_PathOutsidePrefix_PassesThrough(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{found: true, tier: "vip"},
		Memberships: stubMembershipLookup{},
	}
	rr, called := runMiddleware(t, cfg, newRequest("/api/v1/videos/foo", ""))
	if !called {
		t.Fatalf("non-HLS paths must pass through unaffected")
	}
	_ = rr
}

func TestRequireInnerCircleAccess_NonMember_403_OnStaticStreamingHls(t *testing.T) {
	channelID := uuid.New()
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: channelID, tier: "vip", found: true},
		Memberships: stubMembershipLookup{tier: ""},
		PathPrefixes: []string{
			"/api/v1/hls/",
			"/static/streaming-playlists/hls/",
		},
	}
	rr, called := runMiddleware(t, cfg,
		newRequest("/static/streaming-playlists/hls/abc-vid/720p/seg-12.ts", uuid.New().String()))
	if called {
		t.Fatalf("non-member must be blocked from static streaming-playlists segments")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRequireInnerCircleAccess_NonMember_403_OnStaticStreamingMaster(t *testing.T) {
	channelID := uuid.New()
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: channelID, tier: "supporter", found: true},
		Memberships: stubMembershipLookup{tier: ""},
		PathPrefixes: []string{
			"/api/v1/hls/",
			"/static/streaming-playlists/hls/",
		},
	}
	rr, called := runMiddleware(t, cfg,
		newRequest("/static/streaming-playlists/hls/abc-vid/master.m3u8", uuid.New().String()))
	if called {
		t.Fatalf("non-member must be blocked from static streaming-playlists master.m3u8")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestRequireInnerCircleAccess_Member_200_OnStaticStreamingHls(t *testing.T) {
	cfg := InnerCircleAccessConfig{
		Videos:      stubVideoLookup{channelID: uuid.New(), tier: "vip", found: true},
		Memberships: stubMembershipLookup{tier: "vip"},
		PathPrefixes: []string{
			"/api/v1/hls/",
			"/static/streaming-playlists/hls/",
		},
	}
	rr, called := runMiddleware(t, cfg,
		newRequest("/static/streaming-playlists/hls/abc-vid/720p/seg-1.ts", uuid.New().String()))
	if !called {
		t.Fatalf("VIP member must pass through to static streaming handler, status=%d", rr.Code)
	}
}

func TestExtractVideoIDFromPath(t *testing.T) {
	cases := []struct {
		path, prefix, want string
	}{
		{"/api/v1/hls/vid-1/master.m3u8", "/api/v1/hls/", "vid-1"},
		{"/api/v1/hls/vid-1/720p/seg-12.ts", "/api/v1/hls/", "vid-1"},
		{"/api/v1/hls/vid-only", "/api/v1/hls/", "vid-only"},
		{"/api/v1/videos/foo", "/api/v1/hls/", ""},
		{"", "/api/v1/hls/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := extractVideoIDFromPath(tc.path, tc.prefix); got != tc.want {
				t.Fatalf("extractVideoIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
