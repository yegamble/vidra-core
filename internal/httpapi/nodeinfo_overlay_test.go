package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/instancesettings"
)

// TestNodeInfoHonoursAdminOverlay: both NodeInfo documents are what remote
// instances and instance directories read to decide whether to link or list
// this server. They used to read the BOOT env (cfg.InstanceName,
// cfg.RegistrationEnabled), so an admin who renamed the instance or closed
// sign-ups in the UI kept advertising the old name and openRegistrations=true
// to the whole fediverse until the next restart (and forever, if the env was
// never edited). They must read the same overlay-aware seam as GET /instance.
func TestNodeInfoHonoursAdminOverlay(t *testing.T) {
	cfg := fedTestConfig()
	repo := newFedRepoFor(cfg)
	srv, _, _, _, _ := videoServerFullWith(t, cfg, []Option{
		WithFederationService(federation.NewService(repo, federation.WithBaseURL(cfg.PublicBaseURL))),
	})
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	nodeInfo20 := func() nodeInfoResponse {
		t.Helper()
		rec := get(t, srv, "/api/v1/nodeinfo")
		var doc nodeInfoResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal 2.0: %v", err)
		}
		return doc
	}
	nodeInfo21 := func() nodeInfo21Document {
		t.Helper()
		rec := get(t, srv, "/nodeinfo/2.1")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /nodeinfo/2.1 = %d; body=%s", rec.Code, rec.Body.String())
		}
		var doc nodeInfo21Document
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("unmarshal 2.1: %v", err)
		}
		return doc
	}

	// Before any override, the config values are what is advertised.
	if got := nodeInfo20().Instance.Name; got != "Vidra Test" {
		t.Fatalf("2.0 instance.name before override = %q, want Vidra Test", got)
	}
	if !nodeInfo21().OpenRegistrations {
		t.Fatal("2.1 openRegistrations before override = false, want true")
	}

	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings",
		`{"`+instancesettings.KeyInstanceName+`":"Renamed Tube"}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d; body=%s", rec.Code, rec.Body.String())
	}
	setToggle(t, srv, adminTok, instancesettings.KeyRegistrationEnabled, false)

	if got := nodeInfo20().Instance.Name; got != "Renamed Tube" {
		t.Errorf("2.0 instance.name = %q, want the admin override Renamed Tube", got)
	}
	doc := nodeInfo21()
	if got := doc.Metadata["nodeName"]; got != "Renamed Tube" {
		t.Errorf("2.1 metadata.nodeName = %v, want the admin override Renamed Tube", got)
	}
	if doc.OpenRegistrations {
		t.Error("2.1 openRegistrations = true after the admin closed registration, want false")
	}
}
