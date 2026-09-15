package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/searchclient"
)

// TestSuggestionsRecheckPerVideoAuthorization is the H1 regression guard: the
// autocomplete surface (/search/suggestions) must NOT trust the search index's
// static `eligible` flag. Even when the index (here, the search gateway) reports
// a private or unlisted video as an eligible suggestion — the shape a stale or
// directly corrupted documents row with eligible=true takes — core must re-check
// per-video authorization against the DB (the SAME canonical predicate
// /videos/search hydrates with) and drop the title before it reaches anon or a
// non-owner viewer's suggestion box.
//
// The gateway returning the private/unlisted ids IS the corrupted-eligible-row
// simulation at core's boundary: core sees an "eligible" suggestion for a video
// that is not public, exactly as it would if the index row were forced true.
func TestSuggestionsRecheckPerVideoAuthorization(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true} // wired + healthy + toggle-default-on => service path
	srv := searchServerWith(t, WithSearchClient(fake))

	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Three published videos differing only in privacy. Private/unlisted are
	// published on purpose: the ONLY thing that must keep them out of suggestions
	// is the privacy re-check, not their state.
	publicID := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Public Title","privacy":"public"}`)
	privateID := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Secret Private Title","privacy":"private"}`)
	unlistedID := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Hidden Unlisted Title","privacy":"unlisted"}`)

	str := func(s string) *string { return &s }
	// The gateway hands back, for each of the three, the shape a doc-derived title
	// suggestion actually takes on the wire: type "query" (a search-term
	// completion) but carrying the backing video id so the gateway can authorize
	// it (the paired vidra-search change attaches that id). Plus a plain aggregate
	// query row with no id, which must always survive. The re-check keys off the
	// id, NOT the type label — so a private title dressed as a "query" row is still
	// dropped, and a "video"-typed row is re-checked identically.
	fake.suggestResp = searchclient.SuggestionsResponse{
		Suggestions: []searchclient.Suggestion{
			{Text: "golang", Type: "query"}, // pure aggregate, no video id
			{Text: "Public Title", Type: "query", VideoID: &publicID},
			{Text: "Secret Private Title", Type: "query", VideoID: &privateID},
			{Text: "Hidden Unlisted Title", Type: "video", VideoID: &unlistedID},
			// A well-formed but nonexistent id: the index can outlive a deleted
			// video, and an id core can no longer resolve must also drop.
			{Text: "Ghost Title", Type: "query", VideoID: str("00000000-0000-0000-0000-000000000001")},
		},
	}

	get := func(t *testing.T, token string) []suggestionView {
		t.Helper()
		rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", token)
		if rec.Code != http.StatusOK {
			t.Fatalf("suggestions code = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var resp suggestionsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal suggestions: %v", err)
		}
		return resp.Suggestions
	}

	texts := func(sv []suggestionView) map[string]bool {
		m := make(map[string]bool, len(sv))
		for _, s := range sv {
			m[s.Text] = true
		}
		return m
	}

	// The leaked-title assertion holds for anon, for a non-owner, AND for the
	// owner: suggestions now mirror /videos/search, which is a PUBLIC search and
	// never surfaces a private/unlisted title through the index. ("owner may" in
	// the H1 brief is permissive — not returning it is within spec and keeps the
	// two surfaces aligned.)
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"anon", ""},
		{"non-owner", otherTok},
		{"owner", ownerTok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := texts(get(t, tc.token))
			if !got["golang"] {
				t.Errorf("query suggestion 'golang' was dropped; a non-video row must survive")
			}
			if !got["Public Title"] {
				t.Errorf("public video suggestion was dropped; a visible title must survive")
			}
			for _, leaked := range []string{"Secret Private Title", "Hidden Unlisted Title", "Ghost Title"} {
				if got[leaked] {
					t.Errorf("LEAK: suggestion %q reached %s despite the per-video re-check", leaked, tc.name)
				}
			}
		})
	}
}

// TestSuggestionsRecheckDropsUnparseableVideoID proves the re-check never lets a
// suggestion whose video id core cannot even parse pass through unverified: an
// unverifiable title is treated as not-visible and dropped, while the plain query
// row and the genuinely visible public row survive.
func TestSuggestionsRecheckDropsUnparseableVideoID(t *testing.T) {
	fake := &fakeSearchGateway{healthy: true}
	srv := searchServerWith(t, WithSearchClient(fake))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	publicID := createPublishedVideo(t, srv, tok, "ada", `{"title":"Public Title","privacy":"public"}`)

	bad := "not-a-uuid"
	fake.suggestResp = searchclient.SuggestionsResponse{
		Suggestions: []searchclient.Suggestion{
			{Text: "golang", Type: "query"},
			{Text: "Public Title", Type: "video", VideoID: &publicID},
			{Text: "Bad Id Title", Type: "video", VideoID: &bad},
		},
	}

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp suggestionsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	seen := map[string]bool{}
	for _, s := range resp.Suggestions {
		seen[s.Text] = true
	}
	if !seen["golang"] || !seen["Public Title"] {
		t.Errorf("valid rows dropped: %+v", resp.Suggestions)
	}
	if seen["Bad Id Title"] {
		t.Errorf("LEAK: a suggestion with an unparseable video id passed the re-check")
	}
}
