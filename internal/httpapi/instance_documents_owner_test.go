package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/observability"
)

const instanceDocPath = "/api/v1/admin/instance-documents/"

// Custom JS and CSS run in, and restyle, every visitor's browser, so writing
// them is the owner's alone: an ordinary admin is 403 owner_only, while the
// homepage document (sanitised markdown) keeps its admin gate.
func TestCustomCodeDocumentsAreOwnerOnlyToWrite(t *testing.T) {
	srv, repo := instanceConfigServerWithRepo(t)
	var logs bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	ownerTok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	adminTok, _ := registerUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	setRole(t, repo, "bob@example.test", "admin")

	for _, name := range []string{"custom_js", "custom_css"} {
		rec := sendJSONAuth(srv, http.MethodPut, instanceDocPath+name, `{"body":"x"}`, adminTok)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "owner_only" {
			t.Errorf("non-owner admin PUT %s = %d %q, want 403 owner_only; body=%s", name, rec.Code, errorCode(t, rec), rec.Body.String())
		}
	}
	if ev := findAudit(auditEvents(t, &logs), observability.ActionAdminInstanceDocumentUpdate, observability.ResultFailure); ev == nil {
		t.Error("the refused write was not audited")
	}
	pub := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/api/v1/instance/custom.js", nil))
	if pub.Code != http.StatusNotFound {
		t.Errorf("custom.js after a refused write = %d, want 404 (nothing stored)", pub.Code)
	}

	// The homepage is not executable content: still any admin.
	if rec := sendJSONAuth(srv, http.MethodPut, instanceDocPath+"homepage", `{"body":"# hi"}`, adminTok); rec.Code != http.StatusOK {
		t.Errorf("non-owner admin PUT homepage = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// The owner writes both.
	for _, name := range []string{"custom_js", "custom_css"} {
		if rec := sendJSONAuth(srv, http.MethodPut, instanceDocPath+name, `{"body":"x"}`, ownerTok); rec.Code != http.StatusOK {
			t.Errorf("owner PUT %s = %d, want 200; body=%s", name, rec.Code, rec.Body.String())
		}
	}

	// Removal stays with every admin: a non-owner admin who finds a script they
	// did not approve must be able to take it down.
	for _, name := range []string{"custom_js", "custom_css"} {
		if rec := sendJSONAuth(srv, http.MethodPut, instanceDocPath+name, `{"body":""}`, adminTok); rec.Code != http.StatusOK {
			t.Errorf("non-owner admin clearing %s = %d, want 200; body=%s", name, rec.Code, rec.Body.String())
		}
	}
	pub = httptest.NewRecorder()
	srv.Handler().ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/api/v1/instance/custom.js", nil))
	if pub.Code != http.StatusNotFound {
		t.Errorf("custom.js after the admin cleared it = %d, want 404", pub.Code)
	}
	if strings.Contains(logs.String(), `"x"`) && strings.Contains(logs.String(), "body=x") {
		t.Error("audit log leaked a document body")
	}
}
