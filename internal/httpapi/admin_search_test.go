package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/searchclient"
)

// fakeSuggestionBanGateway is the read-path fake plus the three suggestion-ban
// calls. It records the EXACT query string each call received so a test can
// assert what reached vidra-search, not merely the status core answered back:
// a handler that 200s while sending the wrong key would be invisible otherwise.
// normalize mirrors the search service normalizing the path segment server-side
// and echoing back the aggregate key that actually moved.
type fakeSuggestionBanGateway struct {
	fakeSearchGateway

	banned   []string
	unbanned []string
	listReqs [][2]int

	banErr   error
	unbanErr error
	listErr  error
	listResp searchclient.SuggestionBanList
}

func (f *fakeSuggestionBanGateway) BanSuggestion(_ context.Context, q string) (searchclient.SuggestionBan, error) {
	f.banned = append(f.banned, q)
	if f.banErr != nil {
		return searchclient.SuggestionBan{}, f.banErr
	}
	// The service normalizes and echoes the key it actually moved.
	return searchclient.SuggestionBan{NormalizedQuery: normalizeLikeSearch(q), Banned: true}, nil
}

func (f *fakeSuggestionBanGateway) UnbanSuggestion(_ context.Context, q string) error {
	f.unbanned = append(f.unbanned, q)
	return f.unbanErr
}

func (f *fakeSuggestionBanGateway) ListSuggestionBans(_ context.Context, limit, offset int) (searchclient.SuggestionBanList, error) {
	f.listReqs = append(f.listReqs, [2]int{limit, offset})
	if f.listErr != nil {
		return searchclient.SuggestionBanList{}, f.listErr
	}
	return f.listResp, nil
}

// normalizeLikeSearch is a stand-in for vidra-search's normalizer: enough to
// prove core round-trips the SERVICE's key rather than the operator's input.
func normalizeLikeSearch(q string) string { return lowerASCII(q) }

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// banEnv is a wired-and-healthy server with the audit log captured.
type banEnv struct {
	srv  *Server
	gw   *fakeSuggestionBanGateway
	logs *bytes.Buffer
	// ledger is the DURABLE audit trail. The slog capture alone would not catch
	// an envelope the audit service rejects: auditEvent logs first and only then
	// best-effort persists, so an invalid resource_id would leave a convincing
	// log line and NO ledger row.
	ledger *httpAuditFakeRepo
	// admin is the owner-claimed admin; mod is a promoted moderator; user is a
	// plain authenticated account.
	admin, mod, user string
}

func newBanEnv(t *testing.T, healthy bool) *banEnv {
	t.Helper()
	var buf bytes.Buffer
	gw := &fakeSuggestionBanGateway{fakeSearchGateway: fakeSearchGateway{healthy: healthy}}
	ledger := &httpAuditFakeRepo{}
	srv := searchServerWith(t,
		WithSearchClient(gw),
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithAuditLog(audit.NewService(ledger)),
	)
	env := &banEnv{srv: srv, gw: gw, logs: &buf, ledger: ledger}
	env.admin = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	env.mod = registerAndToken(t, srv, `{"username":"moe","email":"moe@example.test","password":"supersecret"}`)
	env.user = registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	env.mod = env.promote(t, "moe", "moe@example.test")
	return env
}

// newBanEnvUnwired builds the same server with NO search client at all — the
// deployment that never set SEARCH_SERVICE_URL.
func newBanEnvUnwired(t *testing.T) *banEnv {
	t.Helper()
	var buf bytes.Buffer
	ledger := &httpAuditFakeRepo{}
	srv := searchServerWith(t,
		WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))),
		WithAuditLog(audit.NewService(ledger)),
	)
	env := &banEnv{srv: srv, logs: &buf, ledger: ledger}
	env.admin = createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	env.mod = registerAndToken(t, srv, `{"username":"moe","email":"moe@example.test","password":"supersecret"}`)
	env.user = registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	env.mod = env.promote(t, "moe", "moe@example.test")
	return env
}

// promote raises username to moderator through the real admin API and returns a
// FRESH token: the role travels in the JWT, so a token minted before the
// promotion still says "user".
func (env *banEnv) promote(t *testing.T, username, email string) string {
	t.Helper()
	var list struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	rec := getWithAuth(env.srv, "/api/v1/admin/users", env.admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users = %d; body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	for _, u := range list.Users {
		if u.Username != username {
			continue
		}
		r := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"role":"moderator"}`, env.admin)
		if r.Code != http.StatusOK {
			t.Fatalf("promote %s = %d; body=%s", username, r.Code, r.Body.String())
		}
		login := postTo(env.srv, "/api/v1/auth/login",
			`{"email":"`+email+`","password":"supersecret"}`)
		if login.Code != http.StatusOK {
			t.Fatalf("re-login %s = %d; body=%s", username, login.Code, login.Body.String())
		}
		var ar authResponse
		if err := json.Unmarshal(login.Body.Bytes(), &ar); err != nil {
			t.Fatalf("unmarshal login: %v", err)
		}
		if ar.User.Role != "moderator" {
			t.Fatalf("promoted %s has role %q, want moderator", username, ar.User.Role)
		}
		return ar.Token
	}
	t.Fatalf("user %q not found to promote", username)
	return ""
}

func (env *banEnv) audits(t *testing.T) []map[string]any { return auditEvents(t, env.logs) }

// assertLedger proves the durable audit row exists with the expected target.
func (env *banEnv) assertLedger(t *testing.T, action, result string, resourceID any) {
	t.Helper()
	for _, r := range env.ledger.rows {
		if r.Action == action && r.Result == result {
			if r.ResourceType != "search_query" || r.ResourceID != resourceID {
				t.Errorf("ledger row %s: resource %s/%q, want search_query/%v",
					action, r.ResourceType, r.ResourceID, resourceID)
			}
			return
		}
	}
	t.Errorf("no durable audit_log row for %s/%s — the envelope was rejected", action, result)
}

const banPath = "/api/v1/admin/search/suggestion-bans"

// TestSuggestionBanModeratorCanBanAndUnban is the acceptance case: the lever is
// reachable by the person on shift, it carries the operator's key to the search
// service, and both directions land in the audit ledger with the actor.
func TestSuggestionBanModeratorCanBanAndUnban(t *testing.T) {
	env := newBanEnv(t, true)

	rec := sendJSONAuth(env.srv, http.MethodPut, banPath+"/Buy%20Cheap%20Followers", "", env.mod)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderator ban = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var banned struct {
		NormalizedQuery string `json:"normalized_query"`
		Banned          bool   `json:"banned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &banned); err != nil {
		t.Fatalf("unmarshal ban: %v", err)
	}
	// The response echoes the SERVICE's key, not the operator's input: a later
	// unban has to target the row that actually moved.
	if banned.NormalizedQuery != "buy cheap followers" || !banned.Banned {
		t.Errorf("ban response = %+v, want the service-normalized key with banned=true", banned)
	}
	// The decoded path segment reaches the client verbatim (escaping is the
	// client's job, and the HMAC is signed over the decoded form).
	if len(env.gw.banned) != 1 || env.gw.banned[0] != "Buy Cheap Followers" {
		t.Fatalf("search saw bans %q, want exactly [\"Buy Cheap Followers\"]", env.gw.banned)
	}

	// Unban.
	rec = sendJSONAuth(env.srv, http.MethodDelete, banPath+"/buy%20cheap%20followers", "", env.mod)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("moderator unban = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(env.gw.unbanned) != 1 || env.gw.unbanned[0] != "buy cheap followers" {
		t.Fatalf("search saw unbans %q, want exactly [\"buy cheap followers\"]", env.gw.unbanned)
	}

	// Audit: both directions, with the acting moderator.
	events := env.audits(t)
	ban := findAudit(events, observability.ActionSearchSuggestionBan, observability.ResultSuccess)
	if ban == nil {
		t.Fatal("no suggestion-ban success audit event")
	}
	if id, _ := ban["actor_id"].(string); id == "" {
		t.Error("ban audit must carry the acting moderator's actor_id")
	}
	if role, _ := ban["actor_role"].(string); role != "moderator" {
		t.Errorf("ban audit actor_role = %q, want moderator", role)
	}
	unban := findAudit(events, observability.ActionSearchSuggestionUnban, observability.ResultSuccess)
	if unban == nil {
		t.Fatal("no suggestion-unban success audit event")
	}
	if id, _ := unban["actor_id"].(string); id == "" {
		t.Error("unban audit must carry the acting moderator's actor_id")
	}
	// The ban and its reversal name the SAME target, so the ledger can be read
	// as a pair. The target is carried as a fingerprint of the aggregate key —
	// a search query is user-authored free text and never enters audit_log.
	if ban["resource_id"] == nil || ban["resource_id"] != unban["resource_id"] {
		t.Errorf("ban/unban resource_id = %v / %v, want the same non-empty target fingerprint",
			ban["resource_id"], unban["resource_id"])
	}
	if rt, _ := ban["resource_type"].(string); rt != "search_query" {
		t.Errorf("ban audit resource_type = %q, want search_query", rt)
	}
	// And it reached the DURABLE ledger, not just the log: audit_log validates
	// resource_id against a bounded-identifier pattern, so a query with a space
	// in it would be dropped with nothing but a warn line to show for it.
	env.assertLedger(t, observability.ActionSearchSuggestionBan, observability.ResultSuccess, ban["resource_id"])
	env.assertLedger(t, observability.ActionSearchSuggestionUnban, observability.ResultSuccess, unban["resource_id"])
	// The plaintext query must not appear anywhere in the captured logs.
	if bytes.Contains(bytes.ToLower(env.logs.Bytes()), []byte("cheap followers")) {
		t.Error("the banned query text leaked into the logs/audit ledger")
	}
}

// TestSuggestionBanListIsReviewable: the list is the reversal surface — a second
// operator has to be able to see what someone else banned.
func TestSuggestionBanListIsReviewable(t *testing.T) {
	env := newBanEnv(t, true)
	env.gw.listResp = searchclient.SuggestionBanList{
		Entries: []searchclient.SuggestionBanEntry{{
			NormalizedQuery: "buy cheap followers",
			Query:           "Buy Cheap Followers",
			TotalCount:      42,
			DistinctUsers:   7,
			FirstSeen:       time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			LastSeen:        time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		}},
		Limit: 20, Offset: 0,
	}

	rec := getWithAuth(env.srv, banPath+"?limit=5&offset=10", env.mod)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderator list = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Entries []struct {
			NormalizedQuery string `json:"normalized_query"`
			Query           string `json:"query"`
			TotalCount      int64  `json:"total_count"`
			DistinctUsers   int    `json:"distinct_users"`
			FirstSeen       string `json:"first_seen"`
			LastSeen        string `json:"last_seen"`
		} `json:"entries"`
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(body.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(body.Entries))
	}
	e := body.Entries[0]
	if e.NormalizedQuery != "buy cheap followers" || e.Query != "Buy Cheap Followers" {
		t.Errorf("entry keys = %+v, want both the aggregate key and its display form", e)
	}
	if e.TotalCount != 42 || e.DistinctUsers != 7 {
		t.Errorf("entry counts = %d/%d, want 42/7 — the evidence a reviewer judges the ban on", e.TotalCount, e.DistinctUsers)
	}
	if e.FirstSeen == "" || e.LastSeen == "" {
		t.Errorf("entry seen window = %q..%q, want both timestamps", e.FirstSeen, e.LastSeen)
	}
	// Paging is forwarded, not silently defaulted.
	if len(env.gw.listReqs) != 1 || env.gw.listReqs[0] != [2]int{5, 10} {
		t.Errorf("search saw list paging %v, want [[5 10]]", env.gw.listReqs)
	}
	// A read is not a moderation action: it must not write a ban audit event.
	for _, ev := range env.audits(t) {
		if a, _ := ev["action"].(string); a == observability.ActionSearchSuggestionBan ||
			a == observability.ActionSearchSuggestionUnban {
			t.Errorf("listing bans wrote a moderation audit event: %v", ev)
		}
	}
}

// TestSuggestionBanRequiresModeratorRole is the gating the council was explicit
// about: a query ban is a MODERATION action, so the moderator on shift holds the
// lever; a plain user does not, and an anonymous caller is 401 (not 403 — the
// distinction is load-bearing for the frontend).
func TestSuggestionBanRequiresModeratorRole(t *testing.T) {
	env := newBanEnv(t, true)

	cases := []struct{ method, path string }{
		{http.MethodGet, banPath},
		{http.MethodPut, banPath + "/spam"},
		{http.MethodDelete, banPath + "/spam"},
	}
	for _, tc := range cases {
		if rec := sendJSONAuth(env.srv, tc.method, tc.path, "", env.user); rec.Code != http.StatusForbidden {
			t.Errorf("plain user %s %s = %d, want 403", tc.method, tc.path, rec.Code)
		}
		if rec := sendJSONAuth(env.srv, tc.method, tc.path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", tc.method, tc.path, rec.Code)
		}
		// The admin holds it too — moderator-OR-admin, not moderator-only.
		rec := sendJSONAuth(env.srv, tc.method, tc.path, "", env.admin)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Errorf("admin %s %s = %d, want to be allowed", tc.method, tc.path, rec.Code)
		}
	}
	// Nothing a rejected caller sent reached the search service.
	if len(env.gw.banned) != 1 || len(env.gw.unbanned) != 1 {
		t.Errorf("search saw bans=%d unbans=%d, want exactly the admin's one of each",
			len(env.gw.banned), len(env.gw.unbanned))
	}
	// A 403 is never audited as a successful ban.
	if findAudit(env.audits(t), observability.ActionSearchSuggestionBan, observability.ResultSuccess) == nil {
		t.Error("the admin's ban should still have been audited")
	}
}

// TestSuggestionBanUnwiredFailsHonestly: a deployment with no search service
// must answer 503 search_unavailable and must NOT record a ban that never
// happened. A silent success here is how a moderator believes an abusive string
// is gone when it is still in autosuggest.
func TestSuggestionBanUnwiredFailsHonestly(t *testing.T) {
	env := newBanEnvUnwired(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, banPath},
		{http.MethodPut, banPath + "/spam"},
		{http.MethodDelete, banPath + "/spam"},
	} {
		rec := sendJSONAuth(env.srv, tc.method, tc.path, "", env.mod)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("unwired %s %s = %d, want 503; body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "search_unavailable" {
			t.Errorf("unwired %s error code = %q, want search_unavailable", tc.method, body.Error.Code)
		}
	}
	events := env.audits(t)
	if findAudit(events, observability.ActionSearchSuggestionBan, observability.ResultSuccess) != nil {
		t.Error("a ban that never reached the search service was audited as a success")
	}
	if findAudit(events, observability.ActionSearchSuggestionUnban, observability.ResultSuccess) != nil {
		t.Error("an unban that never reached the search service was audited as a success")
	}
	// It IS recorded as a failure: an operator reading the ledger after an
	// outage must be able to see the attempt.
	if findAudit(events, observability.ActionSearchSuggestionBan, observability.ResultFailure) == nil {
		t.Error("the failed ban attempt is missing from the audit ledger")
	}
}

// TestSuggestionBanUnhealthyFailsHonestly: wired but the service is down (the
// breaker/prober says so) → 503, no call attempted, no success audit.
func TestSuggestionBanUnhealthyFailsHonestly(t *testing.T) {
	env := newBanEnv(t, false)

	rec := sendJSONAuth(env.srv, http.MethodPut, banPath+"/spam", "", env.mod)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy ban = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if len(env.gw.banned) != 0 {
		t.Errorf("an unhealthy service was still called: %q", env.gw.banned)
	}
	if findAudit(env.audits(t), observability.ActionSearchSuggestionBan, observability.ResultSuccess) != nil {
		t.Error("a ban against an unhealthy service was audited as a success")
	}
}

// TestSuggestionBanUpstreamErrorFailsHonestly: the service is healthy but the
// call itself fails → 503, not a fake 200.
func TestSuggestionBanUpstreamErrorFailsHonestly(t *testing.T) {
	env := newBanEnv(t, true)
	env.gw.banErr = errors.New("boom")
	env.gw.unbanErr = errors.New("boom")
	env.gw.listErr = errors.New("boom")

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, banPath},
		{http.MethodPut, banPath + "/spam"},
		{http.MethodDelete, banPath + "/spam"},
	} {
		rec := sendJSONAuth(env.srv, tc.method, tc.path, "", env.mod)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("upstream error %s = %d, want 503; body=%s", tc.method, rec.Code, rec.Body.String())
		}
	}
	if findAudit(env.audits(t), observability.ActionSearchSuggestionBan, observability.ResultSuccess) != nil {
		t.Error("a failed upstream ban was audited as a success")
	}
}

// TestSuggestionBanRejectsEmptyQuery: a segment that is only whitespace is a
// client error (422), not a 503 and not a call to the service.
func TestSuggestionBanRejectsEmptyQuery(t *testing.T) {
	env := newBanEnv(t, true)
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		rec := sendJSONAuth(env.srv, m, banPath+"/%20%20", "", env.mod)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s blank query = %d, want 422; body=%s", m, rec.Code, rec.Body.String())
		}
	}
	if len(env.gw.banned) != 0 || len(env.gw.unbanned) != 0 {
		t.Errorf("a blank query still reached the search service: bans=%q unbans=%q", env.gw.banned, env.gw.unbanned)
	}
}

// TestSuggestionBanAdminToggleOffIsForbidden: search_service_enabled=false is a
// deliberate operator decision, not an outage — 403 feature_disabled, exactly as
// the search-history proxy distinguishes them.
func TestSuggestionBanAdminToggleOffIsForbidden(t *testing.T) {
	env := newBanEnv(t, true)
	setSearchSetting(t, env.srv, instancesettings.KeySearchServiceEnabled, false)

	rec := sendJSONAuth(env.srv, http.MethodPut, banPath+"/spam", "", env.mod)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("toggle-off ban = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "feature_disabled" {
		t.Errorf("toggle-off error code = %q, want feature_disabled (not search_unavailable)", body.Error.Code)
	}
	if len(env.gw.banned) != 0 {
		t.Errorf("the service was called while smart search is switched off: %q", env.gw.banned)
	}
}

// TestSuggestionBanContractDocumentsEveryStatus closes the failure class a
// previous review found: TestOpenAPIContract only pairs routes with paths, so a
// status core really returns can be missing from the spec and a contract-first
// frontend never learns to handle it. These three routes have two non-obvious
// answers — 403 for a moderator-less caller OR a switched-off search service,
// and 503 when search is unwired — and both must be in the contract.
func TestSuggestionBanContractDocumentsEveryStatus(t *testing.T) {
	spec := filepath.Join("..", "..", "api", "openapi.yaml")
	const collection = "/api/v1/admin/search/suggestion-bans"
	const item = collection + "/{query}"
	want := map[string][]string{
		"GET " + collection: {"200", "401", "403", "503"},
		"PUT " + item:       {"200", "401", "403", "422", "503"},
		"DELETE " + item:    {"204", "401", "403", "422", "503"},
	}
	got := declaredStatuses(t, spec)
	for op, statuses := range want {
		have := got[op]
		if have == nil {
			t.Fatalf("%s is not documented at all", op)
		}
		for _, code := range statuses {
			if !have[code] {
				t.Errorf("%s: status %s is returned but not documented", op, code)
			}
		}
		for code := range have {
			if !slices.Contains(statuses, code) {
				t.Errorf("%s: status %s is documented but the handler cannot return it", op, code)
			}
		}
	}
}

// declaredStatuses parses api/openapi.yaml by indentation (the same shape
// declaredOperations relies on) into "METHOD /path" -> set of status codes.
func declaredStatuses(t *testing.T, specPath string) map[string]map[string]bool {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec at %s: %v", specPath, err)
	}
	statusLine := regexp.MustCompile(`^"(\d{3})":$`)
	out := map[string]map[string]bool{}
	inPaths, inResponses := false, false
	path, op := "", ""
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			inPaths, path, op, inResponses = trimmed == "paths:", "", "", false
		case !inPaths:
		case indent == 2 && strings.HasPrefix(trimmed, "/") && strings.HasSuffix(trimmed, ":"):
			path, op, inResponses = strings.TrimSuffix(trimmed, ":"), "", false
		case indent == 4 && path != "":
			inResponses = false
			if m := specMethod.FindStringSubmatch(trimmed); m != nil {
				op = strings.ToUpper(m[1]) + " " + path
				out[op] = map[string]bool{}
			} else {
				op = ""
			}
		case indent == 6 && op != "":
			inResponses = trimmed == "responses:"
		case indent == 8 && op != "" && inResponses:
			if m := statusLine.FindStringSubmatch(trimmed); m != nil {
				out[op][m[1]] = true
			}
		}
	}
	return out
}
