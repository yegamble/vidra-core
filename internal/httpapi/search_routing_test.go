package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/searchclient"
)

// newAlways500SearchServer returns an httptest server that counts every hit and
// answers 500, so a test can trip the real client's breaker and prove the handler
// stops calling it.
func newAlways500SearchServer(t *testing.T, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

// fakeSearchGateway is an in-memory searchGateway for the W9 routing truth table:
// a programmable Healthy() flag, per-method call counters (to assert the service
// is / is NOT called), and canned responses/errors. It never touches the network.
type fakeSearchGateway struct {
	healthy bool

	searchCalls     int
	suggestCalls    int
	homeRecCalls    int
	relatedRecCalls int
	getHistoryCalls int
	delHistoryCalls int
	delQueryCalls   int
	delUserCalls    int

	searchResp  searchclient.SearchResponse
	suggestResp searchclient.SuggestionsResponse
	homeResp    searchclient.RecommendationsResponse
	relatedResp searchclient.RecommendationsResponse
	historyResp searchclient.HistoryResponse
	searchErr   error
	suggestErr  error
	recsErr     error
	historyErr  error
	// readyErr is the on-demand /readyz probe's answer (admin status page), and
	// readyHook runs inside it so a test can hold the probe open.
	readyErr   error
	readyHook  func()
	readyCalls int
}

func (f *fakeSearchGateway) Healthy() bool { return f.healthy }

func (f *fakeSearchGateway) Ready(context.Context) error {
	f.readyCalls++
	if f.readyHook != nil {
		f.readyHook()
	}
	return f.readyErr
}

func (f *fakeSearchGateway) Suggestions(_ context.Context, _ searchclient.SuggestParams) (searchclient.SuggestionsResponse, error) {
	f.suggestCalls++
	return f.suggestResp, f.suggestErr
}

func (f *fakeSearchGateway) Search(_ context.Context, _ searchclient.SearchParams) (searchclient.SearchResponse, error) {
	f.searchCalls++
	return f.searchResp, f.searchErr
}

func (f *fakeSearchGateway) RecommendationsHome(_ context.Context, _ searchclient.RecsParams) (searchclient.RecommendationsResponse, error) {
	f.homeRecCalls++
	return f.homeResp, f.recsErr
}

func (f *fakeSearchGateway) RecommendationsRelated(_ context.Context, _ searchclient.RecsParams) (searchclient.RecommendationsResponse, error) {
	f.relatedRecCalls++
	return f.relatedResp, f.recsErr
}

func (f *fakeSearchGateway) GetUserHistory(_ context.Context, _ searchclient.HistoryParams) (searchclient.HistoryResponse, error) {
	f.getHistoryCalls++
	return f.historyResp, f.historyErr
}

func (f *fakeSearchGateway) DeleteUserHistory(_ context.Context, _ uuid.UUID) error {
	f.delHistoryCalls++
	return f.historyErr
}

func (f *fakeSearchGateway) DeleteUserHistoryQuery(_ context.Context, _ uuid.UUID, _ string) error {
	f.delQueryCalls++
	return f.historyErr
}

func (f *fakeSearchGateway) DeleteUser(_ context.Context, _ uuid.UUID) error {
	f.delUserCalls++
	return nil
}

// setSearchSetting flips a bool instance setting on the server's overlay.
func setSearchSetting(t *testing.T, srv *Server, key string, on bool) {
	t.Helper()
	v := "false"
	if on {
		v = "true"
	}
	if err := srv.settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		key: {Value: v},
	}, uuid.Nil); err != nil {
		t.Fatalf("apply %s=%s: %v", key, v, err)
	}
}

// searchQuery hits GET /videos/search and returns the decoded response.
func searchQuery(t *testing.T, srv *Server, q string) videoSearchResponse {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q="+q, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /videos/search = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp videoSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal search resp: %v", err)
	}
	return resp
}

// TestRoutingAdminToggleOff proves that flipping search_service_enabled off routes
// EVERY surface to its backup WITHOUT calling the service: search → local SQL,
// suggestions → 200 empty, history GET/DELETE → 403 feature_disabled.
func TestRoutingAdminToggleOff(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)

	// Admin turns smart search off.
	setSearchSetting(t, srv, instancesettings.KeySearchServiceEnabled, false)

	// Search → local SQL (the video still surfaces), no service call.
	resp := searchQuery(t, srv, "alpha")
	if fake.searchCalls != 0 {
		t.Errorf("search called the service while admin-off: %d calls", fake.searchCalls)
	}
	if len(resp.Videos) != 1 || resp.Videos[0].ID != vid {
		t.Errorf("SQL backup did not return the video: %+v", resp.Videos)
	}

	// Suggestions → 200 empty, no service call.
	sugg := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	if sugg.Code != http.StatusOK {
		t.Fatalf("suggestions code = %d, want 200", sugg.Code)
	}
	var sres suggestionsResponse
	_ = json.Unmarshal(sugg.Body.Bytes(), &sres)
	if len(sres.Suggestions) != 0 || fake.suggestCalls != 0 {
		t.Errorf("suggestions not empty/skipped: len=%d calls=%d", len(sres.Suggestions), fake.suggestCalls)
	}

	// History GET/DELETE → 403 feature_disabled, no service call.
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		rec := sendJSONAuth(srv, m, "/api/v1/me/search-history", "", tok)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s history code = %d, want 403; body=%s", m, rec.Code, rec.Body.String())
		}
		var env ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "feature_disabled" {
			t.Errorf("%s history code = %q, want feature_disabled", m, env.Error.Code)
		}
	}
	if fake.getHistoryCalls != 0 || fake.delHistoryCalls != 0 {
		t.Errorf("history reached the service while admin-off: get=%d del=%d", fake.getHistoryCalls, fake.delHistoryCalls)
	}
}

// TestRoutingHealthyUsesService proves that with the service wired, the admin
// toggle on, and Healthy() true, search + suggestions route to vidra-search.
func TestRoutingHealthyUsesService(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)
	id, _ := uuid.Parse(vid)
	fake.searchResp = searchclient.SearchResponse{IDs: []searchclient.ScoredID{{VideoID: id, Score: 1}}}
	fake.suggestResp = searchclient.SuggestionsResponse{Suggestions: []searchclient.Suggestion{{Text: "golang", Type: "query"}}}

	resp := searchQuery(t, srv, "zzz") // query the service would rank; SQL wouldn't match "alpha"
	if fake.searchCalls != 1 {
		t.Errorf("search service calls = %d, want 1", fake.searchCalls)
	}
	if len(resp.Videos) != 1 || resp.Videos[0].ID != vid {
		t.Errorf("service result not hydrated: %+v", resp.Videos)
	}

	sugg := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	var sres suggestionsResponse
	_ = json.Unmarshal(sugg.Body.Bytes(), &sres)
	if fake.suggestCalls != 1 || len(sres.Suggestions) != 1 {
		t.Errorf("suggestions not served by service: calls=%d len=%d", fake.suggestCalls, len(sres.Suggestions))
	}
}

// TestRoutingUnhealthySkipsService proves that when Healthy() is false (the prober
// or a breaker has flagged the service down) every surface fails over immediately
// WITHOUT calling the service, and history answers 503 (down, not admin-off).
func TestRoutingUnhealthySkipsService(t *testing.T) {
	fake := &fakeSearchGateway{healthy: false}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)

	// Search → local SQL, no service call (no timeout latency paid).
	resp := searchQuery(t, srv, "alpha")
	if fake.searchCalls != 0 {
		t.Errorf("search called the unhealthy service: %d calls", fake.searchCalls)
	}
	if len(resp.Videos) != 1 || resp.Videos[0].ID != vid {
		t.Errorf("SQL backup did not return the video: %+v", resp.Videos)
	}

	// Suggestions → empty, no call.
	sugg := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	var sres suggestionsResponse
	_ = json.Unmarshal(sugg.Body.Bytes(), &sres)
	if len(sres.Suggestions) != 0 || fake.suggestCalls != 0 {
		t.Errorf("suggestions not empty/skipped: len=%d calls=%d", len(sres.Suggestions), fake.suggestCalls)
	}

	// History → 503 search_unavailable (down case), no service call.
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/me/search-history", "", tok)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("history code = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "search_unavailable" {
		t.Errorf("history code = %q, want search_unavailable", env.Error.Code)
	}
	if fake.getHistoryCalls != 0 {
		t.Errorf("history reached the unhealthy service: %d calls", fake.getHistoryCalls)
	}
}

// TestRoutingHealthyRequestTimeoutFallsBack proves the last-resort safety: when
// Healthy() is true the service IS called, but a per-request error/timeout still
// degrades that request to local SQL (the response contract is unchanged).
func TestRoutingHealthyRequestTimeoutFallsBack(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true, searchErr: context.DeadlineExceeded}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)

	resp := searchQuery(t, srv, "alpha")
	if fake.searchCalls != 1 {
		t.Errorf("service should be attempted while healthy: calls=%d", fake.searchCalls)
	}
	if len(resp.Videos) != 1 || resp.Videos[0].ID != vid {
		t.Errorf("per-request fallback to SQL failed: %+v", resp.Videos)
	}
}

// TestRoutingBreakerOpenSkipsServiceEndToEnd wires the REAL client (not the fake)
// and trips its search-group breaker, then confirms the handler consults
// Healthy() (which folds in breaker state) and skips the service — a distinct
// signal from the prober but the same routing outcome.
func TestRoutingBreakerOpenSkipsServiceEndToEnd(t *testing.T) {
	var hits int
	ts := newAlways500SearchServer(t, &hits)
	defer ts.Close()
	client := searchclient.New(ts.URL, searchTestSecret)
	srv := searchServerWith(t, WithSearchClient(client))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"alpha","privacy":"public"}`)

	// Trip the search-group breaker directly (5 consecutive 5xx) so Healthy() reads
	// false without waiting for organic traffic.
	for i := 0; i < 5; i++ {
		_, _ = client.Search(context.Background(), searchclient.SearchParams{Query: "x", Limit: 20})
	}
	if client.Healthy() {
		t.Fatal("client should be unhealthy with the search breaker open")
	}
	hitsBefore := hits

	resp := searchQuery(t, srv, "alpha")
	if hits != hitsBefore {
		t.Errorf("handler hit the service despite an open breaker (hits %d -> %d)", hitsBefore, hits)
	}
	if len(resp.Videos) != 1 || resp.Videos[0].ID != vid {
		t.Errorf("SQL backup did not return the video: %+v", resp.Videos)
	}
}

// TestInstanceSearchBlockEffectiveValues covers the /instance search block matrix:
// service_enabled and suggestions_enabled are EFFECTIVE (wired AND admin toggle,
// with suggestions also gated by its own toggle).
func TestInstanceSearchBlockEffectiveValues(t *testing.T) {
	getBlock := func(srv *Server) instanceSearchConfig {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/instance", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /instance = %d", rec.Code)
		}
		var body instanceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal instance: %v", err)
		}
		return body.Search
	}

	// Wired + both toggles on → both effective-true.
	fake := &fakeSearchGateway{healthy: true}
	srv := searchServerWith(t, WithSearchClient(fake))
	if b := getBlock(srv); !b.ServiceEnabled || !b.SuggestionsEnabled {
		t.Errorf("wired/on: service_enabled=%v suggestions_enabled=%v, want true/true", b.ServiceEnabled, b.SuggestionsEnabled)
	}

	// Admin routing toggle off → BOTH false (frontend stops per-keystroke traffic).
	setSearchSetting(t, srv, instancesettings.KeySearchServiceEnabled, false)
	if b := getBlock(srv); b.ServiceEnabled || b.SuggestionsEnabled {
		t.Errorf("admin-off: service_enabled=%v suggestions_enabled=%v, want false/false", b.ServiceEnabled, b.SuggestionsEnabled)
	}

	// Routing on again but suggestions toggle off → service_enabled true,
	// suggestions_enabled false.
	setSearchSetting(t, srv, instancesettings.KeySearchServiceEnabled, true)
	setSearchSetting(t, srv, instancesettings.KeySearchSuggestionsEnabled, false)
	if b := getBlock(srv); !b.ServiceEnabled || b.SuggestionsEnabled {
		t.Errorf("suggestions-off: service_enabled=%v suggestions_enabled=%v, want true/false", b.ServiceEnabled, b.SuggestionsEnabled)
	}

	// Not wired at all → both false regardless of toggles.
	srv2 := searchServerWith(t)
	if b := getBlock(srv2); b.ServiceEnabled || b.SuggestionsEnabled {
		t.Errorf("unwired: service_enabled=%v suggestions_enabled=%v, want false/false", b.ServiceEnabled, b.SuggestionsEnabled)
	}
}
