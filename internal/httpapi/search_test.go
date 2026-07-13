package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/searchevents"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const searchTestSecret = "httpapi-search-secret-httpapi-search-secret"

// fakeSearchOutbox records enqueued events for the httpapi search tests.
type fakeSearchOutbox struct {
	events []sqlcgen.EnqueueSearchEventParams
}

func (f *fakeSearchOutbox) EnqueueSearchEvent(_ context.Context, a sqlcgen.EnqueueSearchEventParams) error {
	f.events = append(f.events, a)
	return nil
}
func (f *fakeSearchOutbox) GetVideoSearchDoc(_ context.Context, _ uuid.UUID) (sqlcgen.GetVideoSearchDocRow, error) {
	return sqlcgen.GetVideoSearchDocRow{}, nil
}
func (f *fakeSearchOutbox) ListVideoSearchDocsPage(_ context.Context, _ sqlcgen.ListVideoSearchDocsPageParams) ([]sqlcgen.ListVideoSearchDocsPageRow, error) {
	return nil, nil
}

func searchServerWith(t *testing.T, httpOpts ...Option) *Server {
	t.Helper()
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(), httpOpts)
	return srv
}

// TestSuggestionsDisabledDegradesToEmpty: with no search client wired, the
// endpoint answers 200 with an empty list (a suggestion box never errors).
func TestSuggestionsDisabledDegradesToEmpty(t *testing.T) {
	srv := searchServerWith(t)
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp suggestionsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Query != "go" || len(resp.Suggestions) != 0 {
		t.Fatalf("resp = %+v, want empty suggestions", resp)
	}
}

// TestSuggestionsFlagComputation verifies the core-computed flags per auth state:
// anonymous → personalized=false, include_history=false; signed-in in simple
// mode → personalized=false (mode gate), include_history=true (instance + user).
// hide_sensitive follows the instance policy (default hide → true).
func TestSuggestionsFlagComputation(t *testing.T) {
	var gotQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(searchclient.SuggestionsResponse{
			Query:       r.URL.Query().Get("q"),
			Suggestions: []searchclient.Suggestion{{Text: "golang", Type: "query"}},
		})
	}))
	defer ts.Close()
	client := searchclient.New(ts.URL, searchTestSecret)
	srv := searchServerWith(t, WithSearchClient(client))

	// Anonymous.
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("anon code = %d; body=%s", rec.Code, rec.Body.String())
	}
	if gotQuery.Get("personalized") != "false" || gotQuery.Get("include_history") != "false" {
		t.Errorf("anon flags: personalized=%s include_history=%s (want false/false)",
			gotQuery.Get("personalized"), gotQuery.Get("include_history"))
	}
	if gotQuery.Get("hide_sensitive") != "true" {
		t.Errorf("anon hide_sensitive=%s, want true", gotQuery.Get("hide_sensitive"))
	}
	var resp suggestionsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].Text != "golang" {
		t.Fatalf("suggestions = %+v", resp.Suggestions)
	}

	// Signed-in (simple mode): personalized stays false (mode gate), history on.
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", tok); rec.Code != http.StatusOK {
		t.Fatalf("signed-in code = %d", rec.Code)
	}
	if gotQuery.Get("personalized") != "false" {
		t.Errorf("signed-in simple-mode personalized=%s, want false", gotQuery.Get("personalized"))
	}
	if gotQuery.Get("include_history") != "true" {
		t.Errorf("signed-in include_history=%s, want true", gotQuery.Get("include_history"))
	}
}

// TestSearchEventsValidationAndEnqueue exercises the allowlist, the batch cap,
// and the happy path (enqueued to the outbox).
func TestSearchEventsValidationAndEnqueue(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	enq := searchevents.NewEnqueuer(outbox, nil)
	srv := searchServerWith(t, WithSearchEvents(enq))

	// Disallowed type → 422.
	bad := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events",
		`{"events":[{"type":"evil.event"}]}`, "")
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disallowed type code = %d, want 422; body=%s", bad.Code, bad.Body.String())
	}

	// Over the cap → 422.
	big := `{"events":[`
	for i := 0; i < 21; i++ {
		if i > 0 {
			big += ","
		}
		big += `{"type":"search.submitted"}`
	}
	big += `]}`
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events", big, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-cap code = %d, want 422", rec.Code)
	}

	// Happy path → 202 + enqueued.
	ok := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events",
		`{"events":[{"type":"search.submitted","query":"go"},{"type":"video.impression","video_id":"x"}]}`, "")
	if ok.Code != http.StatusAccepted {
		t.Fatalf("happy code = %d, want 202; body=%s", ok.Code, ok.Body.String())
	}
	if len(outbox.events) != 2 {
		t.Fatalf("enqueued = %d, want 2", len(outbox.events))
	}
	if outbox.events[0].EventType != "search.submitted" {
		t.Errorf("event[0] type = %q", outbox.events[0].EventType)
	}
}

// TestSearchHistory503WhenDisabled: the history proxy answers 503 (not a fake
// empty history / silent no-op) when the search service is unwired.
func TestSearchHistory503WhenDisabled(t *testing.T) {
	srv := searchServerWith(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		rec := sendJSONAuth(srv, m, "/api/v1/me/search-history", "", tok)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s code = %d, want 503; body=%s", m, rec.Code, rec.Body.String())
		}
		var env ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if env.Error.Code != "search_unavailable" {
			t.Errorf("%s error code = %q, want search_unavailable", m, env.Error.Code)
		}
	}
}

// TestSearchVideosServiceRoutingPreservesOrder: when the search service is wired
// it ranks; core hydrates preserving that order and drops non-hydratable ids.
func TestSearchVideosServiceRoutingPreservesOrder(t *testing.T) {
	srv0 := searchServerWith(t) // temp server to mint videos
	tok := createChannelFor(t, srv0, "ada", "ada@example.test", "ada")
	_ = tok
	// Build the real server with a search client; seed videos on it.
	var returned []searchclient.ScoredID
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(searchclient.SearchResponse{
			Query: r.URL.Query().Get("q"), IDs: returned,
		})
	}))
	defer ts.Close()
	client := searchclient.New(ts.URL, searchTestSecret)
	srv := searchServerWith(t, WithSearchClient(client))
	tok2 := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	v1 := createPublishedVideo(t, srv, tok2, "bob", `{"title":"alpha","privacy":"public"}`)
	v2 := createPublishedVideo(t, srv, tok2, "bob", `{"title":"beta","privacy":"public"}`)
	id1, _ := uuid.Parse(v1)
	id2, _ := uuid.Parse(v2)
	// Search ranks v2 before v1, and includes a bogus id that must be filtered.
	returned = []searchclient.ScoredID{
		{VideoID: id2, Score: 3}, {VideoID: uuid.New(), Score: 2}, {VideoID: id1, Score: 1},
	}
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q=a", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp videoSearchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Videos) != 2 {
		t.Fatalf("videos = %d, want 2 (bogus id filtered); body=%s", len(resp.Videos), rec.Body.String())
	}
	if resp.Videos[0].ID != v2 || resp.Videos[1].ID != v1 {
		t.Errorf("order = [%s,%s], want [%s,%s] (search order preserved)",
			resp.Videos[0].ID, resp.Videos[1].ID, v2, v1)
	}
}

// TestMeSearchPrefsRoundTrip: PATCH the new prefs and confirm GET me reflects them.
func TestMeSearchPrefsRoundTrip(t *testing.T) {
	srv := searchServerWith(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// Default: all three true.
	me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok)
	var u userView
	_ = json.Unmarshal(me.Body.Bytes(), &u)
	if !u.SearchHistoryEnabled || !u.PersonalizedSearchEnabled || !u.PersonalizedRecommendationsEnabled {
		t.Fatalf("defaults = %+v, want all true", u)
	}

	// Turn two off.
	patch := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me",
		`{"search_history_enabled":false,"personalized_recommendations_enabled":false}`, tok)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch code = %d; body=%s", patch.Code, patch.Body.String())
	}
	var after userView
	_ = json.Unmarshal(patch.Body.Bytes(), &after)
	if after.SearchHistoryEnabled || after.PersonalizedRecommendationsEnabled {
		t.Errorf("after patch = %+v, want the two toggled false", after)
	}
	if !after.PersonalizedSearchEnabled {
		t.Errorf("personalized_search should remain true, got false")
	}
}
