package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fedOffServer is an instance with federation switched off — the state of a
// server migrated from PeerTube whose operator has not turned federation on yet.
func fedOffServer() *Server {
	cfg := testConfig()
	cfg.PublicBaseURL = "https://videos.example"
	return New(cfg, nil, nil)
}

// While federation is off, an ActivityPub fetch of an actor or object id is
// 503 + Retry-After, never 404: PeerTube DELETES a remote actor or video it
// refreshes and gets a 404/410 for, while any other error is logged and the
// copy kept. A 404 here would erase this instance's channels, videos and the
// follows on them from every peer that once federated with it.
func TestFederationOffAnswersActivityPubWith503(t *testing.T) {
	srv := fedOffServer()
	id := uuid.NewString()
	paths := []string{
		"/accounts/ada", "/accounts/ada/outbox", "/accounts/ada/followers", "/accounts/ada/following",
		"/video-channels/films", "/video-channels/films/outbox",
		"/video-channels/films/followers", "/video-channels/films/following",
		"/videos/" + id, "/videos/watch/" + id, "/comments/" + id, "/remote-comments/" + id,
	}
	for _, accept := range []string{apAccept, `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`} {
		for _, path := range paths {
			rec := getAccept(t, srv, path, accept)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("GET %s (%s) = %d, want 503", path, accept, rec.Code)
				continue
			}
			if got := rec.Header().Get("Retry-After"); got != "86400" {
				t.Errorf("GET %s: Retry-After = %q, want 86400", path, got)
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "federation_disabled" {
				t.Errorf("GET %s: body = %s (err %v), want error.code federation_disabled", path, rec.Body.String(), err)
			}
		}
	}
	for _, path := range []string{"/inbox", "/accounts/ada/inbox", "/video-channels/films/inbox"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"type":"Follow"}`))
		req.Header.Set("Content-Type", "application/activity+json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "86400" {
			t.Errorf("POST %s = %d (Retry-After %q), want 503 + 86400", path, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}

// Everything that is not an ActivityPub request answers exactly as before:
// a browser or REST client on these paths, and the discovery documents, which
// must not advertise a federating server.
func TestFederationOffLeavesNonActivityPubRequestsAlone(t *testing.T) {
	srv := fedOffServer()
	for _, path := range []string{"/accounts/ada", "/video-channels/films", "/videos/" + uuid.NewString()} {
		for _, accept := range []string{"text/html,application/xhtml+xml", "application/json", ""} {
			if rec := getAccept(t, srv, path, accept); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s (Accept %q) = %d, want the unchanged 404", path, accept, rec.Code)
			}
		}
	}
	for _, path := range []string{"/.well-known/webfinger?resource=acct:ada@videos.example", "/.well-known/nodeinfo", "/nodeinfo/2.1"} {
		if rec := getAccept(t, srv, path, apAccept); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 with federation off", path, rec.Code)
		}
	}
	// A path that merely starts like an AP path is not one.
	for _, path := range []string{"/accounts", "/accounts/ada/videos", "/videos/watch", "/videos/not-a-uuid", "/api/v1/videos"} {
		if rec := getAccept(t, srv, path, apAccept); rec.Code == http.StatusServiceUnavailable {
			t.Errorf("GET %s = 503, want it untouched", path)
		}
	}
}

// With federation ON, the guard is not in the way.
func TestFederationOnServesActivityPubNot503(t *testing.T) {
	if rec := getAccept(t, fedServer(fedTestConfig()), "/accounts/ada", apAccept); rec.Code != http.StatusOK {
		t.Errorf("GET /accounts/ada with federation on = %d, want 200", rec.Code)
	}
}
