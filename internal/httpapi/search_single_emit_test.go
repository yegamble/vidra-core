package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/searchevents"
)

// One user-initiated browser search must write exactly ONE query_log row.
//
// It used to write two. The browser's own POST /search/events carries a
// search.submitted (that is the row that reaches the user's history page,
// because handleSearchEvents is the only ingest path that sets allow_history),
// and the GET /videos/search behind it emits a SECOND one through
// emitSearchSubmitted. Both land in the same table, so every browser search
// double-counted `use_count`, doubled the rows a k-anonymity floor reads, and
// paginating multiplied it again — the routed emit fires per REQUEST, so a
// "Load more" wrote another row for a search the reader never re-issued.
//
// The fix is a declaration, not a heuristic: a client that emits its own
// search.submitted says so on the request, and core skips its routed emit for
// that request. It has to be the client that says it, because nothing on the
// server can tell a browser (which will send the event) from an API consumer
// (which will not) — and an API-only client must keep getting the routed emit,
// which is the ONLY record its searches ever produce.
//
// Suppression is safe to accept on a caller's word: it can only make core
// collect LESS about that caller, never more, and never about anyone else.

// getSearchDeclaringClientEvents GETs /videos/search announcing that this
// client emits its own search.submitted.
func getSearchDeclaringClientEvents(srv *Server, q, remoteAddr, token, declaration string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q="+q, nil)
	req.RemoteAddr = remoteAddr
	req.Header.Set(searchEventsHeader, declaration)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func countSearchSubmitted(outbox *fakeSearchOutbox) int {
	n := 0
	for _, e := range outbox.events {
		if e.EventType == searchevents.TypeSearchSubmitted {
			n++
		}
	}
	return n
}

// TestRoutedSearchSubmittedSkippedWhenClientEmitsItsOwn is the whole point: a
// browser's search leaves exactly one record, the client's.
func TestRoutedSearchSubmittedSkippedWhenClientEmitsItsOwn(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	if rec := getSearchDeclaringClientEvents(srv, "go", "203.0.113.7:44100", "", "client"); rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countSearchSubmitted(outbox); n != 0 {
		t.Errorf("routed search.submitted events = %d, want 0 — the client said it emits its own", n)
	}
}

// TestRoutedSearchSubmittedKeptForApiOnlyClients: the declaration is opt-in, so
// every caller that does not make it keeps the behaviour it has. This is the
// regression guard for the only record an API consumer's search ever leaves.
func TestRoutedSearchSubmittedKeptForApiOnlyClients(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	outbox.events = nil

	if rec := getSearchFrom(srv, "go", "203.0.113.7:44100", "", tok); rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countSearchSubmitted(outbox); n != 1 {
		t.Errorf("routed search.submitted events = %d, want 1 for a caller that made no declaration", n)
	}
}

// TestRoutedSearchSubmittedIgnoresAnUnrecognisedDeclaration: only the one token
// suppresses. A stray or misspelled value must fail SAFE — toward recording the
// search, which is the behaviour every existing client has.
func TestRoutedSearchSubmittedIgnoresAnUnrecognisedDeclaration(t *testing.T) {
	for _, declaration := range []string{"", "server", "clientish", "1"} {
		outbox := &fakeSearchOutbox{}
		srv := subjectServer(t, outbox)
		if rec := getSearchDeclaringClientEvents(srv, "go", "203.0.113.7:44100", "", declaration); rec.Code != http.StatusOK {
			t.Fatalf("declaration %q: code = %d, want 200; body=%s", declaration, rec.Code, rec.Body.String())
		}
		if n := countSearchSubmitted(outbox); n != 1 {
			t.Errorf("declaration %q: routed search.submitted events = %d, want 1", declaration, n)
		}
	}
}

// TestRoutedSearchSubmittedSuppressionIsCaseInsensitive: header values are not
// a case-sensitive protocol anywhere else in this server, and a client that
// spells it "Client" means the same thing.
func TestRoutedSearchSubmittedSuppressionIsCaseInsensitive(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	if rec := getSearchDeclaringClientEvents(srv, "go", "203.0.113.7:44100", "", " Client "); rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if n := countSearchSubmitted(outbox); n != 0 {
		t.Errorf("routed search.submitted events = %d, want 0", n)
	}
}

// TestClientSearchSubmittedStillLandsAfterSuppression: the declaration silences
// core's own emit and NOTHING else. The client's batch is still ingested,
// attributed by the same rule, and it is now the single record of the search.
func TestClientSearchSubmittedStillLandsAfterSuppression(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	outbox.events = nil

	if rec := getSearchDeclaringClientEvents(srv, "go", "203.0.113.7:44100", tok, "client"); rec.Code != http.StatusOK {
		t.Fatalf("search: code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("events: code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if n := countSearchSubmitted(outbox); n != 1 {
		t.Fatalf("search.submitted events = %d, want exactly 1 (the client's)", n)
	}
	payload := behavioralPayload(t, outbox, searchevents.TypeSearchSubmitted)
	if got := payloadString(payload, "user_id"); got != id {
		t.Errorf("user_id = %q, want the principal %q — the surviving record is still attributed by the consent rule", got, id)
	}
}

// TestRoutedSearchSubmittedSuppressedOnEveryPage: paging is not a new search.
// The frontend sends the declaration on every page, and the client emits its
// own event only for page one, so a walk through the results leaves one row.
func TestRoutedSearchSubmittedSuppressedOnEveryPage(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	for _, offset := range []string{"0", "20", "40"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/search?q=go&offset="+offset, nil)
		req.RemoteAddr = "203.0.113.7:44100"
		req.Header.Set(searchEventsHeader, "client")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("offset %s: code = %d, want 200; body=%s", offset, rec.Code, rec.Body.String())
		}
	}
	if n := countSearchSubmitted(outbox); n != 0 {
		t.Errorf("routed search.submitted events = %d over three pages, want 0", n)
	}
}
