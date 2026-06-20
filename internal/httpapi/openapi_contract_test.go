package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/video"
)

// fullRouteOptions mounts every optional feature so the contract test enumerates
// the complete route surface. The wired dependencies are never invoked — only
// the routing table is inspected — so nil/zero collaborators are fine.
func fullRouteOptions() []Option {
	issuer := auth.NewTokenIssuer("contract-test-secret-contract-test-0", "vidra", "vidra", time.Minute)
	return []Option{
		WithAuthService(auth.NewService(nil, issuer, time.Hour), time.Minute),
		WithChannelService(channel.NewService(nil)),
		WithVideoService(video.NewService(nil, nil)),
	}
}

// TestOpenAPIContract is the documentation stop guard: it fails the build when
// the routes registered on the Echo router diverge from the operations declared
// in api/openapi.yaml. Add a route without documenting it, or document a path
// with no route behind it, and this test goes red. Keep code and contract in
// lock-step in the same change.
//
// The spec is parsed by indentation (see api/openapi.yaml for the required
// shape) rather than a YAML library, so the test adds no dependency.
func TestOpenAPIContract(t *testing.T) {
	specPath := filepath.Join("..", "..", "api", "openapi.yaml")

	declared := declaredOperations(t, specPath)
	registered := registeredOperations(t)

	for op := range registered {
		if !declared[op] {
			t.Errorf("route %q is registered but NOT documented in api/openapi.yaml — document it in the same change", op)
		}
	}
	for op := range declared {
		if !registered[op] {
			t.Errorf("api/openapi.yaml documents %q but no route is registered — remove it from the spec or restore the route", op)
		}
	}

	if t.Failed() {
		t.Logf("registered routes:\n  %s", strings.Join(sortedKeys(registered), "\n  "))
		t.Logf("documented operations:\n  %s", strings.Join(sortedKeys(declared), "\n  "))
	}
}

// echoParam matches an Echo path parameter (":id") so it can be normalised to
// the OpenAPI form ("{id}") before comparison.
var echoParam = regexp.MustCompile(`:([^/]+)`)

// registeredOperations returns the live set of "METHOD /path" operations from
// the Echo router, with path parameters normalised to OpenAPI braces.
func registeredOperations(t *testing.T) map[string]bool {
	t.Helper()
	// Construct the server with every optional feature mounted so the test sees
	// the full route surface (auth routes are conditional on an auth service).
	// The dependencies are never invoked — only the routing table is read.
	srv := New(testConfig(), nil, nil, fullRouteOptions()...)
	httpMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "HEAD": true, "OPTIONS": true,
	}
	ops := map[string]bool{}
	for _, r := range srv.Handler().Routes() {
		if !httpMethods[r.Method] || strings.Contains(r.Path, "*") {
			continue // skip Echo's internal/wildcard routes
		}
		ops[r.Method+" "+echoParam.ReplaceAllString(r.Path, "{$1}")] = true
	}
	return ops
}

var specMethod = regexp.MustCompile(`^(get|post|put|patch|delete|head|options):\s*$`)

// declaredOperations parses api/openapi.yaml by indentation and returns the set
// of "METHOD /path" operations it declares.
func declaredOperations(t *testing.T, specPath string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read OpenAPI spec at %s: %v", specPath, err)
	}

	ops := map[string]bool{}
	inPaths := false
	current := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			inPaths = trimmed == "paths:"
			current = ""
		case !inPaths:
			// outside the paths block
		case indent == 2 && strings.HasPrefix(trimmed, "/") && strings.HasSuffix(trimmed, ":"):
			current = strings.TrimSuffix(trimmed, ":")
		case indent == 4 && current != "":
			if m := specMethod.FindStringSubmatch(trimmed); m != nil {
				ops[strings.ToUpper(m[1])+" "+current] = true
			}
		}
	}
	if len(ops) == 0 {
		t.Fatalf("no operations parsed from %s — check the file's indentation shape", specPath)
	}
	return ops
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
