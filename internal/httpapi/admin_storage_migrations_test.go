package httpapi

import (
	"net/http"
	"testing"

	"github.com/vidra/vidra-core/internal/storagemigration"
)

// TestAdminStorageMigrationRoleAndConfigGates covers the admin surface an
// operator can reach WITHOUT a migration target configured, which is the default
// deployment. The routes are mounted either way on purpose — the campaign most
// in need of cancelling is one left behind by a configuration that has since
// changed — so they must answer sensibly rather than 404.
func TestAdminStorageMigrationRoleAndConfigGates(t *testing.T) {
	// No target backend: NewService(repo=nil, primary=nil, target=nil) is exactly
	// the wiring cmd/api produces when STORAGE_MIGRATION_TARGET_* is unset.
	srv, _, _, _, _ := videoServerFullWith(t, testConfig(),
		[]Option{WithStorageMigrationService(storagemigration.NewService(nil, nil, nil, storagemigration.Config{}))})
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	bobTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	const path = "/api/v1/admin/storage/migrations"

	if rec := postJSONAuth(srv, path, `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous start = %d, want 401", rec.Code)
	}
	if rec := postJSONAuth(srv, path, `{}`, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin start = %d, want 403", rec.Code)
	}
	// Configured off: a 503 that names the missing configuration, not a 404 that
	// makes the feature look absent.
	if rec := postJSONAuth(srv, path, `{}`, adminTok); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("start with no target configured = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	// A malformed id is rejected before anything touches the database.
	if rec := postJSONAuth(srv, path+"/not-a-uuid/cancel", `{}`, adminTok); rec.Code != http.StatusBadRequest {
		t.Errorf("cancel with a bad id = %d, want 400", rec.Code)
	}
	if rec := getWithAuth(srv, path+"/not-a-uuid", adminTok); rec.Code != http.StatusBadRequest {
		t.Errorf("get with a bad id = %d, want 400", rec.Code)
	}
	if rec := getWithAuth(srv, path, bobTok); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list = %d, want 403", rec.Code)
	}
}
