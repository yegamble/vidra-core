package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/searchclient"
	"github.com/vidra/vidra-core/internal/searchevents"
)

// --- helpers -------------------------------------------------------------

// principalID resolves the id behind a bearer token.
func principalID(t *testing.T, srv *Server, token string) string {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", token)
	var u userView
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil || u.ID == "" {
		t.Fatalf("resolve principal: code=%d body=%s", rec.Code, rec.Body.String())
	}
	return u.ID
}

// submitSearchEvent posts one search.submitted carrying raw query text, which
// is the payload the erasure has to remove: it lands in core's own
// search_outbox as {"query": ..., "user_id": ...}.
func submitSearchEvent(t *testing.T, srv *Server, token, query string) {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events",
		`{"events":[{"type":"search.submitted","query":"`+query+`","results_count":1}]}`, token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit event: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// outboxQueriesFor lists the raw query strings still held for a user.
func outboxQueriesFor(outbox *fakeSearchOutbox, userID string) []string {
	var out []string
	for _, e := range outbox.events {
		var payload map[string]json.RawMessage
		if json.Unmarshal(e.Payload, &payload) != nil {
			continue
		}
		var uid, q string
		if raw, ok := payload["user_id"]; ok {
			_ = json.Unmarshal(raw, &uid)
		}
		if raw, ok := payload["query"]; ok {
			_ = json.Unmarshal(raw, &q)
		}
		if uid == userID && q != "" {
			out = append(out, q)
		}
	}
	return out
}

// historyDeletedScopesFor lists the scopes of every user.history_deleted event
// still queued for a user.
func historyDeletedScopesFor(outbox *fakeSearchOutbox, userID string) []string {
	var out []string
	for _, e := range outbox.events {
		if e.EventType != searchevents.TypeUserHistoryDel {
			continue
		}
		var p struct {
			UserID string `json:"user_id"`
			Scope  string `json:"scope"`
		}
		if json.Unmarshal(e.Payload, &p) != nil || p.UserID != userID {
			continue
		}
		out = append(out, p.Scope)
	}
	return out
}

// healthySearchStub is a vidra-search stand-in that accepts every history call.
func healthySearchStub(t *testing.T) (*httptest.Server, *searchclient.Client) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)
	return ts, searchclient.New(ts.URL, searchTestSecret)
}

// --- clear search history -------------------------------------------------

// TestClearSearchHistoryErasesCoreOwnSearchRows is the privacy limb. The modal
// behind this endpoint says "This permanently removes every search you have
// made on this instance. This cannot be undone." The handler used to make one
// call to vidra-search and touch nothing in core, while core's search_outbox
// held the raw query text next to the user_id — so the sentence was false in
// core's PRIMARY database.
//
// The cross-user assertion is the other half and it is the more important one:
// a purge that over-deletes is silent data loss for someone who did not ask for
// anything.
func TestClearSearchHistoryErasesCoreOwnSearchRows(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	_, client := healthySearchStub(t)
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)), WithSearchClient(client))

	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bob := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	adaID, bobID := principalID(t, srv, ada), principalID(t, srv, bob)

	submitSearchEvent(t, srv, ada, "ada private search")
	submitSearchEvent(t, srv, ada, "ada second search")
	submitSearchEvent(t, srv, bob, "bob search")

	rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history", "", ada)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear code = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if got := outboxQueriesFor(outbox, adaID); len(got) != 0 {
		t.Errorf("after a permanent clear, core still holds the caller's raw query text: %q", got)
	}
	if got := outboxQueriesFor(outbox, bobID); len(got) != 1 || got[0] != "bob search" {
		t.Errorf("another user's search rows = %q, want them untouched by ada's erasure", got)
	}
	if len(outbox.purgeCalls) != 1 || outbox.purgeCalls[0] != adaID {
		t.Errorf("purge calls = %v, want exactly one for the caller %s", outbox.purgeCalls, adaID)
	}
}

// TestClearSearchHistorySparesItsOwnPurgeEvent is the subtle limb. The clear
// queues a durable user.history_deleted — the instruction that erases the
// user's history in vidra-search even if the direct call is lost — and that
// event carries a top-level user_id exactly like the rows being deleted. If the
// purge took it too, the erasure would cancel the very instruction that
// completes it, silently: a pending outbox row has no second copy.
//
// Also asserted here: a SECOND clear (the retry a user makes when the first
// returned an error) must not eat the first one's still-pending instruction.
func TestClearSearchHistorySparesItsOwnPurgeEvent(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	_, client := healthySearchStub(t)
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)), WithSearchClient(client))

	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	adaID := principalID(t, srv, ada)
	submitSearchEvent(t, srv, ada, "ada private search")

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history", "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("first clear code = %d; body=%s", rec.Code, rec.Body.String())
	}
	scopes := historyDeletedScopesFor(outbox, adaID)
	if len(scopes) != 1 || scopes[0] != searchevents.HistoryScopeSearch {
		t.Fatalf("queued user.history_deleted scopes = %v, want exactly [%s] to survive the purge that ran alongside it",
			scopes, searchevents.HistoryScopeSearch)
	}

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history", "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("second clear code = %d; body=%s", rec.Code, rec.Body.String())
	}
	if scopes := historyDeletedScopesFor(outbox, adaID); len(scopes) != 2 {
		t.Errorf("after a repeated clear the queued instructions = %v, want both kept (the second purge ate the first one's pending event)", scopes)
	}
}

// TestClearSearchHistoryQueuesErasureWhenSearchIsDown: with no search service
// reachable the caller still gets an honest 503 — but the erasure is now
// QUEUED rather than dropped. Before this, the handler returned 503 having done
// nothing at all, so the user's request to be forgotten was silently discarded
// on exactly the instances least able to serve it.
func TestClearSearchHistoryQueuesErasureWhenSearchIsDown(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))

	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	adaID := principalID(t, srv, ada)
	submitSearchEvent(t, srv, ada, "ada private search")

	rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history", "", ada)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("clear code = %d, want an honest 503; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "search_unavailable" {
		t.Errorf("error code = %q, want search_unavailable", env.Error.Code)
	}
	if got := outboxQueriesFor(outbox, adaID); len(got) != 0 {
		t.Errorf("core still holds the caller's raw query text after a clear it answered 503 to: %q", got)
	}
	if scopes := historyDeletedScopesFor(outbox, adaID); len(scopes) != 1 || scopes[0] != searchevents.HistoryScopeSearch {
		t.Errorf("queued instructions = %v, want one %s-scoped user.history_deleted so the search-side erasure still happens when the service returns",
			scopes, searchevents.HistoryScopeSearch)
	}
}

// TestClearSearchHistoryLocalFailureIsNotReportedAsSearchOutage: when core's
// OWN database refuses the delete, the caller must not be told
// "search_unavailable" and must not be told 204. The durable instruction is
// still queued, because the search-side erasure does not depend on core's
// delete succeeding.
func TestClearSearchHistoryLocalFailureIsNotReportedAsSearchOutage(t *testing.T) {
	outbox := &fakeSearchOutbox{purgeErr: errors.New("boom")}
	_, client := healthySearchStub(t)
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)), WithSearchClient(client))

	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	adaID := principalID(t, srv, ada)

	rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history", "", ada)
	if rec.Code == http.StatusNoContent {
		t.Fatal("clear reported 204 while core's own erasure failed — the promise would be false and nobody would know")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("clear code = %d, want 500 (a local database failure is not a search outage); body=%s", rec.Code, rec.Body.String())
	}
	if scopes := historyDeletedScopesFor(outbox, adaID); len(scopes) != 1 {
		t.Errorf("queued instructions = %v, want the durable event enqueued before the local delete was attempted", scopes)
	}
}

// TestDeleteSingleSearchHistoryQueryDoesNotQueueAFullErasure pins a deliberate
// NON-change. Removing one row from the history list must never enqueue a
// user.history_deleted: that event's payload has no query field, so its scope
// is the user's ENTIRE search history — the request would silently delete
// everything the user did not ask to delete, minutes later, in vidra-search.
func TestDeleteSingleSearchHistoryQueryDoesNotQueueAFullErasure(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	// A HEALTHY service on purpose: the handler has to run all the way through
	// for this to pin anything. Gated behind a 503 it would pass vacuously.
	_, client := healthySearchStub(t)
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)), WithSearchClient(client))

	ada := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	adaID := principalID(t, srv, ada)
	submitSearchEvent(t, srv, ada, "keep me")

	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/search-history/golang", "", ada); rec.Code != http.StatusNoContent {
		t.Fatalf("single-query delete code = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if scopes := historyDeletedScopesFor(outbox, adaID); len(scopes) != 0 {
		t.Errorf("a single-query delete queued %v — that erases the caller's WHOLE history downstream", scopes)
	}
	// It must not take the local rows either: a whole-user purge here would be
	// the same over-deletion, applied to core's own database instead.
	if got := outboxQueriesFor(outbox, adaID); len(got) != 1 {
		t.Errorf("single-query delete left %q in core, want the caller's other searches untouched", got)
	}
}

// --- account deletion -----------------------------------------------------

// TestDeleteAccountErasesCoreOwnSearchRows: a hard account delete purged the
// user everywhere except core's own search_outbox, which kept their raw query
// text indefinitely under a user_id whose account no longer exists.
func TestDeleteAccountErasesCoreOwnSearchRows(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	env := newAccountEnv(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))

	token := registerAndToken(t, env.srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	adaID := principalID(t, env.srv, token)
	// Seeded through the server's own enqueuer rather than POST /search/events:
	// that route rides the video-service block, which this env does not wire.
	// The payload is byte-for-byte what handleSearchEvents would have written.
	otherID := uuid.NewString()
	srvEnqueue(env.srv, searchevents.TypeSearchSubmitted,
		`{"query":"ada private search","user_id":"`+adaID+`","allow_history":true}`)
	srvEnqueue(env.srv, searchevents.TypeVideoImpression,
		`{"video_id":"`+uuid.NewString()+`","user_id":"`+adaID+`"}`)
	srvEnqueue(env.srv, searchevents.TypeSearchSubmitted,
		`{"query":"someone else","user_id":"`+otherID+`"}`)

	if rec := doJSON(env.srv, http.MethodDelete, "/api/v1/auth/me", token, `{"password":"supersecret"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete account code = %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := outboxQueriesFor(outbox, adaID); len(got) != 0 {
		t.Errorf("a deleted account's raw query text is still in core's search_outbox: %q", got)
	}
	if got := outboxQueriesFor(outbox, otherID); len(got) != 1 {
		t.Errorf("another user's rows = %q, want them untouched by this account deletion", got)
	}
	// The durable purge instructions must survive the purge that ran with them.
	var suppress, history int
	for _, e := range outbox.events {
		switch e.EventType {
		case searchevents.TypeUserSuppress:
			suppress++
		case searchevents.TypeUserHistoryDel:
			history++
		}
	}
	if suppress != 1 || history != 1 {
		t.Errorf("purge instructions after the erasure: suppress=%d history_deleted=%d, want 1 and 1 (the local purge ate the events that perform the deletion downstream)",
			suppress, history)
	}
}

// srvEnqueue writes one raw outbox row through the server's own enqueuer.
func srvEnqueue(srv *Server, eventType, payload string) {
	srv.searchEvents.EnqueueBehavioral(context.Background(), eventType, json.RawMessage(payload))
}
