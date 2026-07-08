package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestAdminStats proves the admin overview endpoint returns the instance-wide
// counts, reflects created state (public+published videos and total stored
// bytes), and is admin-gated (403 for non-admins, 401 unauthenticated).
func TestAdminStats(t *testing.T) {
	srv, _, _, _, repo := videoServerFullWith(t, testConfig(), nil)
	// The first registered account ("ada") becomes admin; bob is a plain user.
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Seed deterministic instance state directly in the video fake: one public,
	// published video with a 128-byte file (counted in published_videos) and one
	// private video with a 64-byte file (NOT a published video, but its bytes ARE
	// part of total media stored).
	pub := uuid.New()
	repo.videos[pub] = sqlcgen.GetVideoByIDRow{ID: pub, Privacy: "public", State: "published", OwnerID: uuid.New()}
	repo.files[pub] = []sqlcgen.VideoFile{{ID: uuid.New(), VideoID: pub, Kind: "original", SizeBytes: 128}}
	priv := uuid.New()
	repo.videos[priv] = sqlcgen.GetVideoByIDRow{ID: priv, Privacy: "private", State: "published", OwnerID: uuid.New()}
	repo.files[priv] = []sqlcgen.VideoFile{{ID: uuid.New(), VideoID: priv, Kind: "original", SizeBytes: 64}}

	// Non-admin is forbidden; unauthenticated is unauthorized.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/stats", "", bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin stats = %d, want 403", rec.Code)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/stats", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated stats = %d, want 401", rec.Code)
	}

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/admin/stats", "", adminTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin stats = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got adminStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	want := adminStatsResponse{
		Users:            2,   // ada + bob
		PublishedVideos:  1,   // only the public+published video
		MediaStoredBytes: 192, // 128 + 64 across all files
		FederatedPeers:   0,   // no remote actors in this harness
		Comments:         0,
	}
	if got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}
