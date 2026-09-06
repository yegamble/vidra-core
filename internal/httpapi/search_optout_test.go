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
)

// The owner's A13 ruling: opting out of all three discovery controls means no
// ATTRIBUTED COLLECTION, not merely no personalized serving. A user with
// search_history_enabled, personalized_search_enabled and
// personalized_recommendations_enabled all false must produce behavioural
// search events that are indistinguishable from an anonymous visitor's — no
// user_id, no account-derived value anywhere in the payload, and the same
// day-scoped anonymous subject an anonymous caller would carry, so vidra-search
// counts them exactly ONCE in its k-anonymity floors however many session ids
// they rotate through.
//
// These tests are the core half of that rule. They are written against the
// enqueued outbox payload because that payload IS the wire format vidra-search
// stores verbatim in behavior_events.props.

// optOutAll turns all three discovery controls off for the token's account.
func optOutAll(t *testing.T, srv *Server, tok string) {
	t.Helper()
	setPrefs(t, srv, tok, `{"search_history_enabled":false,"personalized_search_enabled":false,"personalized_recommendations_enabled":false}`)
}

// setPrefs PATCHes /auth/me and fails the test if the write is rejected.
func setPrefs(t *testing.T, srv *Server, tok, body string) {
	t.Helper()
	rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/auth/me", body, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /auth/me %s = %d; body=%s", body, rec.Code, rec.Body.String())
	}
}

// payloadHas reports whether a key is present at all (absent and null differ:
// vidra-search reads props->>'user_id', so a JSON null is as unattributed as an
// absent key — but absence is the shape we ship and the shape we assert).
func payloadHas(payload map[string]json.RawMessage, key string) bool {
	_, ok := payload[key]
	return ok
}

// payloadBool reads a bool field, reporting absence as false (which is exactly
// how vidra-search's json.Unmarshal into a bool field reads it).
func payloadBool(payload map[string]json.RawMessage, key string) bool {
	raw, ok := payload[key]
	if !ok {
		return false
	}
	var b bool
	_ = json.Unmarshal(raw, &b)
	return b
}

// assertUnattributed holds one enqueued payload to the whole anonymous shape:
// no account id under any key, and the anonymous subject present so the row
// still counts once in the k floors instead of falling through to the
// client-controlled session id.
func assertUnattributed(t *testing.T, payload map[string]json.RawMessage, accountID string) {
	t.Helper()
	if payloadHas(payload, "user_id") {
		t.Errorf("payload carries user_id = %s, want absent — an opted-out user's events must not be attributed", payload["user_id"])
	}
	if subject := payloadString(payload, "subject_id"); subject == "" {
		t.Error("subject_id missing: an unattributed event must carry the day-scoped anonymous subject, or vidra-search falls back to COALESCE(subject_id, session_id) and counts the client-controlled session")
	} else if subject == accountID {
		t.Error("subject_id equals the account id — the subject must not be derived from the account")
	}
	if payloadBool(payload, "allow_history") {
		t.Error("allow_history = true on an unattributed event")
	}
	// No other key may carry the account id either: the ruling is about any
	// account-derived value in props, not only the two identity columns.
	for k, raw := range payload {
		var s string
		if json.Unmarshal(raw, &s) == nil && s == accountID {
			t.Errorf("payload key %q carries the account id", k)
		}
	}
}

// TestSearchEventsOptedOutUserIsUnattributed: the client-event path.
func TestSearchEventsOptedOutUserIsUnattributed(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	optOutAll(t, srv, tok)

	session := uuid.NewString()
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", session, tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	assertUnattributed(t, payload, id)
	if got := payloadString(payload, "session_id"); got != session {
		t.Errorf("session_id = %q, want the caller's session %q — an unattributed event keeps within-session correlation exactly as an anonymous one does", got, session)
	}
}

// TestSearchSubmittedEmitOptedOutUserIsUnattributed: the routed path. Core is
// authoritative on BOTH ingest paths or on neither — one browser search writes
// two query_log rows (this emit plus the client event) and the floor counts
// both.
func TestSearchSubmittedEmitOptedOutUserIsUnattributed(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	optOutAll(t, srv, tok)

	if rec := getSearchFrom(srv, "go", "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertUnattributed(t, eventPayload(t, outbox, 0), id)
}

// TestOptedOutSubjectIsSessionIndependent is the k-floor property the ruling
// turns on: an opted-out user rotating X-Vidra-Session must stay ONE subject.
// Otherwise "no attribution" would hand them the anonymous forgery hole core
// #167 closed — N sessions counting as N people toward the suggestion floor.
func TestOptedOutSubjectIsSessionIndependent(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	optOutAll(t, srv, tok)

	for _, session := range []string{uuid.NewString(), uuid.NewString(), uuid.NewString()} {
		if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", session, tok); rec.Code != http.StatusAccepted {
			t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	}
	first := payloadString(eventPayload(t, outbox, 0), "subject_id")
	if first == "" {
		t.Fatal("subject_id missing on an opted-out event")
	}
	for n := 1; n < 3; n++ {
		if got := payloadString(eventPayload(t, outbox, n), "subject_id"); got != first {
			t.Errorf("event %d subject_id = %q, want %q — rotating the session header must not mint new subjects", n, got, first)
		}
	}
}

// TestOptedOutClientCannotReattachIdentity: the client is not trusted to say
// who it is. A forged user_id / allow_history / allow_personalization in the
// body must be stripped before the server writes its own answer, or an opted-out
// user's own browser (or anything else posting as them) could re-attribute the
// row the server just anonymized.
func TestOptedOutClientCannotReattachIdentity(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	optOutAll(t, srv, tok)

	forged := `{"events":[{"type":"search.submitted","query":"go","results_count":7,` +
		`"user_id":"` + id + `","allow_history":true,"allow_personalization":true,` +
		`"subject_id":"forged-subject"}]}`
	if rec := postEventsFrom(srv, forged, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	assertUnattributed(t, payload, id)
	if payloadBool(payload, "allow_personalization") {
		t.Error("allow_personalization = true from the client body on an opted-out event")
	}
	if got := payloadString(payload, "subject_id"); got == "forged-subject" {
		t.Error("subject_id came from the client body")
	}
}

// TestHistoryOnlyKeepsAttributionWithoutPersonalization is constraint (b): each
// control's data flows to its OWN feature. History on with both personalization
// controls off keeps the attributed rows the user's own history page is built
// from, and feeds nothing into the watch projection.
func TestHistoryOnlyKeepsAttributionWithoutPersonalization(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	setPrefs(t, srv, tok, `{"personalized_search_enabled":false,"personalized_recommendations_enabled":false}`)

	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	if got := payloadString(payload, "user_id"); got != id {
		t.Errorf("user_id = %q, want the principal %q — the history control alone still earns attribution", got, id)
	}
	if payloadHas(payload, "subject_id") {
		t.Errorf("attributed payload carries subject_id = %s, want absent", payload["subject_id"])
	}
	if !payloadBool(payload, "allow_history") {
		t.Error("allow_history = false with the history control on")
	}
	if payloadBool(payload, "allow_personalization") {
		t.Error("allow_personalization = true with both personalization controls off — nothing may feed the watch projection")
	}
}

// TestPersonalizationOnlyKeepsAttributionWithoutHistory is the mirror: the
// projection may be fed while the user's own history page stays empty. It needs
// ADVANCED mode, because nothing in simple mode reads a watch projection and the
// consent test asks whether a store is actually running, not merely switched on
// (see searchConsent) — TestSimpleModeCollectsNoProjectionConsent is the other
// half of that pair.
func TestPersonalizationOnlyKeepsAttributionWithoutHistory(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	owner := createChannelFor(t, srv, "mallory", "mallory@example.test", "mallory")
	patchSettings(t, srv, owner, `{"search_mode":"advanced"}`)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	setPrefs(t, srv, tok, `{"search_history_enabled":false}`)

	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	// By type, not by index: flipping the mode enqueued a search.config_updated
	// ahead of the event under test.
	payload := behavioralPayload(t, outbox, searchevents.TypeSearchSubmitted)
	if got := payloadString(payload, "user_id"); got != id {
		t.Errorf("user_id = %q, want the principal %q", got, id)
	}
	if payloadBool(payload, "allow_history") {
		t.Error("allow_history = true with the history control off")
	}
	if !payloadBool(payload, "allow_personalization") {
		t.Error("allow_personalization = false with the personalization controls on")
	}
}

// TestSimpleModeCollectsNoProjectionConsent is the consequence the ruling
// accepts on purpose. In the shipped `simple` default nothing reads a watch
// projection — every personalized generator is behind `advanced` — so the
// personalization controls grant no collection there either, and the settings
// page says so by disabling them with a reason. A user whose only remaining
// control is their search history therefore reaches "nothing is stored about me"
// with the one switch the page leaves live, which is what makes the promise
// reachable on a default instance at all.
func TestSimpleModeCollectsNoProjectionConsent(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox) // testConfig() leaves search_mode at its "simple" default
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	setPrefs(t, srv, tok, `{"search_history_enabled":false}`)

	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	if payloadBool(payload, "allow_personalization") {
		t.Error("allow_personalization = true in simple mode — no personalized generator reads the projection there")
	}
	assertUnattributed(t, payload, id)
}

// TestOptOutIsForwardOnly: re-enabling resumes attribution from that moment.
// The rule is a decision taken per event at emit time, so there is nothing to
// migrate forward and nothing to retro-attribute.
func TestOptOutIsForwardOnly(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)

	optOutAll(t, srv, tok)
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("opted-out post = %d, want 202", rec.Code)
	}
	setPrefs(t, srv, tok, `{"search_history_enabled":true}`)
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", uuid.NewString(), tok); rec.Code != http.StatusAccepted {
		t.Fatalf("re-enabled post = %d, want 202", rec.Code)
	}

	assertUnattributed(t, eventPayload(t, outbox, 0), id)
	if got := payloadString(eventPayload(t, outbox, 1), "user_id"); got != id {
		t.Errorf("post-re-enable user_id = %q, want %q", got, id)
	}
}

// --- video.watch_progress: the third emit path ---

// watchProgressServer wires both the outbox and the emission throttle (unset,
// watch_progress is never emitted at all) on a server that can publish videos.
func watchProgressServer(t *testing.T, outbox *fakeSearchOutbox) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.JWTSecret = searchSubjectTestSecret
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{
		WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)),
		WithSearchWatchThrottle(func(context.Context, string) bool { return true }),
	})
	return srv
}

// behavioralPayload finds the first enqueued event of a type and decodes it.
func behavioralPayload(t *testing.T, outbox *fakeSearchOutbox, eventType string) map[string]json.RawMessage {
	t.Helper()
	for i, e := range outbox.events {
		if e.EventType == eventType {
			return eventPayload(t, outbox, i)
		}
	}
	t.Fatalf("no %s event enqueued (%d events)", eventType, len(outbox.events))
	return nil
}

// TestWatchProgressOptedOutUserIsUnattributed: video.watch_progress is emitted
// from an AUTHENTICATED route, so it was the one behavioural event that could
// never be anonymous — and vidra-search stores it in behavior_events like every
// other, then synthesises video.meaningful_watch from it, which the co-visitation
// k floor counts. Attribution has to be decided here too or the ruling has a
// hole exactly the width of a watch.
func TestWatchProgressOptedOutUserIsUnattributed(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := watchProgressServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"clip","privacy":"public"}`)
	optOutAll(t, srv, tok)

	if rec := putProgress(srv, vid, `{"position_seconds":42}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put progress = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	assertUnattributed(t, behavioralPayload(t, outbox, searchevents.TypeVideoWatchProg), id)
}

// TestWatchProgressAttributedUserKeepsUserID pins the other half: an opted-in
// user's watch_progress is unchanged, user_id and all.
func TestWatchProgressAttributedUserKeepsUserID(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := watchProgressServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	vid := createPublishedVideo(t, srv, tok, "ada", `{"title":"clip","privacy":"public"}`)

	if rec := putProgress(srv, vid, `{"position_seconds":42}`, tok); rec.Code != http.StatusNoContent {
		t.Fatalf("put progress = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	payload := behavioralPayload(t, outbox, searchevents.TypeVideoWatchProg)
	if got := payloadString(payload, "user_id"); got != id {
		t.Errorf("user_id = %q, want %q", got, id)
	}
	if payloadHas(payload, "subject_id") {
		t.Errorf("attributed watch_progress carries subject_id = %s, want absent", payload["subject_id"])
	}
}

// --- the read paths ---

// TestOptedOutUserSendsNoAccountIDToSearchService: the ruling is about what the
// search service can attribute, and a read call carrying user_id hands it the
// account id in a request line even when every flag on that request is false.
// For a fully opted-out user core sends none — the id is inert there anyway
// (personalized and include_history are both false), so nothing is lost.
func TestOptedOutUserSendsNoAccountIDToSearchService(t *testing.T) {
	var got []url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query())
		_ = json.NewEncoder(w).Encode(searchclient.SuggestionsResponse{Query: r.URL.Query().Get("q")})
	}))
	defer ts.Close()

	client := searchclient.New(ts.URL, searchTestSecret)
	srv := searchServerWith(t, WithSearchClient(client))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := principalID(t, srv, tok)
	optOutAll(t, srv, tok)

	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/search/suggestions?q=go", "", tok); rec.Code != http.StatusOK {
		t.Fatalf("suggestions = %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(got) == 0 {
		t.Fatal("the search service was never called")
	}
	if sent := got[0].Get("user_id"); sent != "" {
		t.Errorf("suggestions carried user_id = %q, want empty for a fully opted-out caller (id %q)", sent, id)
	}
}
