package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestOpenAPIContractDocumentsGateStatuses generalises, repo-wide, the guard
// TestSuggestionBanContractDocumentsEveryStatus pins for three operations.
//
// TestOpenAPIContract pairs ROUTES with PATHS and nothing else: a mutation that
// deletes a "403" from a documented operation leaves it green. That blind spot
// shipped a real defect — core answers 403 feature_disabled from the
// search-history endpoints when the admin toggle is off, the spec documented
// only 200/503, and vidra-user (which generates its client from this spec) drew
// a permanent, deliberate 403 as a retryable error with a retry button that
// could never succeed.
//
// This guard probes the LIVE Echo router rather than reading Go source, so what
// it asserts is observed, not inferred:
//
//   - an ANONYMOUS request that comes back 401 proves the route sits behind the
//     requireAuth middleware — a hard gate the handler is never reached past —
//     so the operation must document 401 and must declare `security` (a
//     generated client that believes the route is public sends no bearer token
//     at all);
//   - a request carrying a VALID token for the plain "user" role that comes
//     back 403 proves a gate the caller cannot pass — requireRole on an admin
//     route, or a FeatureDisabledError from a switched-off feature — so the
//     operation must document 403.
//
// LIMITS, stated plainly. The probe is a sound LOWER bound on each operation's
// status set and cannot support the opposite direction. Statuses that need real
// collaborators or request data — the 401 POST /auth/login returns for bad
// credentials, the 403 an ownership check returns for someone else's video —
// are invisible to it, so "documented but the probe never saw it" is not
// evidence of a fictional status: measured on this spec there are 15 such 401s
// and 36 such 403s, every one of them genuine. Detecting a per-operation
// fictional status would need a call-graph over the handlers and the
// error-to-status mapper; TestOpenAPIStatusVocabulary below is the decidable
// slice of that direction which is worth its cost.
//
// The server is built with fullRouteOptions — the same wiring
// TestOpenAPIContract uses, so both guards see one route surface — whose
// instance-settings overlay carries zero-value Defaults. Feature toggles that
// derive their default from config are therefore OFF, which is what lets the
// probe observe the 403 feature_disabled answers on the upload routes. A toggle
// hardcoded to default true in the settings registry (search_service_enabled)
// stays on, and its 403 is out of the probe's reach — see
// TestSearchHistoryContractDocumentsEveryStatus.
func TestOpenAPIContractDocumentsGateStatuses(t *testing.T) {
	spec := filepath.Join("..", "..", "api", "openapi.yaml")
	statuses := declaredStatuses(t, spec)
	secured := declaredSecurity(t, spec)

	srv := quietContractServer(t)
	issuer := auth.NewTokenIssuer(contractTestJWTSecret, "vidra", "vidra", time.Minute)
	// The probe token must name a LIVE session: since AUTH-05 slice (c) the auth
	// middleware resolves the session on every authenticated request, so a
	// session-less token 401s everywhere and the probe would see no 403 gates at
	// all. contractAuthRepo answers that one lookup and nothing else.
	userToken, err := issuer.IssueForSession(contractProbeUserID, "user", contractProbeSessionID.String())
	if err != nil {
		t.Fatalf("issue probe token: %v", err)
	}

	ops := sortedKeys(registeredOperations(t))
	gated401, gated403 := 0, 0
	for _, op := range ops {
		declared, documented := statuses[op]
		if !documented {
			continue // TestOpenAPIContract owns the route-without-a-doc direction.
		}
		method, path, _ := strings.Cut(op, " ")
		concrete := openAPIParam.ReplaceAllString(path, probePathValue)

		if probeStatus(srv, method, concrete, "") == http.StatusUnauthorized {
			gated401++
			if !declared["401"] {
				t.Errorf("%s: an unauthenticated request is refused 401 by requireAuth, but the operation does not document 401", op)
			}
			if !secured[op] {
				t.Errorf("%s: an unauthenticated request is refused 401 by requireAuth, but the operation declares no `security` — a generated client will send no bearer token", op)
			}
		}
		if probeStatus(srv, method, concrete, userToken) == http.StatusForbidden {
			gated403++
			if !declared["403"] {
				t.Errorf("%s: an authenticated plain-user request is refused 403, but the operation does not document 403", op)
			}
		}
	}

	// Coverage is part of the assertion: if a refactor stopped the probe reaching
	// the gates, this guard would silently pass on everything. The floors are
	// well below the counts measured when it was written (220 and 78 of 302).
	if gated401 < 200 {
		t.Errorf("only %d of %d operations were observed to 401 anonymously — the probe has lost coverage of requireAuth", gated401, len(ops))
	}
	if gated403 < 60 {
		t.Errorf("only %d of %d operations were observed to 403 for a plain user — the probe has lost coverage of requireRole/feature gates", gated403, len(ops))
	}
}

// TestSearchHistoryContractDocumentsEveryStatus pins the exact status set of the
// three search-history operations, in the idiom
// TestSuggestionBanContractDocumentsEveryStatus established. They are the
// operations the original defect was found on and they are outside the probe's
// reach: their 403 needs search_service_enabled flipped off in the DB overlay
// (proven behaviourally by TestRoutingAdminToggleOff), which a probe against a
// storeless server cannot arrange.
func TestSearchHistoryContractDocumentsEveryStatus(t *testing.T) {
	spec := filepath.Join("..", "..", "api", "openapi.yaml")
	const collection = "/api/v1/me/search-history"
	want := map[string][]string{
		"GET " + collection:                 {"200", "401", "403", "503"},
		"DELETE " + collection:              {"204", "401", "403", "503"},
		"DELETE " + collection + "/{query}": {"204", "401", "403", "503"},
	}
	got := declaredStatuses(t, spec)
	for op, expected := range want {
		have := got[op]
		if have == nil {
			t.Fatalf("%s is not documented at all", op)
		}
		for _, code := range expected {
			if !have[code] {
				t.Errorf("%s: status %s is returned but not documented", op, code)
			}
		}
		for code := range have {
			if !slices.Contains(expected, code) {
				t.Errorf("%s: status %s is documented but the handler cannot return it", op, code)
			}
		}
	}
}

// contractStatusVocabulary is every HTTP status api/openapi.yaml is allowed to
// document. It is the decidable half of the "no fictional status" direction: a
// typo'd or copy-pasted code ("402" on an API with no payment wall, "451" on one
// with no legal-block path) fails here immediately, with no false positives and
// no static analysis. Widening the API's answer vocabulary is a deliberate act
// and costs one line in this list.
//
// It cannot see a status that is real elsewhere in the API but fictional on the
// operation carrying it — that needs the per-operation call-graph analysis
// TestOpenAPIContractDocumentsGateStatuses explains is out of reach. 500 is
// deliberately absent: the spec documents no internal-error responses.
var contractStatusVocabulary = []string{
	"200", "201", "202", "204", "206",
	"302", "304", "307",
	"400", "401", "403", "404", "409", "410", "413", "415", "422", "429",
	"501", "502", "503",
}

func TestOpenAPIStatusVocabulary(t *testing.T) {
	spec := filepath.Join("..", "..", "api", "openapi.yaml")
	used := map[string][]string{}
	for op, codes := range declaredStatuses(t, spec) {
		for code := range codes {
			used[code] = append(used[code], op)
		}
	}
	for code, ops := range used {
		if !slices.Contains(contractStatusVocabulary, code) {
			sort.Strings(ops)
			t.Errorf("status %s is documented on %d operation(s) (e.g. %s) but is not in contractStatusVocabulary — either core cannot return it, or add it to the list in the same change that starts returning it", code, len(ops), ops[0])
		}
	}
	for _, code := range contractStatusVocabulary {
		if used[code] == nil {
			t.Errorf("contractStatusVocabulary allows %s but no operation documents it — drop it so the list stays a real inventory", code)
		}
	}
}

// probePathValue fills every path parameter. A UUID satisfies the {id}-shaped
// ones; the gates under test all run before any handler parses the path, so a
// value the handler would later reject changes nothing this guard observes.
const probePathValue = "00000000-0000-0000-0000-000000000001"

// openAPIParam matches an OpenAPI path parameter ("{id}") for substitution.
var openAPIParam = regexp.MustCompile(`\{[^/]+\}`)

// quietContractServer builds the full route surface with logging discarded: the
// probe deliberately reaches handlers wired to nil collaborators, and the panics
// Echo recovers from would otherwise bury the test output in stack traces.
func quietContractServer(t *testing.T) *Server {
	t.Helper()
	opts := append(fullRouteOptions(), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	srv := New(testConfig(), nil, nil, opts...)
	srv.echo.Logger.SetOutput(io.Discard)
	return srv
}

// probeStatus sends one request through the real middleware chain and returns
// the status. A recovered panic (nil collaborators) surfaces as 500, which is
// simply "not a gate" as far as this guard is concerned.
func probeStatus(srv *Server, method, path, token string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec.Code
}

// declaredSecurity parses api/openapi.yaml by indentation (the same shape
// declaredOperations and declaredStatuses rely on) into "METHOD /path" ->
// whether the operation declares a `security` requirement.
func declaredSecurity(t *testing.T, specPath string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec at %s: %v", specPath, err)
	}
	out := map[string]bool{}
	inPaths := false
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
			inPaths, path, op = trimmed == "paths:", "", ""
		case !inPaths:
		case indent == 2 && strings.HasPrefix(trimmed, "/") && strings.HasSuffix(trimmed, ":"):
			path, op = strings.TrimSuffix(trimmed, ":"), ""
		case indent == 4 && path != "":
			if m := specMethod.FindStringSubmatch(trimmed); m != nil {
				op = strings.ToUpper(m[1]) + " " + path
				out[op] = false
			} else {
				op = ""
			}
		case indent == 6 && op != "" && trimmed == "security:":
			out[op] = true
		}
	}
	return out
}

// contractProbeUserID / contractProbeSessionID are the fixed principal the
// status probe authenticates as. They are fixed (not uuid.New()) so
// contractAuthRepo can vouch for exactly this session and nothing else.
var (
	contractProbeUserID    = uuid.MustParse("00000000-0000-4000-8000-0000000c0de1")
	contractProbeSessionID = uuid.MustParse("00000000-0000-4000-8000-0000000c0de2")
)

// contractAuthRepo is the auth repository fullRouteOptions wires: still empty,
// except for the single session lookup the auth middleware now performs on every
// authenticated request. The embedded interface is nil, so calling any OTHER
// method panics exactly as the previous plain `nil` repo did — this probe never
// gets past the middleware into a handler that would need one.
type contractAuthRepo struct {
	auth.Repository
}

func (contractAuthRepo) GetActiveSessionForAccessToken(_ context.Context, id uuid.UUID) (sqlcgen.GetActiveSessionForAccessTokenRow, error) {
	if id != contractProbeSessionID {
		return sqlcgen.GetActiveSessionForAccessTokenRow{}, pgx.ErrNoRows
	}
	return sqlcgen.GetActiveSessionForAccessTokenRow{ID: id, UserID: contractProbeUserID}, nil
}
