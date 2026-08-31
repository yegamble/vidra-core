package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

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

// TestSearchEventsStripsClientIdentityFields: POST /search/events is a public,
// optionalAuth endpoint whose body is copied into the search outbox. The
// identity fields (user_id, session_id, allow_history) are SERVER-derived and a
// client-supplied value for any of them must never survive. Anonymous caller,
// no X-Vidra-Session header, body forging all three.
func TestSearchEventsStripsClientIdentityFields(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))

	victim := uuid.New().String()
	body := `{"events":[{"type":"search.submitted","query":"go","results_count":7,` +
		`"user_id":"` + victim + `","session_id":"forged-not-a-uuid","allow_history":true}]}`
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events", body, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(outbox.events) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(outbox.events))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(outbox.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if raw, ok := payload["user_id"]; ok {
		t.Errorf("anonymous payload carries user_id = %s, want absent (a forged id inflates search's k-anonymity distinct-user floor)", raw)
	}
	if raw, ok := payload["session_id"]; ok {
		t.Errorf("payload carries session_id = %s, want absent (only the validated X-Vidra-Session header may set it)", raw)
	}
	var allowHistory bool
	if err := json.Unmarshal(payload["allow_history"], &allowHistory); err != nil {
		t.Fatalf("allow_history missing/invalid: %v", err)
	}
	if allowHistory {
		t.Errorf("allow_history = true for an anonymous caller, want false")
	}
	// Legitimate behavioural fields must still reach the outbox.
	var query string
	_ = json.Unmarshal(payload["query"], &query)
	var results int
	_ = json.Unmarshal(payload["results_count"], &results)
	if query != "go" || results != 7 {
		t.Errorf("non-identity fields lost: query=%q results_count=%d, want \"go\"/7", query, results)
	}
}

// TestSearchEventsAuthedIdentityIsServerDerived: a signed-in caller cannot
// attribute an event to someone else, cannot mint a session id through the body
// (only the validated header may), and cannot suppress the server's
// allow_history decision.
func TestSearchEventsAuthedIdentityIsServerDerived(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := searchServerWith(t, WithSearchEvents(searchevents.NewEnqueuer(outbox, nil)))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok)
	var self userView
	_ = json.Unmarshal(me.Body.Bytes(), &self)
	if self.ID == "" {
		t.Fatalf("could not resolve principal id; body=%s", me.Body.String())
	}

	victim := uuid.New().String()
	body := `{"events":[{"type":"search.submitted","query":"go",` +
		`"user_id":"` + victim + `","session_id":"forged-not-a-uuid","allow_history":false}]}`
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/search/events", body, tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if len(outbox.events) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(outbox.events))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(outbox.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	var userID string
	_ = json.Unmarshal(payload["user_id"], &userID)
	if userID != self.ID {
		t.Errorf("user_id = %q, want the principal %q (forged was %q)", userID, self.ID, victim)
	}
	if raw, ok := payload["session_id"]; ok {
		t.Errorf("payload carries session_id = %s, want absent (no X-Vidra-Session header was sent)", raw)
	}
	var allowHistory bool
	_ = json.Unmarshal(payload["allow_history"], &allowHistory)
	if !allowHistory {
		t.Errorf("allow_history = false, want the server-derived true (instance default + user pref + signed in)")
	}
}

// --- anonymous aggregation subject (subject_id) ---

// searchSubjectTestSecret gives the test server a JWT secret so the subject
// digester is derived. testConfig() leaves JWTSecret empty, which is the
// degrade-to-no-subject path — a real deployment always has one (config.go
// defaults it in dev and validate() requires >= 32 bytes in production).
const searchSubjectTestSecret = "search-subject-secret-search-subject-secret"

// subjectServer builds a search-events server whose subject digester is keyed.
func subjectServer(t *testing.T, outbox *fakeSearchOutbox) *Server {
	t.Helper()
	cfg := testConfig()
	cfg.JWTSecret = searchSubjectTestSecret
	srv, _, _, _, _ := videoServerFullWith(t, cfg,
		[]Option{WithSearchEvents(searchevents.NewEnqueuer(outbox, nil))})
	return srv
}

// postEventsFrom posts a search-event batch from a specific socket address with
// an optional X-Vidra-Session header and bearer token.
func postEventsFrom(srv *Server, body, remoteAddr, session, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/search/events", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = remoteAddr
	if session != "" {
		req.Header.Set("X-Vidra-Session", session)
	}
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// eventPayload decodes the nth enqueued outbox payload.
func eventPayload(t *testing.T, outbox *fakeSearchOutbox, n int) map[string]json.RawMessage {
	t.Helper()
	if len(outbox.events) <= n {
		t.Fatalf("enqueued = %d, want > %d", len(outbox.events), n)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(outbox.events[n].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload %d: %v", n, err)
	}
	return payload
}

// payloadString reads a string field, reporting absence as "".
func payloadString(payload map[string]json.RawMessage, key string) string {
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

const submittedEvent = `{"events":[{"type":"search.submitted","query":"go","results_count":7}]}`

// TestSearchEventsAnonSubjectIsSessionIndependent is THE security property.
// vidra-search's k-anonymity floor counts distinct session_ids for anonymous
// rows, and X-Vidra-Session is client-supplied — validated for UUID SHAPE only,
// so anyone can mint unlimited well-formed ids and clear the floor by rotating
// the header. The server-derived subject_id must be the same for both requests
// because it depends on the client address, not on anything the client says.
func TestSearchEventsAnonSubjectIsSessionIndependent(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	for _, session := range []string{uuid.NewString(), uuid.NewString()} {
		if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", session, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	}
	first := payloadString(eventPayload(t, outbox, 0), "subject_id")
	second := payloadString(eventPayload(t, outbox, 1), "subject_id")
	if first == "" || second == "" {
		t.Fatalf("subject_id missing (%q / %q); an anonymous event must carry a server-derived aggregation subject", first, second)
	}
	if first != second {
		t.Errorf("subject_id differs across two X-Vidra-Session values from one address (%q vs %q) — rotating the header must not mint new subjects", first, second)
	}
}

// TestSearchEventsAnonSubjectDiffersByClientAddress: two genuinely different
// clients must remain two subjects, or the field would count everyone as one.
func TestSearchEventsAnonSubjectDiffersByClientAddress(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	session := uuid.NewString()

	for _, addr := range []string{"203.0.113.7:44100", "198.51.100.9:44100"} {
		if rec := postEventsFrom(srv, submittedEvent, addr, session, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	}
	if a, b := payloadString(eventPayload(t, outbox, 0), "subject_id"), payloadString(eventPayload(t, outbox, 1), "subject_id"); a == b {
		t.Errorf("two different client addresses produced the same subject_id %q", a)
	}
}

// TestSearchEventsSubjectIDIsNeverClientSupplied: subject_id joins the
// server-derived field set, so a body-supplied value must never survive — the
// same forgery #128 closed for user_id.
func TestSearchEventsSubjectIDIsNeverClientSupplied(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	forged := "forged-subject-0000"
	body := `{"events":[{"type":"search.submitted","query":"go","subject_id":"` + forged + `"}]}`
	if rec := postEventsFrom(srv, body, "203.0.113.7:44100", "", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if got := payloadString(eventPayload(t, outbox, 0), "subject_id"); got == forged {
		t.Errorf("client-supplied subject_id %q survived into the outbox", got)
	}
}

// TestSearchEventsSessionIDSurvivesAlongsideSubject pins the co-visitation
// constraint: subject_id is an ADDITIONAL field, never a replacement for
// session_id. session_id drives within-session correlation in vidra-search
// (co-visitation edges, query reformulation); collapsing every visitor behind
// one NAT into a single shared session would splice unrelated people's browsing
// into one correlation chain.
func TestSearchEventsSessionIDSurvivesAlongsideSubject(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)

	session := uuid.NewString()
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", session, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	if got := payloadString(payload, "session_id"); got != session {
		t.Errorf("session_id = %q, want the validated header value %q unchanged", got, session)
	}
	subject := payloadString(payload, "subject_id")
	if subject == "" {
		t.Fatal("subject_id missing on an anonymous event")
	}
	if subject == session {
		t.Errorf("subject_id equals session_id (%q) — they are different identifiers with different lifetimes", subject)
	}
}

// TestSearchEventsAuthedCarriesNoSubject: for a signed-in caller user_id is
// already the trustworthy subject. Emitting an IP-derived pseudonym next to a
// known account id would be redundant AND would leak an address-derived value
// for an identified user.
func TestSearchEventsAuthedCarriesNoSubject(t *testing.T) {
	outbox := &fakeSearchOutbox{}
	srv := subjectServer(t, outbox)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	me := sendJSONAuth(srv, http.MethodGet, "/api/v1/auth/me", "", tok)
	var self userView
	_ = json.Unmarshal(me.Body.Bytes(), &self)
	if self.ID == "" {
		t.Fatalf("could not resolve principal id; body=%s", me.Body.String())
	}
	if rec := postEventsFrom(srv, submittedEvent, "203.0.113.7:44100", "", tok); rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	payload := eventPayload(t, outbox, 0)
	if got := payloadString(payload, "user_id"); got != self.ID {
		t.Errorf("user_id = %q, want the principal %q", got, self.ID)
	}
	if raw, ok := payload["subject_id"]; ok {
		t.Errorf("authenticated payload carries subject_id = %s, want absent (user_id is already the trustworthy subject)", raw)
	}
}
