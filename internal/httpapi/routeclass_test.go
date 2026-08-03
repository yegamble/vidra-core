package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// registeredMethodPaths returns every "METHOD template" the fully-wired router
// exposes. It reuses the OpenAPI guard's option set so every conditionally
// mounted feature (media, uploads, live, messaging) is present.
func registeredMethodPaths(t *testing.T) map[string]bool {
	t.Helper()
	srv := New(testConfig(), nil, nil, fullRouteOptions()...)
	out := map[string]bool{}
	for _, r := range srv.Handler().Routes() {
		out[r.Method+" "+r.Path] = true
	}
	return out
}

// TestMediaRouteTemplatesAreRegistered is the anti-rot guard for the media
// exemption list. The route classifier matches on exact Echo templates, so a
// renamed or removed route would silently drop back onto the API rate-limit
// budget and the 30s deadline — the exact regression 5a/5b exist to fix, and one
// that no other test would notice. Fail loudly instead.
func TestMediaRouteTemplatesAreRegistered(t *testing.T) {
	registered := registeredMethodPaths(t)
	for tmpl := range mediaRouteTemplates {
		if !registered["GET "+tmpl] {
			t.Errorf("mediaRouteTemplates lists %q but no GET route is registered for it — update the set in the same change as the route", tmpl)
		}
	}
}

// TestUploadRouteTemplatesAreRegistered is the same guard for the upload set.
// Each template is checked against every method that could carry a body, since
// the set is method-agnostic by design (chunks are PUT, the rest POST).
func TestUploadRouteTemplatesAreRegistered(t *testing.T) {
	registered := registeredMethodPaths(t)
	for tmpl := range uploadRouteTemplates {
		if !registered["POST "+tmpl] && !registered["PUT "+tmpl] {
			t.Errorf("uploadRouteTemplates lists %q but no POST/PUT route is registered for it", tmpl)
		}
	}
}

// TestIsMediaRouteIgnoresWrites proves the classifier is method-aware: several
// media templates are shared with an owner-only write (POST /videos/:id/thumbnail
// sets the custom thumbnail), and a write must never inherit the media budget.
func TestIsMediaRouteIgnoresWrites(t *testing.T) {
	e := New(testConfig(), nil, nil).Handler()

	get := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	get.SetPath("/api/v1/videos/:id/thumbnail")
	if !isMediaRoute(get) {
		t.Error("GET /videos/:id/thumbnail should be classified as media")
	}

	post := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
	post.SetPath("/api/v1/videos/:id/thumbnail")
	if isMediaRoute(post) {
		t.Error("POST /videos/:id/thumbnail is a write and must stay on the API budget")
	}
	// It is not a streaming route either — the custom thumbnail is bounded by the
	// ordinary 8M body limit, so it has no claim on the long deadline.
	if isStreamingRoute(post) {
		t.Error("POST /videos/:id/thumbnail must not get the streaming deadline")
	}
}

// TestIsStreamingRouteCoversUploads proves the resumable chunk PUT — the route
// whose 30s deadline made slow uplinks retry forever — is classified as
// streaming, while an ordinary JSON route is not.
func TestIsStreamingRouteCoversUploads(t *testing.T) {
	e := New(testConfig(), nil, nil).Handler()

	chunk := e.NewContext(httptest.NewRequest(http.MethodPut, "/", nil), httptest.NewRecorder())
	chunk.SetPath(uploadChunkRoutePath)
	if !isStreamingRoute(chunk) {
		t.Errorf("%s must be classified as streaming", uploadChunkRoutePath)
	}
	if isMediaRoute(chunk) {
		t.Error("an upload must not draw on the media rate-limit budget")
	}

	api := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	api.SetPath("/api/v1/videos")
	if isStreamingRoute(api) {
		t.Error("the video listing is ordinary API work and must keep the 30s deadline")
	}
}
