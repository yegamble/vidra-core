package searchclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBanSuggestionEscapesWireAndSignsDecoded pins the one trap this endpoint
// shares with delete-history-query: the query segment is percent-escaped on the
// wire, but vidra-search verifies the HMAC over r.URL.Path (Go's DECODED form).
// Signing the escaped form yields a permanent 401 that looks like a bad secret.
func TestBanSuggestionEscapesWireAndSignsDecoded(t *testing.T) {
	const q = "buy cheap followers"
	var sawAuth bool
	var rawPath, decodedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT (banning twice is one end state, not two bans)", r.Method)
		}
		rawPath, decodedPath = r.URL.EscapedPath(), r.URL.Path
		sawAuth = verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path)
		_ = json.NewEncoder(w).Encode(SuggestionBan{NormalizedQuery: q, Banned: true})
	}))
	defer srv.Close()

	out, err := New(srv.URL, testSecret).BanSuggestion(context.Background(), "Buy Cheap Followers")
	if err != nil {
		t.Fatalf("ban: %v", err)
	}
	if !sawAuth {
		t.Error("HMAC did not verify over the decoded path")
	}
	if rawPath != "/internal/v1/suggestions/bans/Buy%20Cheap%20Followers" {
		t.Errorf("wire path = %q, want the escaped form", rawPath)
	}
	if decodedPath != "/internal/v1/suggestions/bans/Buy Cheap Followers" {
		t.Errorf("decoded path = %q, want the operator's input verbatim", decodedPath)
	}
	// The caller must get the SERVICE's key back, not the string it sent.
	if out.NormalizedQuery != q || !out.Banned {
		t.Errorf("ban response = %+v, want the service-normalized key", out)
	}
}

func TestUnbanSuggestionIsDelete(t *testing.T) {
	var sawAuth bool
	var method, rawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, rawPath = r.Method, r.URL.EscapedPath()
		sawAuth = verifyHMAC(r.Header.Get(authHeaderName), r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL, testSecret).UnbanSuggestion(context.Background(), "spam/slash"); err != nil {
		t.Fatalf("unban: %v", err)
	}
	if method != http.MethodDelete || !sawAuth {
		t.Errorf("method=%s hmac_ok=%v, want DELETE with a valid signature", method, sawAuth)
	}
	// A slash inside the query is escaped on the wire so it cannot break out of
	// the path segment, while the signature still covers the decoded form the
	// server sees — the same split DeleteUserHistoryQuery relies on.
	if rawPath != "/internal/v1/suggestions/bans/spam%2Fslash" {
		t.Errorf("wire path = %q, want the slash escaped inside the segment", rawPath)
	}
}

func TestListSuggestionBansForwardsPaging(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(SuggestionBanList{
			Entries: []SuggestionBanEntry{{NormalizedQuery: "spam", Query: "Spam", TotalCount: 9, DistinctUsers: 3}},
			Limit:   5, Offset: 10,
		})
	}))
	defer srv.Close()

	out, err := New(srv.URL, testSecret).ListSuggestionBans(context.Background(), 5, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if query != "limit=5&offset=10" {
		t.Errorf("query = %q, want limit=5&offset=10", query)
	}
	if len(out.Entries) != 1 || out.Entries[0].TotalCount != 9 || out.Entries[0].DistinctUsers != 3 {
		t.Errorf("entries = %+v, want the review counts decoded", out.Entries)
	}
}

// TestModerationBreakerDoesNotDegradeSearch is the invariant that decides which
// timeout group these calls live in: a moderator hammering a ban against a sick
// service must not flip Healthy() to false, because that routes EVERY viewer's
// search to the local fallback. The moderation breaker may open; search's view
// of the service must not change because of it.
func TestModerationBreakerDoesNotDegradeSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, testSecret)
	// The prober has not run; Healthy() starts optimistic.
	if !c.Healthy() {
		t.Fatal("client did not start healthy")
	}
	for i := 0; i < 20; i++ {
		_, _ = c.BanSuggestion(context.Background(), "spam")
	}
	if !c.breakers[groupModeration].isOpen() {
		t.Fatal("repeated 5xx did not open the moderation breaker (the admin UI would hang per click)")
	}
	if !c.Healthy() {
		t.Error("an operator's failed bans marked SEARCH unhealthy — every viewer would fall back to local SQL")
	}
	// A read-path breaker must still degrade search, or the guard is inverted.
	for i := 0; i < 20; i++ {
		_, _ = c.Search(context.Background(), SearchParams{Query: "x", Limit: 1})
	}
	if c.Healthy() {
		t.Error("a tripped SEARCH breaker no longer degrades search")
	}
}
