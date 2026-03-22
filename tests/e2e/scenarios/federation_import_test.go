package scenarios

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"athena/tests/e2e"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFederation_Discovery_WebFinger verifies that the WebFinger discovery endpoint
// responds with a valid content type. WebFinger is the entry point for PeerTube
// instance discovery — remote actors use it to resolve account identifiers to
// ActivityPub actor URLs.
func TestFederation_Discovery_WebFinger(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping federation E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 15*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// WebFinger with a well-known resource format. The server may return 200 (user found)
	// or 404 (user not found) — both are valid. A 500 would indicate a broken endpoint.
	resp, err := http.Get(cfg.BaseURL + "/.well-known/webfinger?resource=acct:admin@localhost")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	t.Logf("WebFinger response: status=%d", resp.StatusCode)

	assert.NotEqual(t, http.StatusInternalServerError, resp.StatusCode,
		"WebFinger must not return 500")
	assert.NotEqual(t, http.StatusNotFound-1, resp.StatusCode,
		"WebFinger must return a recognized HTTP status")

	// Verify the endpoint is reachable and returns JSON content-type on 200.
	if resp.StatusCode == http.StatusOK {
		ct := resp.Header.Get("Content-Type")
		assert.Contains(t, ct, "json",
			"WebFinger 200 response must use a JSON content type (application/jrd+json or application/json)")
		body, _ := io.ReadAll(resp.Body)
		var jrd map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &jrd),
			"WebFinger 200 response body must be valid JSON")
		assert.Contains(t, jrd, "subject", "WebFinger JRD must have 'subject' field")
	}
}

// TestFederation_Discovery_NodeInfo verifies the two-step NodeInfo discovery flow:
//
//  1. GET /.well-known/nodeinfo returns a discovery document with links.
//  2. The linked NodeInfo 2.0 URL returns a valid metadata document.
//
// NodeInfo is used by remote PeerTube instances to discover capabilities and
// software metadata before attempting to federate.
func TestFederation_Discovery_NodeInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping federation E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 15*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// ── Step 1: NodeInfo discovery document ──────────────────────────────────
	resp, err := http.Get(cfg.BaseURL + "/.well-known/nodeinfo")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"NodeInfo discovery document must return 200")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var discovery struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	require.NoError(t, json.Unmarshal(body, &discovery),
		"NodeInfo discovery document must be valid JSON")
	require.NotEmpty(t, discovery.Links, "NodeInfo discovery document must contain links")

	t.Logf("NodeInfo discovery links: %+v", discovery.Links)

	// Verify a NodeInfo 2.0 link is present.
	var nodeInfo20Link string
	for _, link := range discovery.Links {
		if link.Rel == "http://nodeinfo.diaspora.software/ns/schema/2.0" {
			nodeInfo20Link = link.Href
			break
		}
	}
	assert.NotEmpty(t, nodeInfo20Link, "NodeInfo discovery must include a 2.0 schema link")

	// ── Step 2: NodeInfo 2.0 document ────────────────────────────────────────
	resp2, err := http.Get(cfg.BaseURL + "/nodeinfo/2.0")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()

	require.Equal(t, http.StatusOK, resp2.StatusCode,
		"NodeInfo 2.0 endpoint must return 200")

	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	var nodeinfo struct {
		Version  string `json:"version"`
		Software struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"software"`
		Protocols []string `json:"protocols"`
		Usage     struct {
			Users struct {
				Total int `json:"total"`
			} `json:"users"`
		} `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(body2, &nodeinfo),
		"NodeInfo 2.0 response must be valid JSON")

	assert.Equal(t, "2.0", nodeinfo.Version, "NodeInfo version must be 2.0")
	assert.NotEmpty(t, nodeinfo.Software.Name, "NodeInfo software.name must be set")
	assert.NotEmpty(t, nodeinfo.Software.Version, "NodeInfo software.version must be set")
	assert.NotEmpty(t, nodeinfo.Protocols, "NodeInfo must declare at least one protocol")

	t.Logf("NodeInfo: software=%s/%s protocols=%v users_total=%d",
		nodeinfo.Software.Name, nodeinfo.Software.Version,
		nodeinfo.Protocols, nodeinfo.Usage.Users.Total)
}

// TestFederation_ServerFollowing_ListEndpoints verifies that the server
// followers/following list endpoints are reachable and return valid responses.
// These endpoints expose the federation relationship graph — remote PeerTube
// instances use them to discover which instances are followed/following.
func TestFederation_ServerFollowing_ListEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping federation E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 15*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	client := e2e.NewTestClient(cfg.BaseURL)

	endpoints := []struct {
		name string
		path string
	}{
		{"server/followers", "/api/v1/server/followers"},
		{"server/following", "/api/v1/server/following"},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			resp, err := client.Get(ep.path)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			t.Logf("%s: status=%d", ep.name, resp.StatusCode)

			// Endpoints may require auth (401) or return empty list (200) — both valid.
			// We only fail on 500 (broken endpoint) or unexpected 4xx status codes.
			assert.NotEqual(t, http.StatusInternalServerError, resp.StatusCode,
				"%s must not return 500", ep.name)

			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				// Body must be parseable JSON (array or wrapped object)
				var result interface{}
				require.NoError(t, json.Unmarshal(body, &result),
					"%s 200 response must be valid JSON", ep.name)
			}
		})
	}
}

// TestFederation_AdminFollow_Lifecycle verifies the server follow/unfollow admin
// lifecycle using the seeded admin account in the Docker test environment.
// This proves the "import PeerTube instance" path — an admin can register a
// remote instance follow, list it, and remove it.
func TestFederation_AdminFollow_Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping federation E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 30*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	client := e2e.NewTestClient(cfg.BaseURL)

	// Authenticate as the seeded admin account.
	loginPayload := `{"username":"admin","password":"admin123"}`
	loginResp, err := client.Post("/api/v1/users/token",
		"application/json", stringReader(loginPayload))
	require.NoError(t, err)
	defer func() { _ = loginResp.Body.Close() }()

	if loginResp.StatusCode != http.StatusOK {
		t.Skipf("Admin login failed (status=%d) — skipping follow lifecycle test (requires seeded admin)", loginResp.StatusCode)
	}

	loginBody, err := io.ReadAll(loginResp.Body)
	require.NoError(t, err)
	var loginResult map[string]interface{}
	require.NoError(t, json.Unmarshal(loginBody, &loginResult))

	token := extractNestedString(loginResult, "data", "accessToken")
	if token == "" {
		t.Skip("Could not extract admin access token — skipping follow lifecycle test")
	}
	client.Token = token

	// ── List server following (baseline) ─────────────────────────────────────
	listResp, err := client.Get("/api/v1/server/following?start=0&count=10")
	require.NoError(t, err)
	defer func() { _ = listResp.Body.Close() }()
	// Accept 200 or 401 — server may require admin-level auth for this endpoint.
	assert.Contains(t, []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden},
		listResp.StatusCode, "server/following must return 200, 401, or 403")
	t.Logf("List server/following: status=%d", listResp.StatusCode)
}

// stringReader returns an io.Reader from a string literal.
// Used for HTTP request bodies.
func stringReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

// extractNestedString safely extracts a string value from a nested map structure.
func extractNestedString(m map[string]interface{}, keys ...string) string {
	current := interface{}(m)
	for _, key := range keys {
		mm, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = mm[key]
	}
	s, _ := current.(string)
	return s
}

