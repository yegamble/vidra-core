package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/federation"
)

type fakeFedRepo struct{ users, videos, comments int64 }

func (f fakeFedRepo) CountUsers(context.Context) (int64, error)        { return f.users, nil }
func (f fakeFedRepo) CountPublicVideos(context.Context) (int64, error) { return f.videos, nil }
func (f fakeFedRepo) CountComments(context.Context) (int64, error)     { return f.comments, nil }

func fedTestConfig() *config.Config {
	c := testConfig()
	c.FederationEnabled = true
	c.PublicBaseURL = "https://videos.example"
	c.RegistrationEnabled = true
	return c
}

func fedServer(cfg *config.Config) *Server {
	return New(cfg, nil, nil, WithFederationService(federation.NewService(fakeFedRepo{users: 7, videos: 3, comments: 11})))
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestNodeInfoDiscovery(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/.well-known/nodeinfo")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body nodeInfoDiscovery
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Links) != 1 {
		t.Fatalf("links = %d, want 1", len(body.Links))
	}
	if body.Links[0].Rel != nodeInfo21Rel {
		t.Errorf("rel = %q, want %q", body.Links[0].Rel, nodeInfo21Rel)
	}
	if want := "https://videos.example/nodeinfo/2.1"; body.Links[0].Href != want {
		t.Errorf("href = %q, want %q", body.Links[0].Href, want)
	}
}

func TestNodeInfo21Document(t *testing.T) {
	rec := get(t, fedServer(fedTestConfig()), "/nodeinfo/2.1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "schema/2.1") {
		t.Errorf("content-type = %q, want the nodeinfo 2.1 profile", ct)
	}
	var doc nodeInfo21Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Version != "2.1" {
		t.Errorf("version = %q, want 2.1", doc.Version)
	}
	if doc.Software.Name != "vidra" {
		t.Errorf("software.name = %q, want vidra", doc.Software.Name)
	}
	if len(doc.Protocols) != 1 || doc.Protocols[0] != "activitypub" {
		t.Errorf("protocols = %v, want [activitypub]", doc.Protocols)
	}
	if !doc.OpenRegistrations {
		t.Errorf("openRegistrations = false, want true (RegistrationEnabled)")
	}
	if doc.Usage.Users.Total != 7 || doc.Usage.LocalPosts != 3 || doc.Usage.LocalComments != 11 {
		t.Errorf("usage = %+v, want {7,3,11}", doc.Usage)
	}
}

// The routes are a prod-safe opt-in: absent (404) when FEDERATION_ENABLED is off,
// even though the service is wired — mirroring the dev-endpoint exclusion.
func TestNodeInfoAbsentWhenFederationDisabled(t *testing.T) {
	cfg := fedTestConfig()
	cfg.FederationEnabled = false
	srv := fedServer(cfg)
	for _, path := range []string{"/.well-known/nodeinfo", "/nodeinfo/2.1"} {
		if rec := get(t, srv, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when federation disabled", path, rec.Code)
		}
	}
}
