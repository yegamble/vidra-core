package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// The degenerate page: a HEALTHY vidra-search that simply has nothing indexed
// yet. That is not an error, so the error-only fallback never fired for it —
// and it is the likely state for a whole day after a migration import, since
// the only thing that feeds the index is a reconcile sweep on a 24h ticker.

// lastSearchSubmittedSource returns the `source` recorded on the most recent
// search.submitted event, which is the only place the handler's routing
// decision is observable from outside — the public response body deliberately
// looks the same on both paths.
func lastSearchSubmittedSource(t *testing.T, outbox *fakeSearchOutbox) string {
	t.Helper()
	for i := len(outbox.events) - 1; i >= 0; i-- {
		if outbox.events[i].EventType != searchevents.TypeSearchSubmitted {
			continue
		}
		var payload struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(outbox.events[i].Payload, &payload); err != nil {
			t.Fatalf("unmarshal search.submitted payload: %v", err)
		}
		return payload.Source
	}
	t.Fatal("no search.submitted event was enqueued")
	return ""
}

// TestEmptyServicePageFallsBackToLocalSQL is the cold-index case.
// searchViaService reports ok for a SUCCESSFUL response that happens to carry
// zero ids — hydration short-circuits and it returns an empty slice — so the
// handler took the service branch and then deliberately overwrote `total` with
// core's own SQL count. The page that went out was `{"videos": [], "total": 3}`:
// an empty grid under "3 results", with working pagination through nothing.
//
// The architecture rule is that search is never a hard dependency and any search
// failure is core's cue to fall back to its own SQL silently. That was honoured
// for errors and violated for the far more likely empty-index case.
func TestEmptyServicePageFallsBackToLocalSQL(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	outbox := &fakeSearchOutbox{}
	f := seedSearchCorpus(t, WithSearchClient(fake), WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))
	// A healthy service whose index is empty: the call succeeds, and reports an
	// honest zero for a corpus it has not been told about yet.
	zero := int64(0)
	fake.searchResp = searchclient.SearchResponse{Total: &zero}

	resp := f.search(t, "q=alpha")
	if fake.searchCalls != 1 {
		t.Fatalf("search service calls = %d, want 1 (it is healthy and must still be tried)", fake.searchCalls)
	}
	if len(resp.Videos) == 0 && resp.Total > 0 {
		t.Fatalf("served an empty grid under %d results — the page and the count describe different sets", resp.Total)
	}
	if got := f.titles(resp); len(got) != 3 {
		t.Fatalf("local SQL did not answer the request: got %v, want the 3 matching videos", got)
	}
	if resp.Total != 3 {
		t.Errorf("total = %d, want 3", resp.Total)
	}
	if src := lastSearchSubmittedSource(t, outbox); src != "local" {
		t.Errorf("search.submitted source = %q, want %q — SQL answered this request", src, "local")
	}
	// The service's paging facts must not ride a page it did not answer: a
	// `search_total: 0` beside three results is the same incoherence one field
	// further down.
	if resp.SearchTotal != nil || resp.TotalIsLowerBound != nil || resp.HasMore != nil {
		t.Errorf("the service's paging facts leaked onto a local page: search_total=%v lower_bound=%v has_more=%v",
			resp.SearchTotal, resp.TotalIsLowerBound, resp.HasMore)
	}
}

// TestGenuinelyEmptySearchStaysEmptyAndZero is the first counter-test: the guard
// must not invent results. A query that really matches nothing still returns an
// empty list beside a zero total, which is coherent and correct.
func TestGenuinelyEmptySearchStaysEmptyAndZero(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	f := seedSearchCorpus(t, WithSearchClient(fake))
	fake.searchResp = searchclient.SearchResponse{}

	resp := f.search(t, "q=nosuchvideoanywhere")
	if len(resp.Videos) != 0 {
		t.Errorf("videos = %v, want none", f.titles(resp))
	}
	if resp.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Total)
	}
}

// TestPopulatedServicePageStillAnswersFromTheService is the counter-test that
// matters most: a guard broad enough to route every search through SQL would
// disable the search service outright while looking like a bug fix.
//
// It also pins the deliberate behaviour the guard must NOT touch — the service
// ranks and pages, so `total` (core's per-viewer count of every match: 3) is
// expected to differ from the length of the page (1). That difference is the
// documented design; only the all-zero first page is the defect.
func TestPopulatedServicePageStillAnswersFromTheService(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	outbox := &fakeSearchOutbox{}
	f := seedSearchCorpus(t, WithSearchClient(fake), WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))
	svcTotal := int64(4321)
	fake.searchResp = searchclient.SearchResponse{
		IDs:   []searchclient.ScoredID{{VideoID: uuid.MustParse(f.ids["alpha long"]), Score: 1}},
		Total: &svcTotal,
	}

	resp := f.search(t, "q=alpha")
	if got, want := f.titles(resp), []string{"alpha long"}; !equalStrings(got, want) {
		t.Fatalf("titles = %v, want the service-ranked %v — the guard routed a healthy search to SQL", got, want)
	}
	if resp.Total != 3 {
		t.Errorf("total = %d, want core's own per-viewer count 3", resp.Total)
	}
	if resp.SearchTotal == nil || *resp.SearchTotal != 4321 {
		t.Errorf("search_total = %v, want 4321", resp.SearchTotal)
	}
	if src := lastSearchSubmittedSource(t, outbox); src != "search" {
		t.Errorf("search.submitted source = %q, want %q", src, "search")
	}

	// A page PAST the end of the ranked list is empty on purpose and stays on the
	// service path: "you have run out of results" is not the cold-index defect,
	// and treating it as one would re-run every deep page on SQL.
	deep := f.search(t, "q=alpha&offset=10")
	if len(deep.Videos) != 0 {
		t.Errorf("offset=10 videos = %v, want none", f.titles(deep))
	}
	if deep.SearchTotal == nil {
		t.Error("offset=10 left the service path; an empty page past the end is not the empty-index case")
	}
}
