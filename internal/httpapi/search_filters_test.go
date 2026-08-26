package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sort + filter coverage for GET /videos/search, and the routing rule that keeps
// the two backends (vidra-search and local SQL) from disagreeing.

// searchFixture is a small corpus with the three facts the new filters read:
// publish time, duration, view count, and tags.
type searchFixture struct {
	srv  *Server
	repo *videoFakeRepo
	tok  string
	ids  map[string]string // title -> video id
}

// seedSearchCorpus creates published videos and back-fills the columns the API
// cannot set directly (views, and the probed duration).
func seedSearchCorpus(t *testing.T, httpOpts ...Option) *searchFixture {
	t.Helper()
	srv, _, _, _, repo := videoServerFullWith(t, testConfig(), httpOpts)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	f := &searchFixture{srv: srv, repo: repo, tok: tok, ids: map[string]string{}}
	for _, v := range []struct {
		title    string
		tags     string
		duration int32
		views    int64
		age      time.Duration
	}{
		{"alpha short", `["go","redis"]`, 60, 5, 3 * time.Hour},
		{"alpha medium", `["go"]`, 400, 100, 2 * time.Hour},
		{"alpha long", `["redis"]`, 1200, 50, 1 * time.Hour},
	} {
		id := createPublishedVideo(t, srv, tok, "ada",
			`{"title":"`+v.title+`","privacy":"public","tags":`+v.tags+`}`)
		f.ids[v.title] = id
		vid := uuid.MustParse(id)
		d := v.duration
		repo.metadata[vid] = sqlcgen.VideoMetadatum{VideoID: vid, DurationSeconds: &d}
		repo.views[vid] = v.views
		row := repo.videos[vid]
		row.CreatedAt = time.Now().Add(-v.age)
		repo.videos[vid] = row
	}
	return f
}

// search runs a query and returns the decoded response, failing on non-200.
func (f *searchFixture) search(t *testing.T, query string) videoSearchResponse {
	t.Helper()
	rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/videos/search?"+query, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /videos/search?%s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var resp videoSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// titles is the result titles in order.
func (f *searchFixture) titles(resp videoSearchResponse) []string {
	out := make([]string, 0, len(resp.Videos))
	for _, v := range resp.Videos {
		out = append(out, v.Title)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSearchSortOrdersResults walks every accepted ?sort value. The default
// must not move: callers that never sent a sort keep the order they had.
func TestSearchSortOrdersResults(t *testing.T) {
	f := seedSearchCorpus(t)
	newestFirst := []string{"alpha long", "alpha medium", "alpha short"}

	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"", newestFirst},          // no sort at all — the pre-existing behaviour
		{"relevance", newestFirst}, // stating the default explicitly changes nothing
		{"-published_at", newestFirst},
		{"published_at", []string{"alpha short", "alpha medium", "alpha long"}},
		{"-views", []string{"alpha medium", "alpha long", "alpha short"}},
		{"views", []string{"alpha short", "alpha long", "alpha medium"}},
	} {
		q := "q=alpha"
		if tc.sort != "" {
			q += "&sort=" + url.QueryEscape(tc.sort)
		}
		resp := f.search(t, q)
		if got := f.titles(resp); !equalStrings(got, tc.want) {
			t.Errorf("sort=%q order = %v, want %v", tc.sort, got, tc.want)
		}
		if resp.Total != 3 {
			t.Errorf("sort=%q total = %d, want 3", tc.sort, resp.Total)
		}
	}
}

// TestSearchRejectsUnknownSort pins the reason parseSortParam exists: a sort key
// the SQL has no CASE branch for would fall through to the default order and
// look like it worked.
func TestSearchRejectsUnknownSort(t *testing.T) {
	f := seedSearchCorpus(t)
	rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/videos/search?q=alpha&sort=likes", "", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown sort = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestSearchDurationRange proves the seconds range narrows the page AND the
// total together — a filtered total that counted the unfiltered set would tell
// the caller there were more pages than exist.
func TestSearchDurationRange(t *testing.T) {
	f := seedSearchCorpus(t)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"q=alpha&duration_max=240", []string{"alpha short"}},
		{"q=alpha&duration_min=240&duration_max=600", []string{"alpha medium"}},
		{"q=alpha&duration_min=600", []string{"alpha long"}},
		{"q=alpha&duration_min=60&duration_max=1200", []string{"alpha long", "alpha medium", "alpha short"}},
	} {
		resp := f.search(t, tc.query)
		if got := f.titles(resp); !equalStrings(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.query, got, tc.want)
		}
		if resp.Total != int64(len(tc.want)) {
			t.Errorf("%s total = %d, want %d", tc.query, resp.Total, len(tc.want))
		}
	}
}

// TestSearchDurationExcludesUnknownDuration pins the NULL semantics: a video
// with no probed duration cannot be PROVEN to be in range, so it is not
// returned. In SQL this falls out of `NULL >= 60` being NULL; it would be very
// easy to write a Go filter that let it through.
func TestSearchDurationExcludesUnknownDuration(t *testing.T) {
	f := seedSearchCorpus(t)
	id := createPublishedVideo(t, f.srv, f.tok, "ada", `{"title":"alpha unmeasured","privacy":"public"}`)
	delete(f.repo.metadata, uuid.MustParse(id))

	if resp := f.search(t, "q=alpha"); resp.Total != 4 {
		t.Fatalf("unfiltered total = %d, want 4 (the unmeasured video is a normal result)", resp.Total)
	}
	resp := f.search(t, "q=alpha&duration_min=0")
	for _, title := range f.titles(resp) {
		if title == "alpha unmeasured" {
			t.Fatalf("a video with no known duration matched duration_min=0: %v", f.titles(resp))
		}
	}
	if resp.Total != 3 {
		t.Errorf("duration_min=0 total = %d, want 3", resp.Total)
	}
}

// TestSearchPublishedWindow proves the RFC3339 window narrows both arms.
func TestSearchPublishedWindow(t *testing.T) {
	f := seedSearchCorpus(t)
	cutoff := time.Now().Add(-150 * time.Minute).UTC().Format(time.RFC3339)

	resp := f.search(t, "q=alpha&published_after="+url.QueryEscape(cutoff))
	if got, want := f.titles(resp), []string{"alpha long", "alpha medium"}; !equalStrings(got, want) {
		t.Errorf("published_after = %v, want %v", got, want)
	}
	if resp.Total != 2 {
		t.Errorf("published_after total = %d, want 2", resp.Total)
	}

	resp = f.search(t, "q=alpha&published_before="+url.QueryEscape(cutoff))
	if got, want := f.titles(resp), []string{"alpha short"}; !equalStrings(got, want) {
		t.Errorf("published_before = %v, want %v", got, want)
	}
}

// TestSearchTagSets covers tags_all_of (conjunction) vs tags_one_of
// (disjunction), and that the single legacy ?tag still behaves.
func TestSearchTagSets(t *testing.T) {
	f := seedSearchCorpus(t)
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"q=alpha&tags_all_of=go,redis", []string{"alpha short"}},
		{"q=alpha&tags_one_of=go,redis", []string{"alpha long", "alpha medium", "alpha short"}},
		{"q=alpha&tags_all_of=go", []string{"alpha medium", "alpha short"}},
		{"q=alpha&tags_one_of=redis", []string{"alpha long", "alpha short"}},
		// Repeated params are equivalent to the comma form.
		{"q=alpha&tags_all_of=go&tags_all_of=redis", []string{"alpha short"}},
		// Case is normalised, as it is on write.
		{"q=alpha&tags_all_of=GO,Redis", []string{"alpha short"}},
		// ?tags_all_of=GO,go survives the query-string dedupe (the two spellings
		// differ) and only collapses when the tags are lowercased for the
		// lookup — so the request really can carry a duplicate tag. It must
		// still mean "has go", not the unsatisfiable "has go twice".
		{"q=alpha&tags_all_of=GO,go", []string{"alpha medium", "alpha short"}},
		{"q=alpha&tag=redis", []string{"alpha long", "alpha short"}},
	} {
		resp := f.search(t, tc.query)
		if got := f.titles(resp); !equalStrings(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.query, got, tc.want)
		}
		if resp.Total != int64(len(tc.want)) {
			t.Errorf("%s total = %d, want %d", tc.query, resp.Total, len(tc.want))
		}
	}
}

// TestSearchFilterValidation covers the malformed inputs that must be refused
// rather than silently reinterpreted. An inverted range in particular would
// otherwise return an empty page that reads as "nothing matched".
func TestSearchFilterValidation(t *testing.T) {
	f := seedSearchCorpus(t)
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"q=alpha&duration_min=abc", http.StatusBadRequest},
		{"q=alpha&duration_min=-5", http.StatusBadRequest},
		{"q=alpha&duration_min=600&duration_max=60", http.StatusBadRequest},
		{"q=alpha&published_after=yesterday", http.StatusBadRequest},
		{"q=alpha&published_after=2026-01-02T00:00:00Z&published_before=2026-01-01T00:00:00Z", http.StatusBadRequest},
		{"q=alpha&tags_all_of=" + longTag(), http.StatusUnprocessableEntity},
	} {
		rec := sendJSONAuth(f.srv, http.MethodGet, "/api/v1/videos/search?"+tc.query, "", "")
		if rec.Code != tc.code {
			t.Errorf("%s = %d, want %d; body=%s", tc.query, rec.Code, tc.code, rec.Body.String())
		}
	}
}

func longTag() string {
	b := make([]byte, 51)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// TestFilteredSearchNeverRoutesToTheService is the consistency guarantee. The
// search service accepts only tag/category/language/license and ranks by its own
// relevance, so a request using a new filter or a different sort must run on
// local SQL — and must do so whether the service is healthy or not, or the
// result set would change with the service's mood.
//
// The fake is wired to return a DIFFERENT video from the one SQL would match,
// so "which backend answered" is visible in the response body rather than
// inferred only from a call counter.
func TestFilteredSearchNeverRoutesToTheService(t *testing.T) {
	for _, query := range []string{
		"q=alpha&sort=-published_at",
		"q=alpha&sort=published_at",
		"q=alpha&sort=-views",
		"q=alpha&duration_min=1",
		"q=alpha&duration_max=100000",
		"q=alpha&published_after=2000-01-01T00:00:00Z",
		"q=alpha&published_before=2100-01-01T00:00:00Z",
		"q=alpha&tags_all_of=go",
		"q=alpha&tags_one_of=go",
	} {
		fake := &fakeSearchGateway{healthy: true}
		f := seedSearchCorpus(t, WithSearchClient(fake))
		// The service is primed with a DECOY: one video where SQL matches three.
		// If routing ever leaked, the page length would give it away, not just
		// the call counter.
		fake.searchResp = searchclient.SearchResponse{
			IDs: []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
		}

		healthy := f.search(t, query)
		if fake.searchCalls != 0 {
			t.Errorf("%s called the search service %d times; it cannot honour this request", query, fake.searchCalls)
		}

		// Same request with the service down must produce the identical page.
		fake.healthy = false
		unhealthy := f.search(t, query)
		if !equalStrings(f.titles(healthy), f.titles(unhealthy)) {
			t.Errorf("%s changed with service health: healthy=%v unhealthy=%v",
				query, f.titles(healthy), f.titles(unhealthy))
		}
		if healthy.Total != unhealthy.Total {
			t.Errorf("%s total changed with service health: %d vs %d", query, healthy.Total, unhealthy.Total)
		}
	}
}

// TestUnfilteredRelevanceSearchStillUsesTheService is the other half of the
// routing rule: the default search, and the facets vidra-search does accept,
// are untouched by this work.
func TestUnfilteredRelevanceSearchStillUsesTheService(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	fake.searchResp = searchclient.SearchResponse{
		IDs: []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
	}

	// license is in this list, not the one above: vidra-search KNOWS this facet,
	// so a license-filtered relevance search must still be ranked by the service.
	// Making License "narrowing" would silently move it to local SQL, which is a
	// different ranking — this table is what catches that.
	for _, query := range []string{
		"q=alpha", "q=alpha&sort=relevance", "q=alpha&tag=go", "q=alpha&limit=2",
		"q=alpha&license=1",
	} {
		before := fake.searchCalls
		resp := f.search(t, query)
		if fake.searchCalls != before+1 {
			t.Errorf("%s did not route to the search service (calls %d -> %d)", query, before, fake.searchCalls)
		}
		if got, want := f.titles(resp), []string{"alpha long"}; !equalStrings(got, want) {
			t.Errorf("%s = %v, want the service-ranked %v", query, got, want)
		}
	}
}

// TestTaxonomyFiltersReachTheSearchService proves the facets the service DOES
// accept are actually handed to it. Routing a filtered request to the service is
// only correct if the service was told about the filter: forwarding it is what
// makes the page and the total describe the same, narrowed corpus. Without this,
// dropping a field from the SearchParams build would leave every routing test
// green while the service silently ranked the UNFILTERED corpus.
func TestTaxonomyFiltersReachTheSearchService(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	fake.searchResp = searchclient.SearchResponse{
		IDs: []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
	}

	f.search(t, "q=alpha&tag=go&category=1&language=en&license=1")
	if len(fake.searchParams) != 1 {
		t.Fatalf("search service called %d times, want 1", len(fake.searchParams))
	}
	got := fake.searchParams[0]
	for _, tc := range []struct{ field, value, want string }{
		{"tag", got.Tag, "go"},
		{"category", got.Category, "1"},
		{"language", got.Language, "en"},
		{"license", got.License, "1"},
	} {
		if tc.value != tc.want {
			t.Errorf("SearchParams.%s = %q, want %q — the filter never reached the service",
				tc.field, tc.value, tc.want)
		}
	}
}

// TestSearchServicePagingFieldsArePassedThrough proves the three new
// vidra-search fields reach the wire when the service reports them.
func TestSearchServicePagingFieldsArePassedThrough(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	total, lower, more := int64(4321), true, true
	fake.searchResp = searchclient.SearchResponse{
		IDs:               []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
		Total:             &total,
		TotalIsLowerBound: &lower,
		HasMore:           &more,
	}
	resp := f.search(t, "q=alpha")
	if resp.SearchTotal == nil || *resp.SearchTotal != 4321 {
		t.Errorf("search_total = %v, want 4321", resp.SearchTotal)
	}
	if resp.TotalIsLowerBound == nil || !*resp.TotalIsLowerBound {
		t.Errorf("total_is_lower_bound = %v, want true", resp.TotalIsLowerBound)
	}
	if resp.HasMore == nil || !*resp.HasMore {
		t.Errorf("has_more = %v, want true", resp.HasMore)
	}
	// The top-level total stays CORE's per-viewer count, not the service's.
	if resp.Total != 3 {
		t.Errorf("total = %d, want core's own 3 (the service's 4321 must not overwrite it)", resp.Total)
	}
}

// TestSearchServicePagingFieldsAbsentMeansUnknown is the compatibility case that
// matters at deploy time: the released search service does not report these
// fields yet. They must be OMITTED from the JSON, not rendered as 0/false — a
// `has_more: false` a client believed would stop it paging a list it had barely
// begun.
func TestSearchServicePagingFieldsAbsentMeansUnknown(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	fake.searchResp = searchclient.SearchResponse{
		IDs: []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
	}
	assertPagingFieldsAbsent(t, f.srv, "q=alpha")

	// The local SQL path never has them either.
	local := seedSearchCorpus(t)
	assertPagingFieldsAbsent(t, local.srv, "q=alpha&sort=-views")
}

func assertPagingFieldsAbsent(t *testing.T, srv *Server, query string) {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?"+query, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search %s = %d; body=%s", query, rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"search_total", "total_is_lower_bound", "has_more"} {
		if _, present := raw[k]; present {
			t.Errorf("%s: %q is present with value %s; absent must mean unknown", query, k, raw[k])
		}
	}
}
