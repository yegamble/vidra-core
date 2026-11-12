package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/video"
)

// cachedServer wires the anonymous cache the way cmd/api does, with a TTL long
// enough that only an invalidation can explain a fresh answer.
func cachedServer(t *testing.T, httpOpts ...Option) *Server {
	t.Helper()
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), httpOpts, video.WithAnonymousCache(time.Hour))
	return srv
}

func anonymousFeedIDs(t *testing.T, srv *Server) []string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos?limit=50", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /videos = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Videos []struct {
			ID string `json:"id"`
		} `json:"videos"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ids := make([]string, 0, len(body.Videos))
	for _, v := range body.Videos {
		ids = append(ids, v.ID)
	}
	if body.Total != len(ids) {
		t.Fatalf("total = %d but %d videos listed", body.Total, len(ids))
	}
	slices.Sort(ids)
	return ids
}

// A moderator's block, its lifting, an owner's unpublish and a delete each leave
// (or rejoin) the anonymous feed with the request that made them — the cache's
// TTL is an hour here, so nothing else can explain the change.
func TestTakedownsLeaveTheCachedAnonymousFeedAtOnce(t *testing.T) {
	srv := cachedServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	a := createPublishedVideo(t, srv, admin, "ada", `{"title":"alpha","privacy":"public"}`)
	b := createPublishedVideo(t, srv, admin, "ada", `{"title":"beta","privacy":"public"}`)
	c := createPublishedVideo(t, srv, admin, "ada", `{"title":"gamma","privacy":"public"}`)
	want := func(step string, ids ...string) {
		t.Helper()
		slices.Sort(ids)
		if got := anonymousFeedIDs(t, srv); !slices.Equal(got, ids) {
			t.Fatalf("%s: anonymous feed = %v, want %v", step, got, ids)
		}
	}
	want("published", a, b, c)

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+a+"/block", `{"reason":"tos"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	want("blocked", b, c)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/admin/videos/"+a+"/block", "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d; body=%s", rec.Code, rec.Body.String())
	}
	want("unblocked", a, b, c)

	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+b, `{"privacy":"private"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("unpublish = %d; body=%s", rec.Code, rec.Body.String())
	}
	want("made private", a, c)
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/videos/"+c, "", admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d; body=%s", rec.Code, rec.Body.String())
	}
	want("deleted", a)
}

// The home rail in simple mode is the same for every caller without an account,
// so vidra-search is asked once; a signed-in caller is always asked for, and a
// takedown reaches the cached rail at once.
func TestAnonymousHomeRailAsksSearchOnce(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv := cachedServer(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	a := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)
	b := createPublishedVideo(t, srv, tok, "ada", `{"title":"beta","privacy":"public"}`)
	fake.homeResp = searchclient.RecommendationsResponse{Items: []searchclient.RecItem{
		{VideoID: uuid.MustParse(a), Score: 2, Reason: "trending"},
		{VideoID: uuid.MustParse(b), Score: 1, Reason: "fresh"},
	}}
	rail := func(token string) []string {
		t.Helper()
		resp := recResponse(t, srv, "/api/v1/recommendations/home?limit=5", token)
		if resp.Source != "search" {
			t.Fatalf("source = %q, want search", resp.Source)
		}
		ids := make([]string, 0, len(resp.Items))
		for _, it := range resp.Items {
			ids = append(ids, it.ID)
		}
		return ids
	}

	for i := 0; i < 3; i++ {
		if got := rail(""); !slices.Equal(got, []string{a, b}) {
			t.Fatalf("anonymous rail = %v, want [a b]", got)
		}
	}
	if fake.homeRecCalls != 1 {
		t.Fatalf("vidra-search asked %d times for three anonymous rails, want 1", fake.homeRecCalls)
	}
	rail(tok)
	rail(tok)
	if fake.homeRecCalls != 3 {
		t.Fatalf("after two signed-in rails: %d calls, want 3 (never shared)", fake.homeRecCalls)
	}
	// A different page size is a different answer.
	recResponse(t, srv, "/api/v1/recommendations/home?limit=6", "")
	if fake.homeRecCalls != 4 {
		t.Fatalf("limit=6 reused the limit=5 rail (%d calls)", fake.homeRecCalls)
	}

	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+a+"/block", `{"reason":"tos"}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := rail(""); !slices.Equal(got, []string{b}) {
		t.Fatalf("rail after the block = %v, want [b]", got)
	}
}
