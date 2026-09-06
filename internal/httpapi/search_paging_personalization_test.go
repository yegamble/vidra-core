package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/searchclient"
)

// --- has_more on the vidra-search page (A13 SRC-03) ---

// TestServicePageHasMoreDescribesTheClientPage pins the meaning of `has_more` on
// the response: "another page of THIS request would return results".
//
// The service is asked for an OVER-FETCH (overfetchCount: (offset+limit)*2+10
// ids, so the canonical predicate can drop some and still leave a full page) and
// answers HasMore for ITS OWN window. Core then slices [offset,offset+limit) out
// of what it hydrated — and used to forward the service's flag verbatim. So a
// query whose whole match set fits inside the over-fetch shipped
// `has_more: false` on page one: the frontend's resolveHasMore believes an
// explicit has_more over `loaded < total`, hides the Load-more control, and the
// rest of the results become unreachable. At the shipped page size of 20 the
// over-fetch is 50, so every query matching 21..50 videos was affected.
func TestServicePageHasMoreDescribesTheClientPage(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))

	ids := make([]searchclient.ScoredID, 0, 3)
	for _, title := range []string{"alpha short", "alpha medium", "alpha long"} {
		ids = append(ids, searchclient.ScoredID{VideoID: uuid.MustParse(f.ids[title]), Score: 1})
	}
	total := int64(3)
	no := false
	// The service saw the whole match set inside core's over-fetch, so its own
	// answer is an honest "no further page FROM ME".
	fake.searchResp = searchclient.SearchResponse{IDs: ids, Total: &total, HasMore: &no}

	resp := f.search(t, "q=alpha&limit=1&offset=0")
	if len(resp.Videos) != 1 {
		t.Fatalf("videos = %d, want 1 (the requested page size)", len(resp.Videos))
	}
	if resp.Total != 3 {
		t.Fatalf("total = %d, want 3", resp.Total)
	}
	if resp.HasMore == nil {
		t.Fatal("has_more absent on a service-answered page")
	}
	if !*resp.HasMore {
		t.Errorf("has_more = false on page 1 of 3 results — the client is told to stop paging with 2 results unreachable")
	}

	// The last page must still say false: this must not become "always true".
	resp = f.search(t, "q=alpha&limit=1&offset=2")
	if resp.HasMore == nil || *resp.HasMore {
		t.Errorf("has_more = %v on the final page, want false", resp.HasMore)
	}
}

// TestServicePageHasMoreKeepsTheServiceCeiling: when the service DOES report
// more beyond core's over-fetch, that fact must survive even though core's own
// hydrated slice ended. Otherwise a large result set stops at the over-fetch.
func TestServicePageHasMoreKeepsTheServiceCeiling(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))

	ids := make([]searchclient.ScoredID, 0, 3)
	for _, title := range []string{"alpha short", "alpha medium", "alpha long"} {
		ids = append(ids, searchclient.ScoredID{VideoID: uuid.MustParse(f.ids[title]), Score: 1})
	}
	total := int64(9000)
	yes := true
	fake.searchResp = searchclient.SearchResponse{IDs: ids, Total: &total, HasMore: &yes}

	resp := f.search(t, "q=alpha&limit=3&offset=0")
	if resp.HasMore == nil || !*resp.HasMore {
		t.Errorf("has_more = %v, want true — the service reported more beyond core's over-fetch", resp.HasMore)
	}
}

// --- the personalized flag on the recommendation rails (A13 SRC-03) ---

// recResponse decodes a recommendations body.
func recResponse(t *testing.T, srv *Server, path, token string) recommendationsResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, path, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
	}
	var resp recommendationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// TestRecommendationsPersonalizedFlagIsModeGated.
//
// `personalized` is not decoration: HomeRecommendationsRail picks its heading
// from it, rendering "For you" when true and "Trending now" when false. But
// vidra-search dispatches BOTH rails on the instance search_mode — simple mode
// runs homeSimple/relatedSimple, which ignore the Personalized parameter
// entirely — so in the shipped default a signed-in user was handed the
// byte-identical anonymous list under a "For you" heading.
//
// Personalized SEARCH already carries the mode gate (searchViaService:
// searchAdvanced() && instancePersonalizedSearch() && ...). The rails did not.
func TestRecommendationsPersonalizedFlagIsModeGated(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)
	id := uuid.MustParse(vid)
	fake.homeResp = searchclient.RecommendationsResponse{
		Items: []searchclient.RecItem{{VideoID: id, Score: 1, Reason: "trending"}},
	}
	fake.relatedResp = fake.homeResp

	// No settings service wired => the shipped default, search_mode=simple.
	if got := srv.searchMode(); got != instancesettings.DefaultSearchMode {
		t.Fatalf("fixture search_mode = %q, want the shipped default", got)
	}

	home := recResponse(t, srv, "/api/v1/recommendations/home?limit=5", tok)
	if home.Personalized {
		t.Errorf("home personalized=true in simple mode — the rail renders \"For you\" over the anonymous list")
	}
	related := recResponse(t, srv, "/api/v1/videos/"+vid+"/recommendations?limit=5", tok)
	if related.Personalized {
		t.Errorf("related personalized=true in simple mode — nothing personal was applied")
	}
}

// TestServicePageHasMoreStaysUnknownWithoutEvidence: a search service too old to
// report has_more must not be turned into a fabricated false once core's own
// slice runs out. Absent means unknown, and the client falls back to
// `loaded < total` — the compatibility rule
// TestSearchServicePagingFieldsAbsentMeansUnknown pins.
func TestServicePageHasMoreStaysUnknownWithoutEvidence(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	ids := make([]searchclient.ScoredID, 0, 3)
	for _, title := range []string{"alpha short", "alpha medium", "alpha long"} {
		ids = append(ids, searchclient.ScoredID{VideoID: uuid.MustParse(f.ids[title]), Score: 1})
	}
	// No Total, no HasMore: the released service's shape.
	fake.searchResp = searchclient.SearchResponse{IDs: ids}

	// Inside the hydrated list core is certain, and says so.
	if resp := f.search(t, "q=alpha&limit=1&offset=0"); resp.HasMore == nil || !*resp.HasMore {
		t.Errorf("has_more = %v on page 1 of 3 hydrated rows, want true (core holds them)", resp.HasMore)
	}
	// Past it core knows nothing, and must say nothing.
	if resp := f.search(t, "q=alpha&limit=3&offset=0"); resp.HasMore != nil {
		t.Errorf("has_more = %v with no evidence either way, want absent", *resp.HasMore)
	}
}
